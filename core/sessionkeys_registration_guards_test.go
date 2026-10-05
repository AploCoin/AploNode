package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// A key possession proof does not let a contract account register sessions.
// The check belongs to the direct registry-call path, not just txpool policy.
func TestSessionKeysSignedApplyTransactionRejectsContractOwnerIndependent(t *testing.T) {
	f := newIndependentSessionFixture(
		t, nil, 100, big.NewInt(100), big.NewInt(1_000_000),
	)
	sessionKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := crypto.PubkeyToAddress(sessionKey.PublicKey)
	selectors := [][4]byte{independentSessionSelector}
	aplo, gaplo, expiry := big.NewInt(10), big.NewInt(20), big.NewInt(50)
	proof := independentSessionProof(
		t, f.owner, key, f.target, sessionKey, selectors,
		aplo, gaplo, expiry, f.config.ChainID,
	)
	input, err := sessionkeys.ABI.Pack(
		"CreateSessionKey", key, f.target, selectors,
		aplo, gaplo, expiry, proof,
	)
	if err != nil {
		t.Fatal(err)
	}

	// The transaction still has a valid owner signature, nonce, chain domain,
	// and session-key proof. The transaction executor rejects contract senders.
	f.db.SetCode(f.owner, []byte{0x00})
	before := f.db.Copy().IntermediateRoot(false)
	ownerAPLO := new(big.Int).Set(f.db.GetBalance(f.owner))
	ownerGAplo := sessionkeys.GaploBalance(f.db, f.owner)
	ownerNonce := f.db.GetNonce(f.owner)
	ownerSessionCount := f.db.GetState(
		sessionkeys.Address, sessionkeys.Slot("ownerCount", f.owner, 0),
	)
	tx := f.signedTx(
		t, f.ownerKey, f.db.GetNonce(f.owner), sessionkeys.Address,
		new(big.Int), 700_000, input,
	)
	if _, err := f.apply(t, tx); !errors.Is(err, ErrSenderNoEOA) {
		t.Fatalf("contract-code owner error=%v, want %v", err, ErrSenderNoEOA)
	}
	if after := f.db.Copy().IntermediateRoot(false); after != before {
		t.Fatalf("contract-owner rejection changed state root: before=%s after=%s", before, after)
	}
	if f.db.GetBalance(f.owner).Cmp(ownerAPLO) != 0 ||
		sessionkeys.GaploBalance(f.db, f.owner).Cmp(ownerGAplo) != 0 ||
		f.db.GetNonce(f.owner) != ownerNonce {
		t.Fatal("contract-owner rejection changed owner funds or nonce")
	}
	if sessionkeys.Get(f.db, key) != nil || sessionkeys.Used(f.db, key) ||
		sessionkeys.Owner(f.db, key) != (common.Address{}) {
		t.Fatal("contract-code owner registered or reserved the session key")
	}
	if got := f.db.GetState(
		sessionkeys.Address, sessionkeys.Slot("ownerCount", f.owner, 0),
	); got != ownerSessionCount {
		t.Fatalf("rejected registration changed owner's session index: before=%s after=%s", ownerSessionCount, got)
	}
}
