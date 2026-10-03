// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

//go:generate go run github.com/fjl/gencodec -type Genesis -field-override genesisSpecMarshaling -out gen_genesis.go
//go:generate go run github.com/fjl/gencodec -type GenesisAccount -field-override genesisAccountMarshaling -out gen_genesis_account.go

var errGenesisNoConfig = errors.New("genesis has no chain configuration")

// Genesis specifies the header fields, state of a genesis block. It also defines hard
// fork switch-over blocks through the chain configuration.
type Genesis struct {
	Config     *params.ChainConfig `json:"config"`
	Nonce      uint64              `json:"nonce"`
	Timestamp  uint64              `json:"timestamp"`
	ExtraData  []byte              `json:"extraData"`
	GasLimit   uint64              `json:"gasLimit"   gencodec:"required"`
	Difficulty *big.Int            `json:"difficulty" gencodec:"required"`
	Mixhash    common.Hash         `json:"mixHash"`
	Coinbase   common.Address      `json:"coinbase"`
	Alloc      GenesisAlloc        `json:"alloc"      gencodec:"required"`

	// These fields are used for consensus tests. Please don't use them
	// in actual genesis blocks.
	Number     uint64      `json:"number"`
	GasUsed    uint64      `json:"gasUsed"`
	ParentHash common.Hash `json:"parentHash"`
	BaseFee    *big.Int    `json:"baseFeePerGas"`
}

// GenesisAlloc specifies the initial state that is part of the genesis block.
type GenesisAlloc map[common.Address]GenesisAccount

// nativeProtocolAlloc returns an allocation with Aplo's permanent protocol
// accounts prepared. It does not mutate the supplied map or its storage maps.
func (ga GenesisAlloc) nativeProtocolAlloc() (GenesisAlloc, error) {
	alloc := make(GenesisAlloc, len(ga)+2)
	for addr, account := range ga {
		if account.Balance == nil {
			account.Balance = new(big.Int)
		}
		alloc[addr] = account
	}
	registry := alloc[sessionkeys.Address]
	if len(registry.Code) != 0 || len(registry.Storage) != 0 || registry.Nonce > 1 {
		return nil, errors.New("native Aplo genesis requires an empty reserved session registry")
	}
	registry.Nonce = 1
	if registry.Balance == nil {
		registry.Balance = new(big.Int)
	}
	alloc[sessionkeys.Address] = registry
	gaplo := alloc[params.GAploContractAddress]
	canonical := common.FromHex(params.GAPLO)
	if len(gaplo.Code) == 0 {
		if len(gaplo.Storage) != 0 {
			return nil, errors.New("native Aplo genesis GAplo storage requires its canonical runtime")
		}
		gaplo.Code = canonical
	} else if !bytes.Equal(gaplo.Code, canonical) {
		return nil, errors.New("native Aplo genesis requires canonical GAplo runtime")
	}
	if gaplo.Balance == nil {
		gaplo.Balance = new(big.Int)
	}
	alloc[params.GAploContractAddress] = gaplo
	return alloc, nil
}

func (ga *GenesisAlloc) UnmarshalJSON(data []byte) error {
	m := make(map[common.UnprefixedAddress]GenesisAccount)
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*ga = make(GenesisAlloc)
	for addr, a := range m {
		(*ga)[common.Address(addr)] = a
	}
	return nil
}

// deriveHash computes the state root according to the genesis specification.
func (ga *GenesisAlloc) deriveHash() (common.Hash, error) {
	alloc, err := ga.nativeProtocolAlloc()
	if err != nil {
		return common.Hash{}, err
	}
	// Create an ephemeral in-memory database for computing hash,
	// all the derived states will be discarded to not pollute disk.
	db := state.NewDatabase(rawdb.NewMemoryDatabase())
	statedb, err := state.New(common.Hash{}, db, nil)
	if err != nil {
		return common.Hash{}, err
	}
	for addr, account := range alloc {
		statedb.AddBalance(addr, account.Balance)
		statedb.SetCode(addr, account.Code)
		statedb.SetNonce(addr, account.Nonce)
		for key, value := range account.Storage {
			statedb.SetState(addr, key, value)
		}
	}
	return statedb.Commit(false)
}

