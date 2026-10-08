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

// A signed owner may call proxy code, but only the top-level registry caller is
// authorized; nested call contexts cannot borrow tx.origin's authority.
func TestSessionKeysRegistryCannotAuthorizeThroughOriginOrContext(t *testing.T) {
	for _, opcode := range []byte{0xf1, 0xf2, 0xf4, 0xfa} {
		t.Run(common.Bytes2Hex([]byte{opcode}), func(t *testing.T) {
			f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(1000000))
			freshKey, _ := crypto.GenerateKey()
			fresh := crypto.PubkeyToAddress(freshKey.PublicKey)
			hash := sessionkeys.ProofHash(
				f.owner, fresh, f.target, [][4]byte{independentSessionSelector},
				big.NewInt(10), big.NewInt(100000), big.NewInt(100), f.config.ChainID,
			)
			proof, _ := crypto.Sign(hash[:], freshKey)
			input, err := sessionkeys.ABI.Pack(
				"CreateSessionKey", fresh, f.target, [][4]byte{independentSessionSelector},
				big.NewInt(10), big.NewInt(100000), big.NewInt(100), proof,
			)
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
			if err := sessionkeys.Create(
				f.db, f.owner, proxySigner, f.target, [][4]byte{selector},
				big.NewInt(100), big.NewInt(1000000), 100, 1,
			); err != nil {
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

func TestSessionKeysNativeStateValidation(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*state.StateDB)
		wantValid bool
	}{
		{
			name: "reserved registry permits balance dust",
			configure: func(db *state.StateDB) {
				db.SetNonce(sessionkeys.Address, 1)
				db.SetBalance(sessionkeys.Address, big.NewInt(7))
			},
			wantValid: true,
		},
		{
			name: "legacy registry without reserved nonce",
			configure: func(db *state.StateDB) {
				db.SetNonce(sessionkeys.Address, 0)
			},
		},
		{
			name: "registry code conflict",
			configure: func(db *state.StateDB) {
				db.SetNonce(sessionkeys.Address, 1)
				db.SetCode(sessionkeys.Address, []byte{0x00})
			},
		},
		{
			name: "registered state storage",
			configure: func(db *state.StateDB) {
				db.SetNonce(sessionkeys.Address, 1)
				db.SetState(sessionkeys.Address, common.Hash{}, common.BigToHash(big.NewInt(1)))
			},
			wantValid: true,
		},
		{
			name: "noncanonical GAplo runtime",
			configure: func(db *state.StateDB) {
				db.SetNonce(sessionkeys.Address, 1)
				db.SetCode(params.GAploContractAddress, []byte{0x00})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
			if err != nil {
				t.Fatal(err)
			}
			db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
			db.SetNonce(sessionkeys.Address, 1)
			test.configure(db)
			before := db.Copy().IntermediateRoot(false)
			err = sessionkeys.ValidateState(db)
			if (err == nil) != test.wantValid {
				t.Fatalf("ValidateState error=%v, want valid=%t", err, test.wantValid)
			}
			if after := db.Copy().IntermediateRoot(false); after != before {
				t.Fatalf("state validation mutated state: before=%s after=%s", before, after)
			}
		})
	}
}
