package core

import (
	"crypto/ecdsa"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// Pool admission must use the pending block's domain, including a reorg back
// across the existing EthPoW signing-domain boundary.
func TestSessionKeysPoolSigningDomainFollowsPendingBlock(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(1_000_000))
	f.config.EthPoWForkBlock = big.NewInt(3)
	f.config.ChainID_ALT = big.NewInt(2)
	chain := &sessionPoolChain{
		testBlockChain: &testBlockChain{
			statedb: f.db, gasLimit: f.header.GasLimit, chainHeadFeed: new(event.Feed),
		},
		block: types.NewBlockWithHeader(f.header),
	}
	cfg := DefaultTxPoolConfig
	cfg.Journal = ""
	pool := NewTxPool(cfg, f.config, chain)
	pool.Stop()
	primary := f.signedTx(t, f.sessionKey, 0, f.target, new(big.Int), 100_000, independentSessionSelector[:])
	alt, err := types.SignTx(
		types.NewTransaction(0, f.target, new(big.Int), 100_000, big.NewInt(1), independentSessionSelector[:]),
		types.NewLondonSigner(f.config.ChainID_ALT), f.sessionKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []struct {
		name string
		head int64
		good *types.Transaction
		bad  *types.Transaction
	}{
		{"before fork", 1, primary, alt},
		{"pending fork block", 2, alt, primary},
		{"reorg before fork", 1, primary, alt},
	} {
		t.Run(phase.name, func(t *testing.T) {
			header := types.CopyHeader(f.header)
			header.Number = big.NewInt(phase.head)
			chain.block = types.NewBlockWithHeader(header)
			pool.reset(nil, header)
			if err := pool.validateTx(phase.good, true); err != nil {
				t.Fatalf("valid pending-block domain rejected: %v", err)
			}
			if err := pool.validateTx(phase.bad, true); err == nil {
				t.Fatal("pool admitted the other chain-ID domain")
			}
			// A signed execution control confirms the accepted domain is also
			// the one accepted by consensus for the pending block.
			control := *f
			control.db = f.db.Copy()
			control.header = types.CopyHeader(header)
			control.header.Number = big.NewInt(phase.head + 1)
			receipt, err := control.apply(t, phase.good)
			if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
				t.Fatalf("consensus rejected pool's valid domain: receipt=%v err=%v", receipt, err)
			}
			if _, err := control.apply(t, phase.bad); err == nil {
				t.Fatal("consensus accepted the other chain-ID domain")
			}
		})
	}
}

func TestSessionKeysPoolDomainResetDropsPendingAndQueued(t *testing.T) {
	for _, phase := range []struct {
		name string
		from int64
		to   int64
	}{
		{"fork", 1, 2},
		{"reorg", 2, 1},
	} {
		t.Run(phase.name, func(t *testing.T) {
			f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(1_000_000))
			f.config.EthPoWForkBlock, f.config.ChainID_ALT = big.NewInt(3), big.NewInt(2)
			header := types.CopyHeader(f.header)
			header.Number = big.NewInt(phase.from)
			chain := &sessionPoolChain{
				testBlockChain: &testBlockChain{statedb: f.db, gasLimit: header.GasLimit, chainHeadFeed: new(event.Feed)},
				block:          types.NewBlockWithHeader(header),
			}
			cfg := DefaultTxPoolConfig
			cfg.Journal = ""
			pool := NewTxPool(cfg, f.config, chain)
			pool.Stop()
			oldSigner := types.MakeSigner(f.config, big.NewInt(phase.from+1))
			var txs []*types.Transaction
			for _, key := range []*ecdsa.PrivateKey{f.ownerKey, f.sessionKey} {
				baseNonce := uint64(0)
				if key == f.ownerKey {
					baseNonce = f.db.GetNonce(f.owner)
				}
				for _, nonce := range []uint64{baseNonce, baseNonce + 2} {
					tx, err := types.SignTx(
						types.NewTransaction(nonce, f.target, new(big.Int), 100_000, big.NewInt(1), independentSessionSelector[:]), oldSigner, key,
					)
					if err != nil {
						t.Fatal(err)
					}
					txs = append(txs, tx)
				}
			}
			errs, dirty := pool.addTxsLocked(txs, true)
			for _, err := range errs {
				if err != nil {
					t.Fatalf("seed signed pool transaction: %v", err)
				}
			}
			pool.promoteExecutables(dirty.flatten())
			if err := validateTxPoolInternals(pool); err != nil {
				t.Fatal(err)
			}
			for _, addr := range []common.Address{f.owner, f.session} {
				if pool.pending[addr] == nil || pool.pending[addr].Len() != 1 ||
					pool.queue[addr] == nil || pool.queue[addr].Len() != 1 {
					t.Fatal("fixture needs both pending and gapped queued transactions")
				}
			}
			header = types.CopyHeader(header)
			header.Number = big.NewInt(phase.to)
			chain.block = types.NewBlockWithHeader(header)
			pool.reset(nil, header)
			if pool.all.Count() != 0 || len(pool.pending) != 0 || len(pool.queue) != 0 {
				t.Fatal("domain reset retained stale lookup, pending or queued entries")
			}
			if err := validateTxPoolInternals(pool); err != nil {
				t.Fatal(err)
			}
			for _, tx := range txs {
				if pool.Has(tx.Hash()) {
					t.Fatal("stale transaction still advertised by pool")
				}
			}
			fresh, err := types.SignTx(
				types.NewTransaction(0, f.target, new(big.Int), 100_000, big.NewInt(1), independentSessionSelector[:]),
				types.MakeSigner(f.config, big.NewInt(phase.to+1)), f.sessionKey,
			)
			if err != nil {
				t.Fatal(err)
			}
			if errs, _ := pool.addTxsLocked([]*types.Transaction{fresh}, true); errs[0] != nil {
				t.Fatalf("active-domain replacement rejected: %v", errs[0])
			}
			if !pool.locals.containsTx(fresh) {
				t.Fatal("local signer classification did not follow the new domain")
			}
		})
	}
}

