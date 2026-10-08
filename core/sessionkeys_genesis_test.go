package core

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

func TestSessionKeysNativeGenesisInstallsReservedAccounts(t *testing.T) {
	config := *params.TestChainConfig
	spec := &Genesis{
		Config:   &config,
		GasLimit: 8_000_000, Difficulty: big.NewInt(1),
		Alloc: GenesisAlloc{
			common.HexToAddress("0x9876"): {Balance: big.NewInt(12)},
		},
	}
	if _, ok := spec.Alloc[sessionkeys.Address]; ok {
		t.Fatal("test allocation unexpectedly supplies the registry")
	}
	if _, ok := spec.Alloc[params.GAploContractAddress]; ok {
		t.Fatal("test allocation unexpectedly supplies GAplo")
	}

	// ToBlock and Commit must normalize protocol accounts without modifying the user's allocation.
	block := spec.ToBlock()
	if len(spec.Alloc) != 1 {
		t.Fatalf("genesis normalization mutated the caller allocation: got %d accounts", len(spec.Alloc))
	}
	db := rawdb.NewMemoryDatabase()
	committed, err := spec.Commit(db)
	if err != nil {
		t.Fatalf("commit native genesis: %v", err)
	}
	if block.Root() != committed.Root() {
		t.Fatalf("ToBlock/Commit state roots differ: %s != %s", block.Root(), committed.Root())
	}
	assertNativeGenesisState(t, db, committed.Root())

	setupDB := rawdb.NewMemoryDatabase()
	setupSpec := &Genesis{Config: &config, GasLimit: 8_000_000, Difficulty: big.NewInt(1)}
	_, hash, err := SetupGenesisBlock(setupDB, setupSpec)
	if err != nil {
		t.Fatalf("SetupGenesisBlock native initialization: %v", err)
	}
	if hash != setupSpec.ToBlock().Hash() {
		t.Fatalf("SetupGenesisBlock hash=%s, want %s", hash, setupSpec.ToBlock().Hash())
	}
	assertNativeGenesisState(t, setupDB, hashStateRoot(t, setupDB, hash))
}

