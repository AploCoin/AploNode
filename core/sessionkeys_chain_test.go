package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// Exercise actual canonical-head replacement, not just StateDB.Copy or snapshots.
func TestSessionKeysInsertChainReorgAndRestart(t *testing.T) {
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner, session := crypto.PubkeyToAddress(ownerKey.PublicKey), crypto.PubkeyToAddress(key.PublicKey)
	target := common.HexToAddress("0x9090")
	config := *params.TestChainConfig
	config.SessionKeysBlock = big.NewInt(1)
	config.LondonBlock, config.ArrowGlacierBlock, config.GrayGlacierBlock = nil, nil, nil
	genesisSpec := &Genesis{Config: &config, GasLimit: 8_000_000, Difficulty: big.NewInt(131072), Timestamp: 1, Alloc: GenesisAlloc{
		owner: {Balance: big.NewInt(1000)},
		params.GAploContractAddress: {Balance: new(big.Int), Code: common.FromHex(params.GAPLO), Storage: map[common.Hash]common.Hash{
			sessionkeys.GaploSlot(owner): common.BigToHash(big.NewInt(1_000_000_000)),
			common.HexToHash("0x2"):      common.BigToHash(big.NewInt(1_000_000_000)),
		}},
		target: {Balance: new(big.Int), Code: independentCallerOriginCode()},
	}}
	genDB, chainDB := rawdb.NewMemoryDatabase(), rawdb.NewMemoryDatabase()
	genesis := genesisSpec.MustCommit(genDB)
	genesisSpec.MustCommit(chainDB)
	engine := ethash.NewFaker()
	chain, err := NewBlockChain(chainDB, nil, &config, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { chain.Stop() })
	selectors := [][4]byte{independentSessionSelector}
	aplo, gas, expiry := big.NewInt(100), big.NewInt(10_000_000), big.NewInt(5)
	hash := sessionkeys.ProofHash(owner, session, target, selectors, aplo, gas, expiry, config.ChainID)
	proof, err := crypto.Sign(hash[:], key)
	if err != nil {
		t.Fatal(err)
	}
	create, err := sessionkeys.ABI.Pack("CreateSessionKey", session, target, selectors, aplo, gas, expiry, proof)
	if err != nil {
		t.Fatal(err)
	}
	revoke, _ := sessionkeys.ABI.Pack("RevokeSessionKey", session)
	ownerSigner := types.LatestSigner(&config)
	register, err := types.SignTx(types.NewTransaction(0, sessionkeys.Address, new(big.Int), 700_000, big.NewInt(1), create), ownerSigner, ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	revokeTx, err := types.SignTx(types.NewTransaction(1, sessionkeys.Address, new(big.Int), 400_000, big.NewInt(1), revoke), ownerSigner, ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	use, err := types.SignTx(types.NewTransaction(0, target, big.NewInt(11), 120_000, big.NewInt(1), independentSessionSelector[:]), ownerSigner, key)
	if err != nil {
		t.Fatal(err)
	}
	useAgain, err := types.SignTx(types.NewTransaction(1, target, big.NewInt(7), 120_000, big.NewInt(1), independentSessionSelector[:]), ownerSigner, key)
	if err != nil {
		t.Fatal(err)
	}
	branchA, _ := GenerateChain(&config, genesis, engine, genDB, 2, func(i int, b *BlockGen) {
		if i == 0 {
			b.AddTx(register)
		} else {
			b.AddTx(use)
		}
	})
	if _, err := chain.InsertChain(branchA); err != nil {
		t.Fatalf("import branch A: %v", err)
	}
	stateA, err := chain.State()
	if err != nil {
		t.Fatal(err)
	}
	s := sessionkeys.Get(stateA, session)
	if s == nil || s.Nonce != 1 || s.AploSpent.Cmp(big.NewInt(89)) != 0 {
		t.Fatalf("bad branch A session: %+v", s)
	}
	feeA := new(big.Int).Set(s.GAploSpent)
	branchB, _ := GenerateChain(&config, genesis, engine, genDB, 3, func(i int, b *BlockGen) {
		b.SetExtra([]byte("revoked branch"))
		if i == 0 {
			b.AddTx(register)
		}
		if i == 1 {
			b.AddTx(revokeTx)
		}
	})
	if _, err := chain.InsertChain(branchB); err != nil {
		t.Fatalf("import/reorg branch B: %v", err)
	}
	if chain.CurrentBlock().Hash() != branchB[2].Hash() {
		t.Fatal("longer branch B did not become canonical")
	}
	stateB, err := chain.State()
	if err != nil {
		t.Fatal(err)
	}
	if sessionkeys.Get(stateB, session) != nil || !sessionkeys.Used(stateB, session) || stateB.GetNonce(session) != 0 || stateB.GetBalance(target).Sign() != 0 || stateB.GetBalance(owner).Cmp(big.NewInt(1000)) != 0 {
		t.Fatal("reorg retained branch A session use or lost revocation tombstone")
	}
	extensionA, _ := GenerateChain(&config, branchA[1], engine, genDB, 2, func(i int, b *BlockGen) {
		if i == 1 {
			b.AddTx(useAgain)
		}
	})
	if _, err := chain.InsertChain(extensionA); err != nil {
		t.Fatalf("reorg back to A: %v", err)
	}
	if chain.CurrentBlock().Hash() != extensionA[1].Hash() {
		t.Fatal("longer branch A did not become canonical again")
	}
	assertCanonical := func() {
		t.Helper()
		st, err := chain.State()
		if err != nil {
			t.Fatal(err)
		}
		s := sessionkeys.Get(st, session)
		if s == nil || s.Nonce != 2 || s.AploSpent.Cmp(big.NewInt(82)) != 0 || s.GAploSpent.Cmp(feeA) >= 0 || st.GetNonce(owner) != 1 || st.GetBalance(owner).Cmp(big.NewInt(982)) != 0 || st.GetBalance(target).Cmp(big.NewInt(18)) != 0 {
			t.Fatalf("reorg/restart did not restore correct nonce, budgets, balances: %+v", s)
		}
	}
	assertCanonical()
	chain.Stop()
	chain, err = NewBlockChain(chainDB, nil, &config, ethash.NewFaker(), vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("reopen blockchain: %v", err)
	}
	assertCanonical()
}
