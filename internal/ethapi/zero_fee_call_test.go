package ethapi

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// Exercise the wallet's JSON request shape through ToMessage and the production
// transition, including omitted gas and sender. HTTP/block selection is also
// covered by the disposable-devnet smoke; this backend returns copied state.
func TestZeroFeeBalanceCall(t *testing.T) {
	db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := common.HexToAddress("0xcd086110af6b5c2e81fe9860bd023d1bfc33aeea")
	key := common.HexToAddress("0xaaaa")
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetNonce(sessionkeys.Address, 1)
	db.SetBalance(owner, big.NewInt(123))
	db.SetNonce(owner, 7)
	db.SetState(params.GAploContractAddress, sessionkeys.GaploSlot(owner), common.BigToHash(big.NewInt(456)))
	db.SetState(params.GAploContractAddress, common.HexToHash("0x2"), common.BigToHash(big.NewInt(456)))
	if err := sessionkeys.Create(db, owner, key, params.GAploContractAddress, [][4]byte{{0x70, 0xa0, 0x82, 0x31}}, big.NewInt(10), big.NewInt(20), 100, 1); err != nil {
		t.Fatal(err)
	}
	config := *params.TestChainConfig
	b := &sessionRPCBackend{backendMock: &backendMock{config: &config, current: &types.Header{Number: big.NewInt(2), GasLimit: 50000000, Difficulty: big.NewInt(1), BaseFee: big.NewInt(1)}}, db: db}
	before := db.Copy().IntermediateRoot(false)
	data := hexutil.Bytes(common.FromHex("0x70a08231000000000000000000000000cd086110af6b5c2e81fe9860bd023d1bfc33aeea"))
	zero := common.Address{}
	for _, target := range []common.Address{params.GAploContractAddress, params.AploContractAddress} {
		for i, from := range []*common.Address{nil, &zero, &owner, &key} {
			for _, number := range []rpc.BlockNumber{0, rpc.LatestBlockNumber, rpc.PendingBlockNumber} {
				t.Run(fmt.Sprintf("%s/from%d/block%d", target.Hex(), i, number), func(t *testing.T) {
					args := TransactionArgs{From: from, To: &target, Data: &data}
					result, err := DoCall(context.Background(), b, args, rpc.BlockNumberOrHashWithNumber(number), nil, 0, 50000000)
					// A session may read only its whitelisted target, even at zero fee.
					if from == &key && target != params.GAploContractAddress {
						if err == nil {
							t.Fatal("zero-fee simulation bypassed session target")
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						want := int64(456)
						if target == params.AploContractAddress {
							want = 123
						}
						if result.Err != nil || len(result.ReturnData) != 32 || new(big.Int).SetBytes(result.ReturnData).Cmp(big.NewInt(want)) != 0 {
							t.Fatalf("balance result: %+v", result)
						}
						gas, err := DoEstimateGas(context.Background(), b, args, rpc.BlockNumberOrHashWithNumber(number), 50000000)
						if err != nil || gas < 21000 || gas > 50000000 {
							t.Fatalf("estimate: gas=%d err=%v", gas, err)
						}
					}
					if after := db.Copy().IntermediateRoot(false); after != before {
						t.Fatal("simulation changed live balances, supply, nonce or session budgets")
					}
				})
			}
		}
	}
	// IsFake must not bypass expiry, selector or native allowance checks,
	// including when no fee will be burned.
	for _, policy := range []string{"expiry", "selector", "value allowance"} {
		t.Run(policy, func(t *testing.T) {
			args := TransactionArgs{From: &key, To: &params.GAploContractAddress, Data: &data}
			b.current.Number = big.NewInt(2)
			switch policy {
			case "expiry":
				b.current.Number = big.NewInt(101)
			case "selector":
				bad := hexutil.Bytes{1, 2, 3, 4}
				args.Data = &bad
			case "value allowance":
				value := hexutil.Big(*big.NewInt(11))
				args.Value = &value
			}
			if _, err := DoCall(context.Background(), b, args, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber), nil, 0, 50000000); err == nil {
				t.Fatal("zero-fee simulation bypassed session policy")
			}
			if after := db.Copy().IntermediateRoot(false); after != before {
				t.Fatal("rejected simulation mutated live state")
			}
		})
	}
}

// Empty fee purchase is a simulation rule, not a token/address workaround.
// The default sender must remain zero; an actual takeFee target call must still
// enforce the token's own zero-address burn rule.
func TestZeroFeeCallPreservesCallerAndTargetRules(t *testing.T) {
	db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetNonce(sessionkeys.Address, 1)
	target := common.HexToAddress("0xba1a")
	// Return CALLER, ORIGIN and SELFBALANCE, with an independently funded target.
	db.SetCode(target, common.FromHex("33600052326020524760405260606000f3"))
	db.SetBalance(target, big.NewInt(77))
	config := *params.TestChainConfig
	b := &sessionRPCBackend{backendMock: &backendMock{config: &config, current: &types.Header{Number: big.NewInt(2), GasLimit: 50000000, Difficulty: big.NewInt(1), BaseFee: big.NewInt(1)}}, db: db}
	before := db.Copy().IntermediateRoot(false)
	zero := common.Address{}
	price := hexutil.Big(*new(big.Int))
	for _, from := range []*common.Address{nil, &zero} {
		for _, gasPrice := range []*hexutil.Big{nil, &price} {
			result, err := DoCall(context.Background(), b, TransactionArgs{From: from, To: &target, GasPrice: gasPrice}, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber), nil, 0, 50000000)
			if err != nil {
				t.Fatal(err)
			}
			if result.Err != nil || len(result.ReturnData) != 96 || new(big.Int).SetBytes(result.ReturnData[:64]).Sign() != 0 || new(big.Int).SetBytes(result.ReturnData[64:]).Int64() != 77 {
				t.Fatalf("sender/target semantics changed: %+v", result)
			}
		}
	}
	burn := make(hexutil.Bytes, 36)
	// Derive rather than assume the fee method's selector.
	copy(burn[:4], crypto.Keccak256([]byte("takeFee(uint256)"))[:4])
	result, err := DoCall(context.Background(), b, TransactionArgs{To: &params.GAploContractAddress, Data: &burn}, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber), nil, 0, 50000000)
	if err != nil || result == nil || result.Err != vm.ErrExecutionReverted {
		t.Fatalf("target burn rule bypassed: result=%+v err=%v", result, err)
	}
	reason, err := abi.UnpackRevert(result.ReturnData)
	if err != nil || reason != "ERC20: burn from the zero address" {
		t.Fatalf("unexpected target revert: %q %v", reason, err)
	}
	if db.Copy().IntermediateRoot(false) != before {
		t.Fatal("simulation changed live state")
	}
}
