// This local example generates an ephemeral key, signs registration and one
// session call, and prints raw transactions. It never transmits private keys.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func main() {
	genesis := flag.String("genesis", "", "write a local dev genesis instead of signing")
	ownerAddress := flag.String("owner", "", "genesis funded owner address")
	nonce := flag.Uint64("owner-nonce", 0, "current owner nonce")
	block := flag.Uint64("block", 1, "block containing registration")
	expiry := flag.Uint64("expiry", 100, "last valid block (inclusive)")
	targetHex := flag.String("target", "0x000000000000000000000000000000000000beef", "single allowed target")
	inputHex := flag.String("input", "0x01020304", "exact session call calldata")
	chainID := flag.Int64("chain-id", 424242, "local chain signing domain")
	flag.Parse()
	if *genesis != "" {
		if !common.IsHexAddress(*ownerAddress) {
			panic("-owner address required")
		}
		writeGenesis(*genesis, common.HexToAddress(*ownerAddress), common.HexToAddress(*targetHex), *chainID)
		return
	}
	ownerKey, err := crypto.HexToECDSA(strings.TrimPrefix(os.Getenv("APLO_OWNER_KEY"), "0x"))
	if err != nil {
		panic("set APLO_OWNER_KEY locally to your development owner key")
	}
	key, err := crypto.GenerateKey()
	must(err)
	address := crypto.PubkeyToAddress(key.PublicKey)
	if *expiry < *block || *expiry-*block > sessionkeys.MaxLifetime {
		panic("expiry outside session horizon")
	}
	input := common.FromHex(*inputHex)
	if len(input) < 4 {
		panic("input needs a selector")
	}
	var selector [4]byte
	copy(selector[:], input[:4])
	target := common.HexToAddress(*targetHex)
	// Budgets and prices are dev units, not production fee advice.
	aplo := big.NewInt(1000000)
	gaplo := big.NewInt(10000000)
	price := big.NewInt(1)
	proofHash := sessionkeys.ProofHash(crypto.PubkeyToAddress(ownerKey.PublicKey), address, target, [][4]byte{selector}, aplo, gaplo, new(big.Int).SetUint64(*expiry), big.NewInt(*chainID))
	proof, err := crypto.Sign(proofHash[:], key)
	must(err)
	create, err := sessionkeys.ABI.Pack("CreateSessionKey", address, target, [][4]byte{selector}, aplo, gaplo, new(big.Int).SetUint64(*expiry), proof)
	must(err)
	signer := types.NewEIP155Signer(big.NewInt(*chainID))
	registration, err := types.SignTx(types.NewTransaction(*nonce, sessionkeys.Address, new(big.Int), 1500000, price, create), signer, ownerKey)
	must(err)
	sessionCall, err := types.SignTx(types.NewTransaction(0, target, big.NewInt(7), 100000, price, input), signer, key)
	must(err)
	rawCreate, err := registration.MarshalBinary()
	must(err)
	rawCall, err := sessionCall.MarshalBinary()
	must(err)
	fmt.Println("session address:", address.Hex())
	fmt.Println("owner registration:", hexutil.Encode(rawCreate))
	fmt.Println("session call (submit after registration):", hexutil.Encode(rawCall))
	// The private session key stays only in this process. Persist securely in a
	// client keystore if more calls will be signed later; never log it.
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func writeGenesis(path string, owner, target common.Address, id int64) {
	config := *params.AllEthashProtocolChanges
	config.ChainID = big.NewInt(id)
	config.ChainID_ALT = big.NewInt(id)
	config.SessionKeysBlock = big.NewInt(0)
	config.LondonBlock = nil
	config.ArrowGlacierBlock = nil
	config.GrayGlacierBlock = nil
	supply := new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil)
	g := core.Genesis{Config: &config, GasLimit: 30000000, Difficulty: big.NewInt(1), Alloc: core.GenesisAlloc{
		owner:                             {Balance: new(big.Int).Set(supply)},
		params.GAploContractAddress:       {Code: common.FromHex(params.GAPLO), Balance: new(big.Int), Storage: map[common.Hash]common.Hash{sessionkeys.GaploSlot(owner): common.BigToHash(supply), common.BigToHash(big.NewInt(2)): common.BigToHash(supply)}},
		params.AploContractAddress:        {Code: []byte{0}, Balance: new(big.Int)},
		params.BlockOracleContractAddress: {Code: []byte{0}, Balance: new(big.Int)},
		target:                            {Code: common.FromHex("326000523360205260406000f3"), Balance: new(big.Int)},
	}}
	if target == owner || target == sessionkeys.Address || target == params.GAploContractAddress || target == params.AploContractAddress || target == params.BlockOracleContractAddress {
		panic("target address conflicts with owner/protocol")
	}
	must(config.CheckConfigForkOrder())
	data, err := json.MarshalIndent(g, "", "  ")
	must(err)
	must(os.WriteFile(path, data, 0600))
	fmt.Println("local genesis written:", path)
}