func assertNativeGenesisState(t *testing.T, db ethdb.Database, root common.Hash) {
	t.Helper()
	statedb, err := state.New(root, state.NewDatabase(db), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionkeys.ValidateState(statedb); err != nil {
		t.Fatalf("genesis lacks native protocol accounts: %v", err)
	}
	if statedb.GetNonce(sessionkeys.Address) != 1 || statedb.GetCodeSize(sessionkeys.Address) != 0 {
		t.Fatal("genesis registry is not reserved as code-free nonce one")
	}
	if !sessionkeys.CanonicalGaplo(statedb) {
		t.Fatal("genesis did not install canonical GAplo runtime")
	}
}

func hashStateRoot(t *testing.T, db ethdb.Database, hash common.Hash) common.Hash {
	t.Helper()
	header := rawdb.ReadHeader(db, hash, 0)
	if header == nil {
		t.Fatalf("missing genesis header for %s", hash)
	}
	return header.Root
}

func TestSessionKeysNativeGenesisRejectsConflictingAllocations(t *testing.T) {
	config := *params.TestChainConfig
	tests := []struct {
		name  string
		alloc GenesisAlloc
	}{
		{
			name: "registry code",
			alloc: GenesisAlloc{
				sessionkeys.Address: {Code: []byte{0x00}},
			},
		},
		{
			name: "registry storage",
			alloc: GenesisAlloc{
				sessionkeys.Address: {Storage: map[common.Hash]common.Hash{
					common.Hash{}: common.BigToHash(big.NewInt(1)),
				}},
			},
		},
		{
			name: "registry nonce above reservation",
			alloc: GenesisAlloc{
				sessionkeys.Address: {Nonce: 2},
			},
		},
		{
			name: "noncanonical GAplo code",
			alloc: GenesisAlloc{
				params.GAploContractAddress: {Code: []byte{0x00}},
			},
		},
		{
			name: "GAplo storage without runtime",
			alloc: GenesisAlloc{
				params.GAploContractAddress: {Storage: map[common.Hash]common.Hash{
					common.Hash{}: common.BigToHash(big.NewInt(1)),
				}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := &Genesis{
				Config: &config, GasLimit: 8_000_000, Difficulty: big.NewInt(1),
				Alloc: test.alloc,
			}
			if _, err := spec.Commit(rawdb.NewMemoryDatabase()); err == nil {
				t.Fatal("conflicting native protocol allocation was accepted")
			}
		})
	}
}

func TestSessionKeysSetupRejectsLegacyGenesisWithoutMigration(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	stateDB, err := state.New(
		common.Hash{}, state.NewDatabaseWithConfig(db, &trie.Config{Preimages: true}), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stateDB.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	// This pre-native genesis has canonical GAplo but lacks the registry reservation.
	legacyRoot, err := stateDB.Commit(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateDB.Database().TrieDB().Commit(legacyRoot, true, nil); err != nil {
		t.Fatal(err)
	}
	legacyBlock := types.NewBlock(
		&types.Header{
			Number: big.NewInt(0), GasLimit: 8_000_000,
			Difficulty: big.NewInt(1), Root: legacyRoot,
		},
		nil, nil, nil, trie.NewStackTrie(nil),
	)
	writeGenesisHead(db, legacyBlock, params.TestChainConfig)

	before := stateSnapshot(t, db)
	_, _, err = SetupGenesisBlock(db, nil)
	if err == nil || !strings.Contains(err.Error(), "fresh data directory") {
		t.Fatalf("legacy genesis setup error=%v, want explicit incompatibility", err)
	}
	if after := stateSnapshot(t, db); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected legacy genesis startup wrote state or modified metadata")
	}
	legacyState, err := state.New(legacyRoot, state.NewDatabase(db), nil)
	if err != nil {
		t.Fatal(err)
	}
	if legacyState.GetNonce(sessionkeys.Address) != 0 {
		t.Fatal("startup migrated the legacy registry instead of rejecting it")
	}
}

func writeGenesisHead(db ethdb.Database, block *types.Block, config *params.ChainConfig) {
	rawdb.WriteTd(db, block.Hash(), 0, block.Difficulty())
	rawdb.WriteBlock(db, block)
	rawdb.WriteCanonicalHash(db, block.Hash(), 0)
	rawdb.WriteHeadBlockHash(db, block.Hash())
	rawdb.WriteHeadFastBlockHash(db, block.Hash())
	rawdb.WriteHeadHeaderHash(db, block.Hash())
	rawdb.WriteChainConfig(db, block.Hash(), config)
}

func TestSessionKeysCommitGenesisStateRecoversNativeGenesis(t *testing.T) {
	config := *params.TestChainConfig
	owner := common.HexToAddress("0x9876")
	spec := &Genesis{
		Config:   &config,
		GasLimit: 8_000_000, Difficulty: big.NewInt(1),
		Alloc: GenesisAlloc{owner: {Balance: big.NewInt(12)}},
	}
	source := rawdb.NewMemoryDatabase()
	block, err := spec.Commit(source)
	if err != nil {
		t.Fatalf("commit source genesis: %v", err)
	}
	if block.Hash() == block.Root() {
		t.Fatal("test requires distinct genesis block hash and state root")
	}

	// Preserve the block metadata and normalized genesis spec while dropping trie state.
	recovery := rawdb.NewMemoryDatabase()
	writeGenesisHead(recovery, block, &config)
	rawdb.WriteGenesisStateSpec(
		recovery, block.Root(), rawdb.ReadGenesisStateSpec(source, block.Root()),
	)
	if err := CommitGenesisState(recovery, block.Hash()); err != nil {
		t.Fatalf("recover genesis state by header root: %v", err)
	}
	assertNativeGenesisState(t, recovery, block.Root())
}

func TestSessionKeysCommitGenesisStateRecoversDefaultGenesisWithoutSpec(t *testing.T) {
	spec := DefaultGenesisBlock()
	source := rawdb.NewMemoryDatabase()
	block, err := spec.Commit(source)
	if err != nil {
		t.Fatalf("commit default genesis: %v", err)
	}
	if block.Hash() == block.Root() {
		t.Fatal("test requires distinct default genesis block hash and state root")
	}

	// Mainnet has a known allocation fallback when the persisted spec is missing.
	recovery := rawdb.NewMemoryDatabase()
	writeGenesisHead(recovery, block, spec.Config)
	if len(rawdb.ReadGenesisStateSpec(recovery, block.Root())) != 0 {
		t.Fatal("recovery test unexpectedly copied a genesis allocation spec")
	}
	if err := CommitGenesisState(recovery, block.Hash()); err != nil {
		t.Fatalf("recover known default genesis allocation: %v", err)
	}
	assertNativeGenesisState(t, recovery, block.Root())
}

func TestSessionKeysCommitGenesisStateRejectsTamperedOrLegacySpecWithoutWrites(t *testing.T) {
	config := *params.TestChainConfig
	owner := common.HexToAddress("0x9876")
	spec := &Genesis{
		Config:   &config,
		GasLimit: 8_000_000, Difficulty: big.NewInt(1),
		Alloc: GenesisAlloc{owner: {Balance: big.NewInt(12)}},
	}
	source := rawdb.NewMemoryDatabase()
	block, err := spec.Commit(source)
	if err != nil {
		t.Fatalf("commit source genesis: %v", err)
	}
	validSpec := rawdb.ReadGenesisStateSpec(source, block.Root())

	tests := []struct {
		name string
		spec []byte
	}{
		{
			name: "tampered normalized allocation",
			spec: tamperGenesisBalance(t, validSpec, owner, big.NewInt(13)),
		},
		{
			name: "legacy allocation without registry reservation",
			spec: mustMarshalGenesisAlloc(t, GenesisAlloc{
				owner: {Balance: big.NewInt(12)},
				params.GAploContractAddress: {
					Code: common.FromHex(params.GAPLO), Balance: new(big.Int),
				},
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := rawdb.NewMemoryDatabase()
			writeGenesisHead(db, block, &config)
			rawdb.WriteGenesisStateSpec(db, block.Root(), test.spec)
			before := stateSnapshot(t, db)
			if err := CommitGenesisState(db, block.Hash()); err == nil {
				t.Fatal("invalid persisted genesis allocation was accepted")
			}
			if after := stateSnapshot(t, db); !reflect.DeepEqual(after, before) {
				t.Fatal("rejected genesis recovery wrote state or modified metadata")
			}
		})
	}
}

func tamperGenesisBalance(
	t *testing.T,
	blob []byte,
	owner common.Address,
	balance *big.Int,
) []byte {
	t.Helper()
	alloc := make(GenesisAlloc)
	if err := alloc.UnmarshalJSON(blob); err != nil {
		t.Fatal(err)
	}
	account := alloc[owner]
	account.Balance = balance
	alloc[owner] = account
	return mustMarshalGenesisAlloc(t, alloc)
}

func mustMarshalGenesisAlloc(t *testing.T, alloc GenesisAlloc) []byte {
	t.Helper()
	blob, err := json.Marshal(alloc)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func stateSnapshot(t *testing.T, db ethdb.Database) map[string][]byte {
	t.Helper()
	snapshot := make(map[string][]byte)
	iterator := db.NewIterator(nil, nil)
	defer iterator.Release()
	for iterator.Next() {
		snapshot[string(iterator.Key())] = append([]byte(nil), iterator.Value()...)
	}
	if err := iterator.Error(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSessionKeysNativeGenesisRequiresProtectedSigningDomain(t *testing.T) {
	tests := []struct {
		name   string
		update func(*params.ChainConfig)
	}{
		{
			name: "EIP155 disabled",
			update: func(config *params.ChainConfig) {
				config.EIP155Block = nil
			},
		},
		{
			name: "EIP155 scheduled after genesis",
			update: func(config *params.ChainConfig) {
				config.EIP155Block = big.NewInt(1)
			},
		},
		{
			name: "missing chain ID",
			update: func(config *params.ChainConfig) {
				config.ChainID = nil
			},
		},
		{
			name: "chain ID outside uint256",
			update: func(config *params.ChainConfig) {
				config.ChainID = new(big.Int).Lsh(big.NewInt(1), 256)
			},
		},
		{
			name: "missing alternate chain ID",
			update: func(config *params.ChainConfig) {
				config.EthPoWForkBlock = big.NewInt(0)
				config.ChainID_ALT = nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := *params.TestChainConfig
			test.update(&config)
			spec := &Genesis{
				Config: &config, GasLimit: 8_000_000, Difficulty: big.NewInt(1),
			}
			if _, err := spec.Commit(rawdb.NewMemoryDatabase()); err == nil {
				t.Fatal("genesis without a protected signing domain was accepted")
			}
		})
	}
}

func TestDeveloperGenesisRejectsReservedFaucetAddresses(t *testing.T) {
	tests := []struct {
		name    string
		address common.Address
	}{
		{name: "zero", address: common.Address{}},
		{name: "registry", address: sessionkeys.Address},
		{name: "GAplo", address: params.GAploContractAddress},
		{name: "native APLO", address: params.AploContractAddress},
		{name: "block oracle", address: params.BlockOracleContractAddress},
		{name: "precompile", address: common.BytesToAddress([]byte{1})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("developer genesis accepted a reserved faucet address")
				}
			}()
			DeveloperGenesisBlock(0, 8_000_000, test.address)
		})
	}
}
