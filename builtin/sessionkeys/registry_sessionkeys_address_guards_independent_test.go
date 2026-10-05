package sessionkeys_test

import (
	"math/big"
	"testing"

	registry "github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Public Run rejects the reserved registry as owner or target, while allowing
// an ordinary contract target. This distinguishes target policy from the
// owner/key account restrictions.
func TestRegistrySessionKeysPublicRunReservedOwnerAndTargetIndependent(t *testing.T) {
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keySigner, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	key := crypto.PubkeyToAddress(keySigner.PublicKey)
	selectors := [][4]byte{{0x11, 0x22, 0x33, 0x44}}
	aplo, gaplo, expiry := big.NewInt(10), big.NewInt(20), big.NewInt(100)
	chainID := big.NewInt(1)
	tests := []struct {
		name          string
		owner         common.Address
		target        common.Address
		contractOwner bool
		wantSuccess   bool
	}{
		{name: "registry cannot be owner", owner: registry.Address, target: independentAddress(0x9000)},
		{name: "registry cannot be target", owner: owner, target: registry.Address},
		{name: "contract cannot be owner", owner: owner, target: independentAddress(0x9000), contractOwner: true},
		{name: "contract target is allowed", owner: owner, target: independentAddress(0x9000), wantSuccess: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := independentRegistryState(t)
			if test.contractOwner {
				db.SetCode(test.owner, []byte{0x00})
			}
			if test.wantSuccess {
				db.SetCode(test.target, []byte{0x00})
			}
			input := independentPackCreate(
				t, test.owner, keySigner, key, test.target, selectors,
				aplo, gaplo, expiry, chainID,
			)
			before := db.Copy().IntermediateRoot(false)
			_, _, err := registry.Run(
				db, test.owner, input,
				registry.CreateGas+registry.SelectorGas, 1, false, chainID,
			)
			if test.wantSuccess {
				if err != nil {
					t.Fatalf("valid contract target rejected: %v", err)
				}
				if session := registry.Get(db, key); session == nil || session.Target != test.target {
					t.Fatalf("contract target was not registered: %#v", session)
				}
				if db.GetCodeSize(test.target) == 0 {
					t.Fatal("positive target fixture is not a contract")
				}
				return
			}
			if err == nil {
				t.Fatal("registry reserved address was accepted in a forbidden role")
			}
			if after := db.Copy().IntermediateRoot(false); after != before {
				t.Fatalf("rejected registration mutated state: before=%s after=%s", before, after)
			}
			if registry.Get(db, key) != nil || registry.Used(db, key) {
				t.Fatal("rejected registration created or reserved the session key")
			}
		})
	}
}

// A reserved key cannot practically provide the required proof of possession,
// so exercise its defensive invariant directly in Create.
func TestRegistrySessionKeysTrustedCreateRejectsRegistryAsKeyIndependent(t *testing.T) {
	db := independentRegistryState(t)
	owner := independentAddress(0xa0)
	selector := [4]byte{0x11, 0x22, 0x33, 0x44}
	before := db.Copy().IntermediateRoot(false)
	if err := registry.Create(
		db, owner, registry.Address, independentAddress(0x9000), [][4]byte{selector},
		big.NewInt(10), big.NewInt(20), 100, 1,
	); err == nil {
		t.Fatal("registry address accepted as a session key")
	}
	if after := db.Copy().IntermediateRoot(false); after != before {
		t.Fatalf("rejected reserved-key creation mutated state: before=%s after=%s", before, after)
	}
	if registry.Get(db, registry.Address) != nil || registry.Used(db, registry.Address) {
		t.Fatal("reserved registry key was registered or marked used")
	}
}
