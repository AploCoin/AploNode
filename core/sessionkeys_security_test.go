package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func TestSessionKeysRegistryCannotAuthorizeThroughOriginOrContext(t *testing.T) {
	for _, opcode := range []byte{0xf1, 0xf2, 0xf4, 0xfa} {
		t.Run(common.Bytes2Hex([]byte{opcode}), func(t *testing.T) {
			f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(1000000))
			freshKey, _ := crypto.GenerateKey()
			fresh := crypto.PubkeyToAddress(freshKey.PublicKey)
			hash := sessionkeys.ProofHash(f.owner, fresh, f.target, [][4]byte{independentSessionSelector}, big.NewInt(10), big.NewInt(100000), big.NewInt(100), f.config.ChainID)
			proof, _ := crypto.Sign(hash[:], freshKey)
			input, err := sessionkeys.ABI.Pack("CreateSessionKey", fresh, f.target, [][4]byte{independentSessionSelector}, big.NewInt(10), big.NewInt(100000), big.NewInt(100), proof)
			if err != nil {
				t.Fatal(err)
			}
			// Forward transaction calldata to registry using CALL/CALLCODE/DELEGATECALL/STATICCALL.
			code := []byte{0x36, 0x60, 0, 0x60, 0, 0x37, 0x60, 0, 0x60, 0, 0x36, 0x60, 0}
			if opcode == 0xf1 || opcode == 0xf2 {
				code = append(code, 0x60, 0)
			}
			code = append(code, 0x73)
			code = append(code, sessionkeys.Address.Bytes()...)
			code = append(code, 0x5a, opcode, 0x60, 0, 0x55, 0)
			f.db.SetCode(f.target, code)
			// Replace the previous session with a fresh key whose whitelist deliberately
			// admits the registry selector on the proxy; policy must still reject nested auth.
			proxyKey, _ := crypto.GenerateKey()
			proxySigner := crypto.PubkeyToAddress(proxyKey.PublicKey)
			var selector [4]byte
			copy(selector[:], input[:4])
			if err := sessionkeys.Create(f.db, f.owner, proxySigner, f.target, [][4]byte{selector}, big.NewInt(100), big.NewInt(1000000), 100, 1); err != nil {
				t.Fatal(err)
			}
			tx := f.signedTx(t, proxyKey, 0, f.target, new(big.Int), 250000, input)
			receipt, err := f.apply(t, tx)
			if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
				t.Fatalf("proxy tx %v %v", receipt, err)
			}
			if sessionkeys.Used(f.db, fresh) || f.db.GetState(f.target, common.Hash{}) != (common.Hash{}) {
				t.Fatal("indirect builtin gained owner authorization from origin")
			}
		})
	}
}
func TestSessionKeysActivationDustAndGenesisConflicts(t *testing.T) {
	config := *params.TestChainConfig
	config.SessionKeysBlock = big.NewInt(2)
	for _, existing := range []struct{ balance, nonce int64 }{{0, 0}, {1, 0}, {1, 7}} {
		db, _ := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
		db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
		if existing.balance > 0 {
			db.SetBalance(sessionkeys.Address, big.NewInt(existing.balance))
		}
		if existing.nonce > 0 {
			db.SetNonce(sessionkeys.Address, uint64(existing.nonce))
		}
		if err := sessionkeys.CheckForkBoundary(db, &config, big.NewInt(2)); err != nil {
			t.Fatal(err)
		}
		if err := sessionkeys.Activate(db, &config, big.NewInt(2)); err != nil {
			t.Fatal(err)
		}
		if db.GetBalance(sessionkeys.Address).Cmp(big.NewInt(existing.balance)) != 0 {
			t.Fatal("activation lost native dust")
		}
		if existing.balance > 0 && db.GetNonce(sessionkeys.Address) != uint64(existing.nonce) {
			t.Fatal("activation reset existing EOA nonce")
		}
		if db.Empty(sessionkeys.Address) {
			t.Fatal("registry vulnerable to EIP161 deletion")
		}
	}
	config.SessionKeysBlock = big.NewInt(0)
	for _, alloc := range []GenesisAccount{{Code: []byte{0}}, {Storage: map[common.Hash]common.Hash{sessionkeys.Slot("initialized", sessionkeys.Address, 0): common.BigToHash(big.NewInt(1))}}} {
		g := &Genesis{Config: &config, Alloc: GenesisAlloc{params.GAploContractAddress: {Code: common.FromHex(params.GAPLO)}, sessionkeys.Address: alloc}}
		if _, err := g.Commit(rawdb.NewMemoryDatabase()); err == nil {
			t.Fatal("genesis imported conflicting registry")
		}
	}
}