// flush is very similar with deriveHash, but the main difference is
// all the generated states will be persisted into the given database.
// Also, the genesis state specification will be flushed as well.
func (ga *GenesisAlloc) flush(db ethdb.Database) error {
	alloc, err := ga.nativeProtocolAlloc()
	if err != nil {
		return err
	}
	statedb, err := state.New(common.Hash{}, state.NewDatabaseWithConfig(db, &trie.Config{Preimages: true}), nil)
	if err != nil {
		return err
	}
	for addr, account := range alloc {
		statedb.AddBalance(addr, account.Balance)
		statedb.SetCode(addr, account.Code)
		statedb.SetNonce(addr, account.Nonce)
		for key, value := range account.Storage {
			statedb.SetState(addr, key, value)
		}
	}
	root, err := statedb.Commit(false)
	if err != nil {
		return err
	}
	err = statedb.Database().TrieDB().Commit(root, true, nil)
	if err != nil {
		return err
	}
	// Marshal the genesis state specification and persist.
	blob, err := json.Marshal(alloc)
	if err != nil {
		return err
	}
	rawdb.WriteGenesisStateSpec(db, root, blob)
	return nil
}

// CommitGenesisState loads the stored genesis state with the given block
// hash and commits them into the given database handler.
func CommitGenesisState(db ethdb.Database, hash common.Hash) error {
	header := rawdb.ReadHeader(db, hash, 0)
	if header == nil {
		return errors.New("genesis header not found")
	}
	var alloc GenesisAlloc
	blob := rawdb.ReadGenesisStateSpec(db, header.Root)
	if len(blob) != 0 {
		if err := alloc.UnmarshalJSON(blob); err != nil {
			return err
		}
	} else {
		// Genesis allocation is missing and there are several possibilities:
		// the node is legacy which doesn't persist the genesis allocation or
		// the persisted allocation is just lost.
		// - supported networks(mainnet, testnets), recover with defined allocations
		// - private network, can't recover
		genesis := DefaultGenesisBlock()
		if hash != genesis.ToBlock().Hash() {
			return errors.New("not found")
		}
		var err error
		alloc, err = genesis.Alloc.nativeProtocolAlloc()
		if err != nil {
			return err
		}
	}
	// Recovery must never normalize an old specification into a different chain.
	registry := alloc[sessionkeys.Address]
	if registry.Nonce != 1 || len(registry.Code) != 0 || len(registry.Storage) != 0 ||
		!bytes.Equal(alloc[params.GAploContractAddress].Code, common.FromHex(params.GAPLO)) {
		return errors.New("incompatible native Aplo genesis specification; recreate the devnet in a fresh data directory")
	}
	root, err := alloc.deriveHash()
	if err != nil {
		return err
	}
	if root != header.Root {
		return errors.New("persisted genesis allocation does not match the genesis state root")
	}
	return alloc.flush(db)
}

// GenesisAccount is an account in the state of the genesis block.
type GenesisAccount struct {
	Code       []byte                      `json:"code,omitempty"`
	Storage    map[common.Hash]common.Hash `json:"storage,omitempty"`
	Balance    *big.Int                    `json:"balance" gencodec:"required"`
	Nonce      uint64                      `json:"nonce,omitempty"`
	PrivateKey []byte                      `json:"secretKey,omitempty"` // for tests
}

// field type overrides for gencodec
type genesisSpecMarshaling struct {
	Nonce      math.HexOrDecimal64
	Timestamp  math.HexOrDecimal64
	ExtraData  hexutil.Bytes
	GasLimit   math.HexOrDecimal64
	GasUsed    math.HexOrDecimal64
	Number     math.HexOrDecimal64
	Difficulty *math.HexOrDecimal256
	BaseFee    *math.HexOrDecimal256
	Alloc      map[common.UnprefixedAddress]GenesisAccount
}

