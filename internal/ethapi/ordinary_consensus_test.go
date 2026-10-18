package ethapi

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

func TestOrdinaryConsensusGasEstimation(t *testing.T) {
	db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := common.HexToAddress("0xcd086110af6b5c2e81fe9860bd023d1bfc33aeea")
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetNonce(sessionkeys.Address, 1)
	db.SetBalance(owner, big.NewInt(123))
	config := *params.TestChainConfig
	b := &sessionRPCBackend{backendMock: &backendMock{config: &config, current: &types.Header{Number: big.NewInt(2), GasLimit: 30000000, Difficulty: big.NewInt(1), BaseFee: big.NewInt(1)}}, db: db}
	fund := func(amount int64) {
		db.SetState(params.GAploContractAddress, sessionkeys.GaploSlot(owner), common.BigToHash(big.NewInt(amount)))
		db.SetState(params.GAploContractAddress, common.HexToHash("0x2"), common.BigToHash(big.NewInt(amount)))
	}
	fund(500000)
	data := hexutil.Bytes(common.FromHex("0x70a08231000000000000000000000000cd086110af6b5c2e81fe9860bd023d1bfc33aeea"))
	price := hexutil.Big(*big.NewInt(2))
	block := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	before := db.Copy().IntermediateRoot(false)
	gas, err := DoEstimateGas(context.Background(), b, TransactionArgs{From: &owner, To: &params.GAploContractAddress, GasPrice: &price, Data: &data}, block, 30000000)
	if err != nil || gas != 23993 {
		t.Fatalf("ordinary GAplo estimate must include historical cold storage: %d %v", gas, err)
	}
	if db.Copy().IntermediateRoot(false) != before {
		t.Fatal("estimate mutated live state")
	}
	target := common.HexToAddress("0xbbbb")
	value := hexutil.Big(*big.NewInt(7))
	args := TransactionArgs{From: &owner, To: &target, GasPrice: &price, Value: &value}
	for _, amount := range []int64{42007, 42006} {
		fund(amount)
		before = db.Copy().IntermediateRoot(false)
		gas, err = DoEstimateGas(context.Background(), b, args, block, 30000000)
		if amount == 42007 && (err != nil || gas != 21000) {
			t.Fatalf("exact ordinary dual reservation: %d %v", gas, err)
		}
		if amount == 42006 && err == nil {
			t.Fatal("estimation ignored additional ordinary GAplo value reservation")
		}
		if db.Copy().IntermediateRoot(false) != before {
			t.Fatal("estimation/refusal mutated live state")
		}
	}
}