type sessionDomainConcurrentChain struct {
	*testBlockChain
	head atomic.Value
}

func (c *sessionDomainConcurrentChain) CurrentBlock() *types.Block {
	return c.head.Load().(*types.Block)
}

// Exercise public ingestion/status APIs concurrently with scheduled resets.
// Two prices also trigger pending replacement events sent under the pool lock.
func TestSessionKeysPoolConcurrentDomainResetAndIngestion(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(1_000_000))
	f.config.EthPoWForkBlock, f.config.ChainID_ALT = big.NewInt(3), big.NewInt(2)
	chain := &sessionDomainConcurrentChain{
		testBlockChain: &testBlockChain{statedb: f.db, gasLimit: f.header.GasLimit, chainHeadFeed: new(event.Feed)},
	}
	chain.head.Store(types.NewBlockWithHeader(f.header))
	cfg := DefaultTxPoolConfig
	cfg.Journal = ""
	pool := NewTxPool(cfg, f.config, chain)
	defer pool.Stop()
	var txs []*types.Transaction
	var hashes []common.Hash
	for _, domain := range []*big.Int{f.config.ChainID, f.config.ChainID_ALT} {
		for _, price := range []int64{1, 2} {
			tx, err := types.SignTx(
				types.NewTransaction(0, f.target, new(big.Int), 100_000, big.NewInt(price), independentSessionSelector[:]),
				types.NewLondonSigner(domain), f.sessionKey,
			)
			if err != nil {
				t.Fatal(err)
			}
			txs, hashes = append(txs, tx), append(hashes, tx.Hash())
		}
	}
	// Give replacement an executable predecessor before concurrent resets.
	if errs := pool.AddRemotesSync(txs[:1]); errs[0] != nil {
		t.Fatal(errs[0])
	}
	if errs := pool.AddRemotesSync(txs[1:2]); errs[0] != nil {
		t.Fatal(errs[0])
	}
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for i := 0; i < 100; i++ {
			pool.AddRemotes(txs)
			pool.Status(hashes)
		}
	}()
	for i := 0; i < 20; i++ {
		header := types.CopyHeader(f.header)
		header.Number = big.NewInt(1 + int64(i%2))
		chain.head.Store(types.NewBlockWithHeader(header))
		<-pool.requestReset(nil, header)
	}
	readers.Wait()
	pool.mu.RLock()
	signer := pool.signer
	pool.mu.RUnlock()
	<-pool.requestPromoteExecutables(newAccountSet(signer, f.session))
	if err := validateTxPoolInternals(pool); err != nil {
		t.Fatal(err)
	}
}

func TestSessionKeysPoolEthPoWRejectsUnprotectedOrdinarySender(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(1_000_000))
	// Even an unchanged chain ID must activate the existing protected-only rule.
	f.config.EthPoWForkBlock, f.config.ChainID_ALT = big.NewInt(3), new(big.Int).Set(f.config.ChainID)
	chain := &sessionPoolChain{
		testBlockChain: &testBlockChain{statedb: f.db, gasLimit: f.header.GasLimit, chainHeadFeed: new(event.Feed)},
		block:          types.NewBlockWithHeader(f.header),
	}
	cfg := DefaultTxPoolConfig
	cfg.Journal = ""
	pool := NewTxPool(cfg, f.config, chain)
	pool.Stop()
	tx := unprotectedSessionTestTx(t, f.ownerKey, f.db.GetNonce(f.owner), f.target, new(big.Int), independentSessionSelector[:])
	if errs, _ := pool.addTxsLocked([]*types.Transaction{tx}, true); errs[0] != nil {
		t.Fatalf("ordinary pre-fork unprotected control rejected: %v", errs[0])
	}
	header := types.CopyHeader(f.header)
	header.Number = big.NewInt(2)
	chain.block = types.NewBlockWithHeader(header)
	pool.reset(nil, header)
	if pool.Has(tx.Hash()) || pool.validateTx(tx, true) == nil {
		t.Fatal("pool retained or admitted an unprotected transaction at EthPoW")
	}
	header.Number = big.NewInt(1)
	chain.block = types.NewBlockWithHeader(header)
	pool.reset(nil, header)
	if err := pool.validateTx(tx, true); err != nil {
		t.Fatalf("reorg did not restore ordinary pre-fork admission: %v", err)
	}
	if sessionkeys.Get(f.db, f.session) == nil {
		t.Fatal("pool reset changed consensus registry state")
	}
}