type genesisAccountMarshaling struct {
	Code       hexutil.Bytes
	Balance    *math.HexOrDecimal256
	Nonce      math.HexOrDecimal64
	Storage    map[storageJSON]storageJSON
	PrivateKey hexutil.Bytes
}

// storageJSON represents a 256 bit byte array, but allows less than 256 bits when
// unmarshaling from hex.
type storageJSON common.Hash

func (h *storageJSON) UnmarshalText(text []byte) error {
	text = bytes.TrimPrefix(text, []byte("0x"))
	if len(text) > 64 {
		return fmt.Errorf("too many hex characters in storage key/value %q", text)
	}
	offset := len(h) - len(text)/2 // pad on the left
	if _, err := hex.Decode(h[offset:], text); err != nil {
		fmt.Println(err)
		return fmt.Errorf("invalid hex storage key/value %q", text)
	}
	return nil
}

func (h storageJSON) MarshalText() ([]byte, error) {
	return hexutil.Bytes(h[:]).MarshalText()
}

// GenesisMismatchError is raised when trying to overwrite an existing
// genesis block with an incompatible one.
type GenesisMismatchError struct {
	Stored, New common.Hash
}

func (e *GenesisMismatchError) Error() string {
	return fmt.Sprintf("database contains incompatible genesis (have %x, new %x)", e.Stored, e.New)
}

// SetupGenesisBlock writes or updates the genesis block in db.
// The block that will be used is:
//
//	                     genesis == nil       genesis != nil
//	                  +------------------------------------------
//	db has no genesis |  main-net default  |  genesis
//	db has genesis    |  from DB           |  genesis (if compatible)
//
// The stored chain configuration will be updated if it is compatible (i.e. does not
// specify a fork block below the local head block). In case of a conflict, the
// error is a *params.ConfigCompatError and the new, unwritten config is returned.
//
// The returned chain configuration is never nil.
func SetupGenesisBlock(db ethdb.Database, genesis *Genesis) (*params.ChainConfig, common.Hash, error) {
	return SetupGenesisBlockWithOverride(db, genesis, nil, nil)
}

