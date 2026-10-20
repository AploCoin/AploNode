package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

func TestZeroFeeSimulationPreservesTransactionFeePath(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fake         bool
		price, funds int64
		want         error
	}{
		{"zero-cost simulation", true, 0, 0, nil},
		{"zero-priced real message still burns", false, 0, 0, vm.ErrExecutionReverted},
		{"funded simulation still propagates burn error", true, 1, 100000, vm.ErrExecutionReverted},
		{"funded real message still propagates burn error", false, 1, 100000, vm.ErrExecutionReverted},
		{"unfunded real message", false, 1, 0, ErrInsufficientFunds},
		{"unfunded priced simulation", true, 1, 0, ErrInsufficientFunds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
			if err != nil {
				t.Fatal(err)
			}
			zero := common.Address{}
			target := params.AploContractAddress
			db.SetNonce(sessionkeys.Address, 1)
			db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
			db.SetState(params.GAploContractAddress, sessionkeys.GaploSlot(zero), common.BigToHash(big.NewInt(tc.funds)))
			db.SetState(params.GAploContractAddress, common.HexToHash("0x2"), common.BigToHash(big.NewInt(tc.funds)))
			input := common.FromHex("0x70a082310000000000000000000000000000000000000000000000000000000000000000")
			msg := types.NewMessage(zero, &target, 0, new(big.Int), 100000, big.NewInt(tc.price), big.NewInt(tc.price), big.NewInt(tc.price), input, nil, tc.fake)
			ctx := vm.BlockContext{CanTransfer: CanTransfer, Transfer: Transfer, GetHash: func(uint64) common.Hash { return common.Hash{} }, BlockNumber: big.NewInt(1), Time: big.NewInt(1), Difficulty: big.NewInt(1), GasLimit: 1000000, BaseFee: new(big.Int)}
			evm := vm.NewEVM(ctx, NewEVMTxContext(msg), db, params.TestChainConfig, vm.Config{NoBaseFee: true}, nil)
			gp := new(GasPool).AddGas(ctx.GasLimit)
			before := db.Copy().IntermediateRoot(false)
			result, err := ApplyMessage(evm, msg, gp)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			if err == nil && (result == nil || result.Err != nil) {
				t.Fatalf("target failed: %+v", result)
			}
			if err != nil && (gp.Gas() != ctx.GasLimit || db.Copy().IntermediateRoot(false) != before) {
				t.Fatal("rejected fee changed state or gas pool")
			}
		})
	}
}
