package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
)

type sessionPoolChain struct {
	*testBlockChain
	block *types.Block
}

func (c *sessionPoolChain) CurrentBlock() *types.Block { return c.block }
func TestSessionKeysPoolOwnerFundsAndHeadPolicy(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 2, big.NewInt(100), big.NewInt(1000000))
	chain := &sessionPoolChain{testBlockChain: &testBlockChain{statedb: f.db, gasLimit: f.header.GasLimit, chainHeadFeed: new(event.Feed)}, block: types.NewBlockWithHeader(f.header)}
	cfg := DefaultTxPoolConfig
	cfg.Journal = ""
	pool := NewTxPool(cfg, f.config, chain)
	pool.Stop() // exercise state/policy deterministically after stopping housekeeping
	tx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(11), 100000, independentSessionSelector[:])
	if err := pool.validateTx(tx, true); err != nil {
		t.Fatalf("owner funded session rejected: %v", err)
	}
	bad := f.signedTx(t, f.sessionKey, 0, f.target, new(big.Int), 100000, []byte{9, 8, 7, 6})
	if err := pool.validateTx(bad, true); err == nil {
		t.Fatal("pool admitted unlisted selector")
	}
	// A state snapshot models reorg back to an unrevoked parent.
	before := f.db.Snapshot()
	if err := sessionkeys.Revoke(f.db, f.owner, f.session); err != nil {
		t.Fatal(err)
	}
	if err := pool.validateTx(tx, true); err == nil || pool.validSessionQueued(f.session, tx) {
		t.Fatal("pool accepted revoked key")
	}
	f.db.RevertToSnapshot(before)
	if err := pool.validateTx(tx, true); err != nil {
		t.Fatalf("reorg policy did not recover: %v", err)
	}
	before = f.db.Snapshot()
	// Strict pending nonce lanes must drop a revoked transaction and dependent suffix.
	pending := newTxList(true)
	pending.Add(tx, 10)
	later := f.signedTx(t, f.sessionKey, 1, f.target, new(big.Int), 100000, independentSessionSelector[:])
	pending.Add(later, 10)
	if err := sessionkeys.Revoke(f.db, f.owner, f.session); err != nil {
		t.Fatal(err)
	}
	removed, invalid := pending.FilterSession(func(tx *types.Transaction) bool { return pool.validSessionQueued(f.session, tx) })
	if len(removed)+len(invalid) != 2 || pending.Len() != 0 {
		t.Fatal("pending revoked nonce lane retained")
	}
	f.db.RevertToSnapshot(before)
	next := types.CopyHeader(f.header)
	next.Number = big.NewInt(2)
	chain.block = types.NewBlockWithHeader(next)
	if err := pool.validateTx(tx, true); err == nil {
		t.Fatal("pool admitted session after next-block expiry")
	}
	chain.block = types.NewBlockWithHeader(f.header)
	f.db.SetBalance(f.owner, big.NewInt(1))
	if err := pool.validateTx(tx, true); err == nil {
		t.Fatal("pool ignored owner native shortage")
	}
	f.db.SetBalance(f.owner, big.NewInt(1000))
	f.db.SetState(common.HexToAddress("0x1234"), sessionkeys.GaploSlot(f.owner), common.Hash{})
	if err := pool.validateTx(tx, true); err == nil {
		t.Fatal("pool ignored owner GAplo shortage")
	}
}