func SetupGenesisBlockWithOverride(db ethdb.Database, genesis *Genesis, overrideTerminalTotalDifficulty *big.Int, overrideTerminalTotalDifficultyPassed *bool) (*params.ChainConfig, common.Hash, error) {
	if genesis != nil {
		if err := genesis.validateNativeProtocol(); err != nil {
			return genesis.Config, common.Hash{}, err
		}
	}
	if genesis != nil && genesis.Config == nil {
		return params.AllEthashProtocolChanges, common.Hash{}, errGenesisNoConfig
	}

	applyOverrides := func(config *params.ChainConfig) {
		if config != nil {
			if overrideTerminalTotalDifficulty != nil {
				config.TerminalTotalDifficulty = overrideTerminalTotalDifficulty
			}
			if overrideTerminalTotalDifficultyPassed != nil {
				config.TerminalTotalDifficultyPassed = *overrideTerminalTotalDifficultyPassed
			}
		}
	}

	// Just commit the new block if there is no stored genesis block.
	stored := rawdb.ReadCanonicalHash(db, 0)
	if (stored == common.Hash{}) {
		if genesis == nil {
			log.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			log.Info("Writing custom genesis block")
		}
		block, err := genesis.Commit(db)
		if err != nil {
			return genesis.Config, common.Hash{}, err
		}
		applyOverrides(genesis.Config)
		return genesis.Config, block.Hash(), nil
	}
	// We have the genesis block in database(perhaps in ancient database)
	// but the corresponding state is missing.
	header := rawdb.ReadHeader(db, stored, 0)
	genesisState, err := state.New(header.Root, state.NewDatabaseWithConfig(db, nil), nil)
	if err != nil {
		if genesis == nil {
			genesis = DefaultGenesisBlock()
		}
		// Ensure the stored genesis matches with the given one.
		hash := genesis.ToBlock().Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
		block, err := genesis.Commit(db)
		if err != nil {
			return genesis.Config, hash, err
		}
		applyOverrides(genesis.Config)
		return genesis.Config, block.Hash(), nil
	}
	// An old devnet is deliberately incompatible. Never migrate its state as a
	// side effect of startup; the operator must initialize a fresh data directory.
	if err := sessionkeys.ValidateState(genesisState); err != nil {
		return genesis.configOrDefault(stored), stored, fmt.Errorf("incompatible native Aplo genesis; recreate the devnet in a fresh data directory: %w", err)
	}
	var registryStorage bool
	if err := genesisState.ForEachStorage(sessionkeys.Address, func(_, value common.Hash) bool {
		registryStorage = value != (common.Hash{})
		return !registryStorage
	}); err != nil {
		return genesis.configOrDefault(stored), stored, err
	}
	if registryStorage {
		return genesis.configOrDefault(stored), stored, errors.New("native Aplo genesis session registry storage must be empty")
	}
	// Check whether the genesis block is already written.
	if genesis != nil {
		hash := genesis.ToBlock().Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
	}
	// Get the existing chain configuration.
	newcfg := genesis.configOrDefault(stored)
	applyOverrides(newcfg)
	if err := newcfg.CheckConfigForkOrder(); err != nil {
		return newcfg, common.Hash{}, err
	}
	storedcfg := rawdb.ReadChainConfig(db, stored)
	if storedcfg == nil {
		log.Warn("Found genesis block without chain config")
		rawdb.WriteChainConfig(db, stored, newcfg)
		return newcfg, stored, nil
	}
	// Special case: if a private network is being used (no genesis and also no
	// mainnet hash in the database), we must not apply the `configOrDefault`
	// chain config as that would be AllProtocolChanges (applying any new fork
	// on top of an existing private network genesis block). In that case, only
	// apply the overrides.
	if genesis == nil && stored != params.AploGenesisHash {
		newcfg = storedcfg
		applyOverrides(newcfg)
	}
	if err := (&Genesis{Config: newcfg}).validateNativeProtocol(); err != nil {
		return newcfg, stored, err
	}
	// Check config compatibility and write the config. Compatibility errors
	// are returned to the caller unless we're already at block zero.
	height := rawdb.ReadHeaderNumber(db, rawdb.ReadHeadHeaderHash(db))
	if height == nil {
		return newcfg, stored, fmt.Errorf("missing block number for head header hash")
	}
	compatErr := storedcfg.CheckCompatible(newcfg, *height)
	if compatErr != nil && *height != 0 {
		return newcfg, stored, compatErr
	}
	rawdb.WriteChainConfig(db, stored, newcfg)
	return newcfg, stored, nil
}

func (g *Genesis) configOrDefault(ghash common.Hash) *params.ChainConfig {
	switch {
	case g != nil:
		return g.Config
	case ghash == params.AploGenesisHash:
		return params.MainnetChainConfig
	default:
		return params.AllEthashProtocolChanges
	}
}

// ToBlock returns the genesis block according to genesis specification.
func (g *Genesis) ToBlock() *types.Block {
	if err := g.validateNativeProtocol(); err != nil {
		panic(err)
	}
	root, err := g.Alloc.deriveHash()
	if err != nil {
		panic(err)
	}
	head := &types.Header{
		Number:     new(big.Int).SetUint64(g.Number),
		Nonce:      types.EncodeNonce(g.Nonce),
		Time:       g.Timestamp,
		ParentHash: g.ParentHash,
		Extra:      g.ExtraData,
		GasLimit:   g.GasLimit,
		GasUsed:    g.GasUsed,
		BaseFee:    g.BaseFee,
		Difficulty: g.Difficulty,
		MixDigest:  g.Mixhash,
		Coinbase:   g.Coinbase,
		Root:       root,
	}
	if g.GasLimit == 0 {
		head.GasLimit = params.GenesisGasLimit
	}
	if g.Difficulty == nil && g.Mixhash == (common.Hash{}) {
		head.Difficulty = params.GenesisDifficulty
	}
	if g.Config != nil && g.Config.IsLondon(common.Big0) {
		if g.BaseFee != nil {
			head.BaseFee = g.BaseFee
		} else {
			head.BaseFee = new(big.Int).SetUint64(params.InitialBaseFee)
		}
	}
	return types.NewBlock(head, nil, nil, nil, trie.NewStackTrie(nil))
}

