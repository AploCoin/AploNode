// Command sessionkeys-smoke exercises a disposable localhost --dev node. It
// creates synthetic session keys in memory and uses the unlocked dev faucet.
// Never point it at a chain containing real funds.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus/clique"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

var ctx context.Context
var r *rpc.Client
var client *ethclient.Client

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func require(ok bool, text string) {
	if !ok {
		panic(text)
	}
}
func input(sig string, words ...[]byte) []byte {
	b := common.CopyBytes(crypto.Keccak256([]byte(sig))[:4])
	for _, w := range words {
		b = append(b, common.LeftPadBytes(w, 32)...)
	}
	return b
}
func call(to common.Address, data []byte, block string, from *common.Address) []byte {
	args := map[string]interface{}{"to": to, "data": hexutil.Bytes(data)}
	if from != nil {
		args["from"] = *from
	}
	var out hexutil.Bytes
	must(r.CallContext(ctx, &out, "eth_call", args, block))
	return out
}
func uintCall(to common.Address, sig string, words ...[]byte) *big.Int {
	return new(big.Int).SetBytes(call(to, input(sig, words...), "latest", nil))
}
func session(key common.Address) []interface{} {
	data, err := sessionkeys.ABI.Pack("getSession", key)
	must(err)
	out, err := sessionkeys.ABI.Unpack("getSession", call(sessionkeys.Address, data, "latest", nil))
	must(err)
	return out
}
func receipt(hash common.Hash) *types.Receipt {
	for {
		got, err := client.TransactionReceipt(ctx, hash)
		if err == nil {
			require(got.Status == 1, "transaction reverted: "+hash.Hex())
			return got
		}
		select {
		case <-ctx.Done():
			panic(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}
func sendOwner(owner, to common.Address, data []byte) *types.Receipt {
	var hash common.Hash
	price, err := client.SuggestGasPrice(ctx)
	must(err)
	must(r.CallContext(ctx, &hash, "eth_sendTransaction", map[string]interface{}{"from": owner, "to": to, "data": hexutil.Bytes(data), "gas": hexutil.Uint64(1500000), "gasPrice": (*hexutil.Big)(price)}))
	return receipt(hash)
}
func snapshot(owner, key common.Address) string {
	native, err := client.BalanceAt(ctx, owner, nil)
	must(err)
	nonce, err := client.NonceAt(ctx, owner, nil)
	must(err)
	keyNonce, err := client.NonceAt(ctx, key, nil)
	must(err)
	keyNative, err := client.BalanceAt(ctx, key, nil)
	must(err)
	b, err := json.Marshal([]interface{}{native.String(), keyNative.String(), uintCall(params.GAploContractAddress, "balanceOf(address)", key[:]).String(), uintCall(params.GAploContractAddress, "balanceOf(address)", owner[:]).String(), uintCall(params.GAploContractAddress, "totalSupply()").String(), nonce, keyNonce, session(key)})
	must(err)
	return string(b)
}
func main() {
	endpoint := flag.String("rpc", "http://127.0.0.1:19645", "disposable localhost --dev RPC")
	output := flag.String("out", "", "optional JSON evidence path")
	flag.Parse()
	u, err := url.Parse(*endpoint)
	must(err)
	require(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"), "only localhost HTTP is allowed")
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err = rpc.DialContext(ctx, *endpoint)
	must(err)
	defer r.Close()
	client = ethclient.NewClient(r)
	var accounts []common.Address
	must(r.CallContext(ctx, &accounts, "eth_accounts"))
	require(len(accounts) == 1, "requires fresh --dev chain with one unlocked synthetic faucet")
	owner := accounts[0]
	chainID, err := client.ChainID(ctx)
	must(err)
	require(chainID.Int64() == 1337, "requires --dev chain ID 1337")
	key, err := crypto.GenerateKey()
	must(err)
	keyAddr := crypto.PubkeyToAddress(key.PublicKey)
	number, err := client.BlockNumber(ctx)
	must(err)
	budget := new(big.Int).Exp(big.NewInt(10), big.NewInt(21), nil)
	expiry := new(big.Int).SetUint64(number + 500)
	hash := sessionkeys.ProofHash(owner, keyAddr, params.GAploContractAddress, [][4]byte{params.GAploMineSelector}, new(big.Int), budget, expiry, chainID)
	proof, err := crypto.Sign(hash[:], key)
	must(err)
	data, err := sessionkeys.ABI.Pack("CreateSessionKey", keyAddr, params.GAploContractAddress, [][4]byte{params.GAploMineSelector}, new(big.Int), budget, expiry, proof)
	must(err)
	registered := sendOwner(owner, sessionkeys.Address, data)
	stake := new(big.Int).Mul(big.NewInt(1000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	require(uintCall(params.AploContractAddress, "getStake(address)", owner[:]).Sign() == 0, "requires fresh unstaked --dev faucet")
	staked := sendOwner(owner, params.AploContractAddress, input("stake(uint256)", stake.Bytes()))
	require(uintCall(params.AploContractAddress, "getStake(address)", owner[:]).Cmp(stake) == 0, "owner stake mismatch")
	// Both reported targets, both original/relevant addresses, four sender shapes,
	// real genesis and a later historical state, latest and pending. No gas field.
	before := snapshot(owner, keyAddr)
	zero := common.Address{}
	unfunded := common.HexToAddress("0xcd086110af6b5c2e81fe9860bd023d1bfc33aeea")
	count := 0
	for _, target := range []common.Address{params.GAploContractAddress, params.AploContractAddress} {
		for _, account := range []common.Address{owner, unfunded} {
			for _, block := range []string{"0x0", hexutil.EncodeBig(staked.BlockNumber), "latest", "pending"} {
				control := call(target, input("balanceOf(address)", account[:]), block, &owner)
				for _, from := range []*common.Address{nil, &zero, &unfunded, &owner} {
					got := call(target, input("balanceOf(address)", account[:]), block, from)
					require(len(got) == 32 && common.Bytes2Hex(got) == common.Bytes2Hex(control), "sender-dependent balance result")
					count++
				}
			}
		}
	}
	// A forbidden zero-price fake call from the session must still be rejected.
	var ignored hexutil.Bytes
	err = r.CallContext(ctx, &ignored, "eth_call", map[string]interface{}{"from": keyAddr, "to": params.AploContractAddress, "data": hexutil.Bytes(input("balanceOf(address)", owner[:]))}, "latest")
	require(err != nil, "zero-fee session bypassed whitelist")
	require(snapshot(owner, keyAddr) == before, "eth_call changed live balances, supply, nonce or session budgets")
	// Mining parameters are keyed by CALLER (session); rewards use the owner stake.
	raw := call(params.GAploContractAddress, input("miner_params(address)", keyAddr[:]), "latest", nil)
	require(len(raw) == 128, "invalid miner parameters")
	last := new(big.Int).SetBytes(raw[:32]).Uint64()
	difficulty := new(big.Int).SetBytes(raw[32:64])
	if difficulty.Sign() == 0 {
		difficulty = uintCall(params.GAploContractAddress, "DEFAULT_DIFFICULTY()")
	}
	require(difficulty.Sign() > 0, "invalid mining difficulty")
	var nonce [32]byte
	for n := uint64(0); ; n++ {
		if n%100000 == 0 {
			must(ctx.Err())
		}
		nonce = common.BigToHash(new(big.Int).SetUint64(n))
		packed := append(common.CopyBytes(keyAddr[:]), nonce[:]...)
		packed = append(packed, common.LeftPadBytes(difficulty.Bytes(), 32)...)
		packed = append(packed, raw[96:128]...)
		packed = append(packed, raw[64:96]...)
		if new(big.Int).SetBytes(crypto.Keccak256(packed)).Cmp(difficulty) < 0 {
			break
		}
	}
	for {
		n, err := client.BlockNumber(ctx)
		must(err)
		if n >= last+20 {
			break
		}
		select {
		case <-ctx.Done():
			panic(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	price, err := client.SuggestGasPrice(ctx)
	must(err)
	unsigned := types.NewTransaction(0, params.GAploContractAddress, new(big.Int), 500000, price, input("mine(bytes32)", nonce[:]))
	signed, err := types.SignTx(unsigned, types.NewEIP155Signer(chainID), key)
	must(err)
	ownerBefore := uintCall(params.GAploContractAddress, "balanceOf(address)", owner[:])
	must(client.SendTransaction(ctx, signed))
	mined := receipt(signed.Hash())
	actualFee := new(big.Int).Mul(new(big.Int).SetUint64(mined.GasUsed), price)
	record := session(keyAddr)
	require(record[3].(*big.Int).Cmp(new(big.Int).Sub(budget, actualFee)) == 0, "fee budget does not equal actual fee")
	require(record[2].(*big.Int).Sign() == 0 && record[4].(*big.Int).Uint64() == 1, "native budget or session nonce changed incorrectly")
	afterParams := call(params.GAploContractAddress, input("miner_params(address)", keyAddr[:]), "latest", nil)
	require(new(big.Int).SetBytes(afterParams[64:96]).Cmp(new(big.Int).Add(new(big.Int).SetBytes(raw[64:96]), big.NewInt(1))) == 0, "mining share was not recorded")
	header, err := client.HeaderByNumber(ctx, mined.BlockNumber)
	must(err)
	effective := new(big.Int).Set(price)
	if header.BaseFee != nil && effective.Cmp(header.BaseFee) > 0 {
		effective.Set(header.BaseFee)
	}
	reward := new(big.Int).Mul(new(big.Int).SetUint64(mined.GasUsed), effective)
	reward.Div(reward, params.GAploRewardCoef)
	reward.Mul(reward, big.NewInt(11))
	reward.Div(reward, big.NewInt(10))
	// In Clique the header coinbase is zero; recover the actual block author.
	// The faucet receives the tip, but still pays the burned London base fee.
	ownerAfter := uintCall(params.GAploContractAddress, "balanceOf(address)", owner[:])
	author, err := crypto.SigToPub(clique.SealHash(header).Bytes(), header.Extra[len(header.Extra)-65:])
	must(err)
	baseFeePaid := new(big.Int)
	if header.BaseFee != nil {
		baseFeePaid.Mul(new(big.Int).SetUint64(mined.GasUsed), header.BaseFee)
	}
	wantDelta := new(big.Int).Sub(reward, baseFeePaid)
	require(crypto.PubkeyToAddress(*author) == owner && new(big.Int).Sub(ownerAfter, ownerBefore).Cmp(wantDelta) == 0, fmt.Sprintf("mining reward mismatch: delta=%s expected=%s", new(big.Int).Sub(ownerAfter, ownerBefore), wantDelta))
	require(uintCall(params.GAploContractAddress, "balanceOf(address)", keyAddr[:]).Sign() == 0, "session received owner fee refund")
	// An ordinary signed EOA with no GAplo cannot send a paid transaction.
	unfundedKey, err := crypto.GenerateKey()
	must(err)
	unfundedTx, err := types.SignTx(types.NewTransaction(0, params.AploContractAddress, new(big.Int), 100000, price, input("balanceOf(address)", owner[:])), types.NewEIP155Signer(chainID), unfundedKey)
	must(err)
	require(client.SendTransaction(ctx, unfundedTx) != nil, "unfunded ordinary transaction accepted")
	// Replay and wrong target are rejected by the real transaction pool.
	require(client.SendTransaction(ctx, signed) != nil, "session nonce replay accepted")
	bad, err := types.SignTx(types.NewTransaction(1, params.AploContractAddress, new(big.Int), 100000, price, input("balanceOf(address)", owner[:])), types.NewEIP155Signer(chainID), key)
	must(err)
	require(client.SendTransaction(ctx, bad) != nil, "session target whitelist bypassed")
	revokeData, err := sessionkeys.ABI.Pack("RevokeSessionKey", keyAddr)
	must(err)
	revoked := sendOwner(owner, sessionkeys.Address, revokeData)
	require(session(keyAddr)[0].(common.Address) == (common.Address{}), "session remains active")
	post, err := types.SignTx(types.NewTransaction(1, params.GAploContractAddress, new(big.Int), 500000, price, input("mine(bytes32)", nonce[:])), types.NewEIP155Signer(chainID), key)
	must(err)
	require(client.SendTransaction(ctx, post) != nil, "revoked session transaction accepted")
	evidence := map[string]interface{}{"rpc": *endpoint, "owner": owner, "session": keyAddr, "chain_id": chainID.String(), "balance_calls": count, "simulation_state_unchanged": true, "register": registered.TxHash, "stake": staked.TxHash, "mine": mined.TxHash, "revoke": revoked.TxHash, "mine_gas": mined.GasUsed, "gas_price": price.String(), "actual_fee_budget_debit": actualFee.String(), "owner_mining_reward": reward.String(), "base_fee_burned": baseFeePaid.String(), "owner_gaplo_delta": wantDelta.String(), "unfunded_ordinary_rejected": true, "replay_whitelist_revoke_rejected": true, "synthetic_keys": true, "wallet_extension_tested": false}
	b, err := json.MarshalIndent(evidence, "", "  ")
	must(err)
	fmt.Println(string(b))
	if *output != "" {
		must(os.WriteFile(*output, append(b, '\n'), 0600))
	}
}