func TestSessionKeysPoolCumulativeBudgetsAndSharedOwner(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(150000))
	chain := &sessionPoolChain{testBlockChain: &testBlockChain{statedb: f.db, gasLimit: f.header.GasLimit, chainHeadFeed: new(event.Feed)}, block: types.NewBlockWithHeader(f.header)}
	cfg := DefaultTxPoolConfig
	cfg.Journal = ""
	pool := NewTxPool(cfg, f.config, chain)
	pool.Stop()
	first := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(60), 60000, independentSessionSelector[:])
	pool.pending[f.session] = newTxList(true)
	pool.pending[f.session].Add(first, 10)
	next := f.signedTx(t, f.sessionKey, 1, f.target, big.NewInt(60), 60000, independentSessionSelector[:])
	if pool.canReserveSessionFunds(f.session, next) || pool.validateTx(next, true) == nil {
		t.Fatal("cumulative APLO budget overcommit admitted")
	}
	next = f.signedTx(t, f.sessionKey, 1, f.target, new(big.Int), 100000, independentSessionSelector[:])
	if pool.canReserveSessionFunds(f.session, next) {
		t.Fatal("cumulative GAplo budget overcommit admitted")
	}
	replacement := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(60), 70000, independentSessionSelector[:])
	if !pool.canReserveSessionFunds(f.session, replacement) {
		t.Fatal("replacement counted old nonce twice")
	}
	secondKey, _ := crypto.GenerateKey()
	secondAddr := crypto.PubkeyToAddress(secondKey.PublicKey)
	if err := sessionkeys.Create(f.db, f.owner, secondAddr, f.target, [][4]byte{independentSessionSelector}, big.NewInt(100), big.NewInt(150000), 100, 1); err != nil {
		t.Fatal(err)
	}
	sibling := f.signedTx(t, secondKey, 0, f.target, big.NewInt(60), 60000, independentSessionSelector[:])
	f.db.SetBalance(f.owner, big.NewInt(100))
	if pool.canReserveSessionFunds(secondAddr, sibling) || pool.validateTx(sibling, true) == nil {
		t.Fatal("shared owner native overcommit admitted")
	}
	f.db.SetBalance(f.owner, big.NewInt(1000))
	f.db.SetState(common.HexToAddress("0x1234"), sessionkeys.GaploSlot(f.owner), common.BigToHash(big.NewInt(100000)))
	if pool.canReserveSessionFunds(secondAddr, sibling) {
		t.Fatal("shared owner GAplo overcommit admitted")
	}
}

func TestSessionKeysPoolReorgKeepsFundedPrefixAndConsistentHeap(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(150), big.NewInt(1000000))
	chain := &sessionPoolChain{testBlockChain: &testBlockChain{statedb: f.db, gasLimit: f.header.GasLimit, chainHeadFeed: new(event.Feed)}, block: types.NewBlockWithHeader(f.header)}
	cfg := DefaultTxPoolConfig
	cfg.Journal = ""
	pool := NewTxPool(cfg, f.config, chain)
	pool.Stop()
	list := newTxList(true)
	pool.pending[f.session] = list
	for nonce := uint64(0); nonce < 2; nonce++ {
		tx := f.signedTx(t, f.sessionKey, nonce, f.target, big.NewInt(60), 60000, independentSessionSelector[:])
		list.Add(tx, 10)
		pool.all.Add(tx, true)
	}
	// A reorg lowers owner funds: retain nonce zero and requeue nonce one.
	f.db.SetBalance(f.owner, big.NewInt(100))
	pool.demoteUnexecutables()
	if pool.pending[f.session].Len() != 1 || pool.pending[f.session].LastElement().Nonce() != 0 || pool.queue[f.session].Len() != 1 || pool.queue[f.session].LastElement().Nonce() != 1 {
		t.Fatal("head change discarded funded prefix or lost suffix")
	}
	ready := pool.pending[f.session].Ready(0)
	if len(ready) != 1 || ready[0] == nil || ready[0].Nonce() != 0 || pool.pending[f.session].Len() != 0 {
		t.Fatal("reservation demotion corrupted heap")
	}
	// Explicit delegation policy removal must rebuild its strict nonce heap.
	list = newTxList(true)
	for nonce := uint64(0); nonce < 3; nonce++ {
		list.Add(f.signedTx(t, f.sessionKey, nonce, f.target, new(big.Int), 60000, independentSessionSelector[:]), 10)
	}
	removed, invalid := list.FilterSession(func(tx *types.Transaction) bool { return tx.Nonce() != 1 })
	if len(removed) != 1 || len(invalid) != 1 {
		t.Fatal("policy suffix removal failed")
	}
	ready = list.Ready(0)
	if len(ready) != 1 || ready[0] == nil || ready[0].Nonce() != 0 || list.Len() != 0 || len(*list.txs.index) != 0 {
		t.Fatal("policy filter left stale heap entries")
	}
}