// Commit writes the block and state of a genesis specification to the database.
// The block is committed as the canonical head block.
func (g *Genesis) Commit(db ethdb.Database) (*types.Block, error) {
	if err := g.validateNativeProtocol(); err != nil {
		return nil, err
	}
	block := g.ToBlock()
	if block.Number().Sign() != 0 {
		return nil, errors.New("can't commit genesis block with number > 0")
	}
	config := g.Config
	if config == nil {
		config = params.AllEthashProtocolChanges
	}
	if err := config.CheckConfigForkOrder(); err != nil {
		return nil, err
	}
	if config.Clique != nil && len(block.Extra()) < 32+crypto.SignatureLength {
		return nil, errors.New("can't start clique chain without signers")
	}
	// All the checks has passed, flush the states derived from the genesis
	// specification as well as the specification itself into the provided
	// database.
	if err := g.Alloc.flush(db); err != nil {
		return nil, err
	}
	rawdb.WriteTd(db, block.Hash(), block.NumberU64(), block.Difficulty())
	rawdb.WriteBlock(db, block)
	rawdb.WriteReceipts(db, block.Hash(), block.NumberU64(), nil)
	rawdb.WriteCanonicalHash(db, block.Hash(), block.NumberU64())
	rawdb.WriteHeadBlockHash(db, block.Hash())
	rawdb.WriteHeadFastBlockHash(db, block.Hash())
	rawdb.WriteHeadHeaderHash(db, block.Hash())
	rawdb.WriteChainConfig(db, block.Hash(), config)
	return block, nil
}

// MustCommit writes the genesis block and state to db, panicking on error.
// The block is committed as the canonical head block.
func (g *Genesis) MustCommit(db ethdb.Database) *types.Block {
	block, err := g.Commit(db)
	if err != nil {
		panic(err)
	}
	return block
}

// DefaultGenesisBlock returns the Ethereum main net genesis block.
func DefaultGenesisBlock() *Genesis {
	gaploCode := common.FromHex(params.GAPLO)
	// builtinStubCode is a single STOP byte deployed at built-in contract addresses.
	// It must be non-empty so that Call's len(code)==0 short-circuit is not hit;
	// actual dispatch is handled by the builtin package, not the EVM interpreter.
	builtinStubCode := common.FromHex("6080604052")
	initialSupply := new(big.Int)
	initialSupply.SetString("1000000000000000000000000000000000000000000000000000000000000000000000000000", 10)

	bootstrapAddr := common.HexToAddress("0xCd086110Af6b5c2E81FE9860bd023D1BFC33aeEA")
	// Storage slot for _balances[bootstrapAddr]: keccak256(abi.encode(bootstrapAddr, uint256(0)))
	// _balances is at Solidity slot 0 in the GAPLO contract (OpenZeppelin ERC20 layout).
	bootstrapSlotInput := make([]byte, 64)
	copy(bootstrapSlotInput[12:32], bootstrapAddr.Bytes()) // address left-padded to 32 bytes
	// bytes [32:64] remain zero → mapping slot index 0
	bootstrapBalanceSlot := crypto.Keccak256Hash(bootstrapSlotInput)

	//balanceSlot := common.HexToHash("0x0b063ea9a1c5a5f474216f628fdf6a692512b36dff1289ce954d014cd1884625")
	ShortNameSlot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003")
	NameSlot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000004")
	totalMined := new(big.Int)
	totalMined.SetString("11", 10)

	return &Genesis{
		Config:     params.MainnetChainConfig,
		Timestamp:  1731516733,
		Nonce:      66,
		ExtraData:  []byte{}, //hexutil.MustDecode("0x11bbe8db4e347b4e8c937c1c8370e4b5ed33adb3db69cbdb7a38e1e50b1b82fa"),
		GasLimit:   30000000,
		Difficulty: big.NewInt(131072),
		Alloc: map[common.Address]GenesisAccount{
			// Pre-deploy GAplo contract
			params.GAploContractAddress: {
				Code: gaploCode,
				Storage: map[common.Hash]common.Hash{
					// Total supply storage slot
					common.HexToHash("0x2"): common.BigToHash(initialSupply),
					// Initial balance for genesis address storage slot
					// Calculate storage slot for _balances[initialHolder]
					bootstrapBalanceSlot: common.BigToHash(initialSupply),
					//totalMinedSlot: common.BigToHash(totalMined),
					ShortNameSlot: common.HexToHash("4741504c4f00000000000000000000000000000000000000000000000000000a"),
					NameSlot:      common.HexToHash("4761732041706c6f000000000000000000000000000000000000000000000010"),
				},
				Balance: big.NewInt(0),
			},
			params.AploContractAddress: {
				Code:    builtinStubCode,
				Storage: map[common.Hash]common.Hash{},
				Balance: big.NewInt(0),
			},
			params.BlockOracleContractAddress: {
				Code:    builtinStubCode,
				Storage: map[common.Hash]common.Hash{},
				Balance: big.NewInt(0),
			},
			//common.HexToAddress("0x67adcF8c25c88aF0Df3caB522C9dD5b11d017aca"): {
			//	Balance: balance,
			//},
		}}
}

