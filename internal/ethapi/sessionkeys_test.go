package ethapi

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// The backend supplies real StateDB copies and the production VM/transition.
type sessionRPCBackend struct {
	*backendMock
	db *state.StateDB
}

func (b *sessionRPCBackend) StateAndHeaderByNumberOrHash(
	context.Context, rpc.BlockNumberOrHash,
) (*state.StateDB, *types.Header, error) {
	return b.db.Copy(), b.current, nil
}

func (b *sessionRPCBackend) BlockByNumberOrHash(
	context.Context, rpc.BlockNumberOrHash,
) (*types.Block, error) {
	return types.NewBlockWithHeader(b.current), nil
}

func (b *sessionRPCBackend) GetEVM(
	_ context.Context,
	msg core.Message,
	db *state.StateDB,
	h *types.Header,
	cfg *vm.Config,
) (*vm.EVM, func() error, error) {
	ctx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		BlockNumber: h.Number,
		Time:        big.NewInt(0),
		Difficulty:  big.NewInt(1),
		GasLimit:    h.GasLimit,
		BaseFee:     h.BaseFee,
	}
	return vm.NewEVM(ctx, core.NewEVMTxContext(msg), db, b.config, *cfg, nil), func() error { return nil }, nil
}

func TestSessionKeysRPCSimulation(t *testing.T) {
	db, _ := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetNonce(sessionkeys.Address, 1)
	owner := common.HexToAddress("0xbeef")
	key := common.HexToAddress("0xaaaa")
	target := common.HexToAddress("0xbbbb")
	db.SetBalance(owner, big.NewInt(100))
	db.SetState(
		params.GAploContractAddress, common.BigToHash(big.NewInt(2)),
		common.BigToHash(big.NewInt(1000000)),
	)
	db.SetState(params.GAploContractAddress, sessionkeys.GaploSlot(owner), common.BigToHash(big.NewInt(1000000)))
	// Return ORIGIN and CALLER in two words.
	db.SetCode(target, common.FromHex("326000523360205260406000f3"))
	if err := sessionkeys.Create(
		db, owner, key, target, [][4]byte{{1, 2, 3, 4}},
		big.NewInt(100), big.NewInt(80000), 20, 1,
	); err != nil {
		t.Fatal(err)
	}
	config := *params.TestChainConfig
	b := &sessionRPCBackend{
		backendMock: &backendMock{
			config: &config,
			current: &types.Header{
				Number:     big.NewInt(2),
				GasLimit:   30000000,
				Difficulty: big.NewInt(1),
				BaseFee:    big.NewInt(1),
			},
		},
		db: db,
	}
	data := hexutil.Bytes{1, 2, 3, 4}
	price := hexutil.Big(*big.NewInt(1))
	value := hexutil.Big(*big.NewInt(7))
	args := TransactionArgs{From: &key, To: &target, Data: &data, GasPrice: &price, Value: &value}
	block := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	estimated, err := DoEstimateGas(context.Background(), b, args, block, 30000000)
	if err != nil {
		t.Fatal(err)
	}
	if estimated < 21000 || estimated > 80000 {
		t.Fatalf("budget estimate %d", estimated)
	}
	gas := hexutil.Uint64(estimated)
	args.Gas = &gas
	result, err := DoCall(context.Background(), b, args, block, nil, 0, 30000000)
	if err != nil || result.Err != nil {
		t.Fatalf("call: %v %v", err, result)
	}
	if len(result.ReturnData) != 64 ||
		common.BytesToAddress(result.ReturnData[:32]) != owner ||
		common.BytesToAddress(result.ReturnData[32:]) != key {
		t.Fatal("RPC simulation lost delegated identities")
	}
	if db.GetNonce(key) != 0 ||
		db.GetBalance(owner).Cmp(big.NewInt(100)) != 0 ||
		sessionkeys.Get(db, key).AploSpent.Cmp(big.NewInt(100)) != 0 {
		t.Fatal("RPC mutated live state")
	}
	data[0] = 9
	if _, err := DoCall(context.Background(), b, args, block, nil, 0, 30000000); err == nil {
		t.Fatal("RPC bypassed whitelist")
	}
}
