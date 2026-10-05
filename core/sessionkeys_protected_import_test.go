package core

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/trie"
)

// Pool policy is not a block-validity boundary. The processor must reject
// unprotected legacy transactions from used keys and direct registry calls.
func TestSessionKeysStateProcessorRejectsUnprotectedSessionAndRegistryTransactionsIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	// Disable only the optional EthPoW replay-protection rule so these failures
	// come from the native Session Keys validation below.
	f.config.EthPoWForkBlock = nil
	selector := independentSessionSelector
	if err := sessionkeys.Create(
		f.state, f.owner, f.session, f.target, [][4]byte{selector},
		big.NewInt(10), big.NewInt(1_000_000), 100, 0,
	); err != nil {
		t.Fatalf("seed session for import validation: %v", err)
	}
	control := unprotectedSessionTestTx(
		t, f.ownerKey, 0, f.target, big.NewInt(1), selector[:],
	)
	controlState := f.state.Copy()
	controlHeader := &types.Header{
		ParentHash: f.genesis.Hash(), Number: big.NewInt(1),
		GasLimit: 8_000_000, Time: f.genesis.Time() + 1,
		Difficulty: big.NewInt(1), Coinbase: f.coinbase, BaseFee: new(big.Int),
	}
	controlBlock := types.NewBlock(
		controlHeader, []*types.Transaction{control}, nil, nil, trie.NewStackTrie(nil),
	)
	controlReceipts, _, controlGas, err := f.chain.processor.Process(
		controlBlock, controlState, vm.Config{},
	)
	if err != nil || len(controlReceipts) != 1 ||
		controlReceipts[0].Status != types.ReceiptStatusSuccessful || controlGas == 0 {
		t.Fatalf("ordinary unprotected owner transaction failed: receipts=%v gas=%d err=%v", controlReceipts, controlGas, err)
	}
	revoke, err := sessionkeys.ABI.Pack("RevokeSessionKey", f.session)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		tx   *types.Transaction
	}{
		{
			name: "used session sender",
			tx: unprotectedSessionTestTx(t, f.sessionKey, 0, f.target, big.NewInt(1),
				append(selector[:], 0x01)),
		},
		{
			name: "direct registry call",
			tx:   unprotectedSessionTestTx(t, f.ownerKey, 0, sessionkeys.Address, new(big.Int), revoke),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.tx.Protected() {
				t.Fatal("fixture must contain an unprotected Homestead transaction")
			}
			state := f.state.Copy()
			before := state.IntermediateRoot(false)
			ownerNonce, sessionNonce := state.GetNonce(f.owner), state.GetNonce(f.session)
			ownerAPLO := new(big.Int).Set(state.GetBalance(f.owner))
			ownerGAplo := sessionkeys.GaploBalance(state, f.owner)
			remaining := sessionkeys.Get(state, f.session)
			header := &types.Header{
				ParentHash: f.genesis.Hash(), Number: big.NewInt(1),
				GasLimit: 8_000_000, Time: f.genesis.Time() + 1,
				Difficulty: big.NewInt(1), Coinbase: f.coinbase, BaseFee: new(big.Int),
			}
			block := types.NewBlock(header, []*types.Transaction{test.tx}, nil, nil, trie.NewStackTrie(nil))
			_, _, usedGas, err := f.chain.processor.Process(block, state, vm.Config{})
			if !errors.Is(err, sessionkeys.ErrInvalid) {
				t.Fatalf("processor error=%v, want %v", err, sessionkeys.ErrInvalid)
			}
			if usedGas != 0 {
				t.Fatalf("rejected block reported gas use %d, want zero", usedGas)
			}
			if after := state.IntermediateRoot(false); after != before {
				t.Fatalf("rejected block changed state root: before=%s after=%s", before, after)
			}
			if state.GetNonce(f.owner) != ownerNonce || state.GetNonce(f.session) != sessionNonce ||
				state.GetBalance(f.owner).Cmp(ownerAPLO) != 0 ||
				sessionkeys.GaploBalance(state, f.owner).Cmp(ownerGAplo) != 0 {
				t.Fatal("rejected block changed account nonce or owner funds")
			}
			current := sessionkeys.Get(state, f.session)
			if current == nil || current.Nonce != remaining.Nonce ||
				current.AploSpent.Cmp(remaining.AploSpent) != 0 ||
				current.GAploSpent.Cmp(remaining.GAploSpent) != 0 {
				t.Fatal("rejected block changed session nonce or allowances")
			}
		})
	}
}

func unprotectedSessionTestTx(
	t *testing.T,
	key *ecdsa.PrivateKey,
	nonce uint64,
	to common.Address,
	value *big.Int,
	data []byte,
) *types.Transaction {
	t.Helper()
	tx, err := types.SignTx(
		types.NewTransaction(nonce, to, value, 300_000, big.NewInt(1), data),
		types.HomesteadSigner{}, key,
	)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