// DefaultRopstenGenesisBlock returns the Ropsten network genesis block.
func DefaultRopstenGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.RopstenChainConfig,
		Nonce:      66,
		ExtraData:  hexutil.MustDecode("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   16777216,
		Difficulty: big.NewInt(1048576),
		Alloc:      decodePrealloc(ropstenAllocData),
	}
}

// DefaultRinkebyGenesisBlock returns the Rinkeby network genesis block.
func DefaultRinkebyGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.RinkebyChainConfig,
		Timestamp:  1492009146,
		ExtraData:  hexutil.MustDecode("0x52657370656374206d7920617574686f7269746168207e452e436172746d616e42eb768f2244c8811c63729a21a3569731535f067ffc57839b00206d1ad20c69a1981b489f772031b279182d99e65703f0076e4812653aab85fca0f00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"),
		GasLimit:   4700000,
		Difficulty: big.NewInt(1),
		Alloc:      decodePrealloc(rinkebyAllocData),
	}
}

// DefaultGoerliGenesisBlock returns the Görli network genesis block.
func DefaultGoerliGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.GoerliChainConfig,
		Timestamp:  1548854791,
		ExtraData:  hexutil.MustDecode("0x22466c6578692069732061207468696e6722202d204166726900000000000000e0a2bd4258d2768837baa26a28fe71dc079f84c70000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"),
		GasLimit:   10485760,
		Difficulty: big.NewInt(1),
		Alloc:      decodePrealloc(goerliAllocData),
	}
}

// DefaultSepoliaGenesisBlock returns the Sepolia network genesis block.
func DefaultSepoliaGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.SepoliaChainConfig,
		Nonce:      0,
		ExtraData:  []byte("Sepolia, Athens, Attica, Greece!"),
		GasLimit:   0x1c9c380,
		Difficulty: big.NewInt(0x20000),
		Timestamp:  1633267481,
		Alloc:      decodePrealloc(sepoliaAllocData),
	}
}

// DefaultKilnGenesisBlock returns the kiln network genesis block.
func DefaultKilnGenesisBlock() *Genesis {
	g := new(Genesis)
	reader := strings.NewReader(KilnAllocData)
	if err := json.NewDecoder(reader).Decode(g); err != nil {
		panic(err)
	}
	return g
}

// DeveloperGenesisBlock returns the 'geth --dev' genesis block.
func DeveloperGenesisBlock(period uint64, gasLimit uint64, faucet common.Address) *Genesis {
	faucetNumber := new(big.Int).SetBytes(faucet.Bytes())
	if faucet == (common.Address{}) || faucet == sessionkeys.Address ||
		faucet == params.GAploContractAddress || faucet == params.AploContractAddress ||
		faucet == params.BlockOracleContractAddress ||
		(faucetNumber.IsUint64() && faucetNumber.Uint64() <= 9) {
		panic("developer faucet must be an ordinary EOA address")
	}
	// Override the default period to the user requested one
	config := *params.AllCliqueProtocolChanges
	config.Clique = &params.CliqueConfig{
		Period: period,
		Epoch:  config.Clique.Epoch,
	}

	// Fund native APLO and GAplo separately so the first protected transaction can
	// pay fees without a setup transaction or lazy protocol-state initialization.
	supply := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))
	// Assemble and return the genesis with the precompiles and faucet pre-funded.
	return &Genesis{
		Config:     &config,
		ExtraData:  append(append(make([]byte, 32), faucet[:]...), make([]byte, crypto.SignatureLength)...),
		GasLimit:   gasLimit,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(1),
		Alloc: map[common.Address]GenesisAccount{
			common.BytesToAddress([]byte{1}): {Balance: big.NewInt(1)}, // ECRecover
			common.BytesToAddress([]byte{2}): {Balance: big.NewInt(1)}, // SHA256
			common.BytesToAddress([]byte{3}): {Balance: big.NewInt(1)}, // RIPEMD
			common.BytesToAddress([]byte{4}): {Balance: big.NewInt(1)}, // Identity
			common.BytesToAddress([]byte{5}): {Balance: big.NewInt(1)}, // ModExp
			common.BytesToAddress([]byte{6}): {Balance: big.NewInt(1)}, // ECAdd
			common.BytesToAddress([]byte{7}): {Balance: big.NewInt(1)}, // ECScalarMul
			common.BytesToAddress([]byte{8}): {Balance: big.NewInt(1)}, // ECPairing
			common.BytesToAddress([]byte{9}): {Balance: big.NewInt(1)}, // BLAKE2b
			faucet:                           {Balance: new(big.Int).Set(supply)},
			params.GAploContractAddress: {
				Code:    common.FromHex(params.GAPLO),
				Balance: new(big.Int),
				Storage: map[common.Hash]common.Hash{
					sessionkeys.GaploSlot(faucet):   common.BigToHash(supply),
					common.BigToHash(big.NewInt(2)): common.BigToHash(supply),
				},
			},
			sessionkeys.Address: {Nonce: 1, Balance: new(big.Int)},
		},
	}
}

func decodePrealloc(data string) GenesisAlloc {
	var p []struct{ Addr, Balance *big.Int }
	if err := rlp.NewStream(strings.NewReader(data), 0).Decode(&p); err != nil {
		panic(err)
	}
	ga := make(GenesisAlloc, len(p))
	for _, account := range p {
		ga[common.BigToAddress(account.Addr)] = GenesisAccount{Balance: account.Balance}
	}
	return ga
}

// validateNativeProtocol rejects conflicting genesis predeploys without changing
// caller-owned allocations. Both root derivation and persistence normalize them.
func (g *Genesis) validateNativeProtocol() error {
	config := g.Config
	if config == nil {
		config = params.AllEthashProtocolChanges
	}
	if config.ChainID == nil || config.ChainID.Sign() < 0 || config.ChainID.BitLen() > 256 ||
		(config.EthPoWForkBlock != nil && (config.ChainID_ALT == nil || config.ChainID_ALT.Sign() < 0 || config.ChainID_ALT.BitLen() > 256)) {
		return errors.New("native Aplo genesis requires uint256 chain ID signing domains")
	}
	if config.EIP155Block == nil || config.EIP155Block.Sign() != 0 {
		return errors.New("native Aplo genesis requires EIP155 at block zero")
	}
	_, err := g.Alloc.nativeProtocolAlloc()
	return err
}
