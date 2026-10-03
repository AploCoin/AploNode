package sessionkeys_test

import (
	"crypto/ecdsa"
	"math/big"
	"math/rand"
	"testing"

	registry "github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// independentRegistryState creates a fresh trie with the canonical GAplo runtime.
func independentRegistryState(t *testing.T) *state.StateDB {
	t.Helper()
	db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetNonce(registry.Address, 1)
	return db
}

func independentAddress(n uint64) common.Address {
	return common.BigToAddress(new(big.Int).SetUint64(n))
}

// independentSessionProof signs a registry creation payload with the key being registered.
func independentSessionProof(
	t *testing.T,
	signer *ecdsa.PrivateKey,
	owner, key, target common.Address,
	selectors [][4]byte,
	aplo, gaplo, expiry, chainID *big.Int,
) []byte {
	t.Helper()
	if crypto.PubkeyToAddress(signer.PublicKey) != key {
		t.Fatalf(
			"proof signer address %s does not match claimed session key %s",
			crypto.PubkeyToAddress(signer.PublicKey), key,
		)
	}
	hash := registry.ProofHash(owner, key, target, selectors, aplo, gaplo, expiry, chainID)
	proof, err := crypto.Sign(hash[:], signer)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

// independentPackCreate builds the canonical ABI call, including its proof of possession.
func independentPackCreate(
	t *testing.T,
	owner common.Address,
	signer *ecdsa.PrivateKey,
	key, target common.Address,
	selectors [][4]byte,
	aplo, gaplo, expiry, chainID *big.Int,
) []byte {
	t.Helper()
	proof := independentSessionProof(t, signer, owner, key, target, selectors, aplo, gaplo, expiry, chainID)
	input, err := registry.ABI.Pack(
		"CreateSessionKey", key, target, selectors, aplo, gaplo, expiry, proof,
	)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

// independentCreate seeds trusted registry state without exercising the public ABI.
func independentCreate(
	db *state.StateDB,
	owner, key common.Address,
	block, expiry uint64,
	selectors ...[4]byte,
) error {
	return registry.Create(db, owner, key, independentAddress(0x9000), selectors,
		big.NewInt(50), big.NewInt(70), expiry, block)
}

func independentBucket(expiry uint64) common.Address {
	return common.BigToAddress(new(big.Int).SetUint64(expiry))
}

func TestRegistrySessionKeysIndependentAccountingAndRevocation(t *testing.T) {
	db := independentRegistryState(t)
	owner, key, target := independentAddress(0xa0), independentAddress(0xb0), independentAddress(0x9000)
	selector := [4]byte{0x12, 0x34, 0x56, 0x78}
	db.SetBalance(owner, big.NewInt(1000))
	db.SetNonce(owner, 91)

	if err := registry.Create(
		db, owner, key, target, [][4]byte{selector},
		big.NewInt(50), big.NewInt(70), 1100, 100,
	); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := registry.Get(db, key)
	if s == nil || s.Owner != owner || s.Target != target || s.Nonce != 0 || s.Expiry != 1100 {
		t.Fatalf("unexpected session record: %#v", s)
	}
	if s.AploSpent.Cmp(big.NewInt(50)) != 0 || s.GAploSpent.Cmp(big.NewInt(70)) != 0 {
		t.Fatalf("unexpected initial allowances: APLO=%v GAplo=%v", s.AploSpent, s.GAploSpent)
	}
	if len(s.Selectors) != 1 || s.Selectors[0] != selector {
		t.Fatalf("unexpected selector list: %x", s.Selectors)
	}

	data := append(selector[:], make([]byte, 32)...)
	if err := registry.Validate(
		s, s.Expiry, &target, data, big.NewInt(12), big.NewInt(18),
	); err != nil {
		t.Fatalf("session should be valid through its expiry block: %v", err)
	}
	if err := registry.Validate(
		s, s.Expiry+1, &target, data, big.NewInt(12), big.NewInt(18),
	); err == nil {
		t.Fatal("session remained valid after its expiry block")
	}
	if err := registry.Validate(
		s, 1100, &independentAddressValue, data, big.NewInt(12), big.NewInt(18),
	); err == nil {
		t.Fatal("session accepted a different target")
	}
	if err := registry.Validate(
		s, 1100, &target, selector[:3], big.NewInt(12), big.NewInt(18),
	); err == nil {
		t.Fatal("session accepted short selector input")
	}
	if err := registry.Validate(
		s, 1100, &target, data, big.NewInt(51), big.NewInt(18),
	); err == nil {
		t.Fatal("session exceeded its APLO allowance")
	}
	if err := registry.Validate(
		s, 1100, &target, data, big.NewInt(12), big.NewInt(71),
	); err == nil {
		t.Fatal("session exceeded its GAplo allowance")
	}
	wrongSelector := append([]byte{0x87, 0x65, 0x43, 0x21}, make([]byte, 32)...)
	if err := registry.Validate(
		s, 1100, &target, wrongSelector, big.NewInt(12), big.NewInt(18),
	); err == nil {
		t.Fatal("session accepted an unlisted selector")
	}

	registry.Charge(db, key, big.NewInt(12), big.NewInt(18))
	db.SetNonce(key, 1)
	s = registry.Get(db, key)
	if s.Nonce != 1 || s.AploSpent.Cmp(big.NewInt(38)) != 0 || s.GAploSpent.Cmp(big.NewInt(52)) != 0 {
		t.Fatalf("session accounting did not advance independently: %#v", s)
	}
	if db.GetNonce(owner) != 91 {
		t.Fatalf("session transaction changed owner's nonce: %d", db.GetNonce(owner))
	}

	if err := registry.Revoke(db, owner, key); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if registry.Get(db, key) != nil || registry.Owner(db, key) != (common.Address{}) {
		t.Fatal("revoked session still has active configuration")
	}
	if !registry.Used(db, key) {
		t.Fatal("revocation cleared the permanent used-key marker")
	}
	if err := independentCreate(db, owner, key, 100, 200, selector); err == nil {
		t.Fatal("revoked key was reused")
	}
	if err := registry.Revoke(db, independentAddress(0xa1), key); err == nil {
		t.Fatal("non-owner revoke succeeded")
	}
}

var independentAddressValue = independentAddress(0x9001)

func TestRegistrySessionKeysIndependentExpiryBoundsAndOverflow(t *testing.T) {
	db := independentRegistryState(t)
	owner, key := independentAddress(0xa0), independentAddress(0xb0)
	selector := [4]byte{0xaa, 0xbb, 0xcc, 0xdd}
	if err := independentCreate(db, owner, key, 100, 1100, selector); err != nil {
		t.Fatalf("maximum lifetime should be accepted: %v", err)
	}
	data := append(selector[:], make([]byte, 32)...)
	s := registry.Get(db, key)
	if err := registry.Validate(s, 1100, &s.Target, data, big.NewInt(0), big.NewInt(0)); err != nil {
		t.Fatalf("session must remain valid for block 1100: %v", err)
	}
	registry.Cleanup(db, 1100)
	if registry.Get(db, key) != nil ||
		db.GetState(registry.Address, registry.Slot("ownerCount", owner, 0)).Big().Sign() != 0 ||
		db.GetState(registry.Address, registry.Slot("bucketCount", independentBucket(1100), 0)).Big().Sign() != 0 {
		t.Fatal("expiry cleanup left a live session or stale index entry")
	}

	max := ^uint64(0)
	maxOwner, maxKey := independentAddress(0xa1), independentAddress(0xb1)
	if err := independentCreate(
		db, maxOwner, maxKey, max-registry.MaxLifetime, max, selector,
	); err != nil {
		t.Fatalf("non-wrapping maximum block expiry should be accepted: %v", err)
	}
	if err := independentCreate(
		db, independentAddress(0xa2), independentAddress(0xb2),
		max-registry.MaxLifetime+1, max, selector,
	); err == nil {
		t.Fatal("expiry arithmetic wrapped near MaxUint64")
	}

	invalid := []struct {
		name    string
		block   uint64
		expiry  uint64
		choices [][4]byte
	}{
		{name: "expiry before current block", block: 10, expiry: 9, choices: [][4]byte{selector}},
		{name: "expiry beyond horizon", block: 10, expiry: 1011, choices: [][4]byte{selector}},
		{name: "no selectors", block: 10, expiry: 11},
		{name: "duplicate selectors", block: 10, expiry: 11, choices: [][4]byte{selector, selector}},
	}
	for i, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			fresh := independentRegistryState(t)
			if err := registry.Create(
				fresh, independentAddress(0xc0), independentAddress(uint64(0xd0+i)),
				independentAddress(0x9000), test.choices,
				big.NewInt(1), big.NewInt(1), test.expiry, test.block,
			); err == nil {
				t.Fatal("invalid creation succeeded")
			}
		})
	}
}

func TestRegistrySessionKeysIndependentOwnerAndExpiryBucketCaps(t *testing.T) {
	db := independentRegistryState(t)
	selector := [4]byte{1, 2, 3, 4}
	keyID := uint64(0x10000)
	createFor := func(owner common.Address, expiry uint64) error {
		keyID++
		return independentCreate(db, owner, independentAddress(keyID), 10, expiry, selector)
	}
	first, second, third := independentAddress(0xa0), independentAddress(0xa1), independentAddress(0xa2)
	for i := uint64(0); i < registry.MaxOwnerSessions; i++ {
		expiry := 20 + i/registry.MaxBucketEntries
		if err := createFor(first, expiry); err != nil {
			t.Fatalf("first owner's session %d: %v", i, err)
		}
	}
	if err := createFor(first, 22); err == nil {
		t.Fatal("owner session cap was not enforced")
	}
	if got := db.GetState(
		registry.Address, registry.Slot("ownerCount", first, 0),
	).Big().Uint64(); got != registry.MaxOwnerSessions {
		t.Fatalf("owner session count=%d, want %d", got, registry.MaxOwnerSessions)
	}

	bucketDB := independentRegistryState(t)
	for i := uint64(0); i < registry.MaxBucketEntries; i++ {
		keyID++
		if err := independentCreate(bucketDB, second, independentAddress(keyID), 10, 20, selector); err != nil {
			t.Fatalf("expiry bucket entry %d: %v", i, err)
		}
	}
	if err := independentCreate(
		bucketDB, third, independentAddress(keyID+1), 10, 20, selector,
	); err == nil {
		t.Fatal("expiry bucket cap was not enforced")
	}
	if got := bucketDB.GetState(
		registry.Address, registry.Slot("bucketCount", independentBucket(20), 0),
	).Big().Uint64(); got != registry.MaxBucketEntries {
		t.Fatalf("expiry bucket count=%d, want %d", got, registry.MaxBucketEntries)
	}
}

func TestRegistrySessionKeysIndependentRandomizedIndexLifecycle(t *testing.T) {
	db := independentRegistryState(t)
	rng := rand.New(rand.NewSource(20261003))
	owners := []common.Address{independentAddress(0xa0), independentAddress(0xa1), independentAddress(0xa2)}
	type entry struct {
		owner  common.Address
		key    common.Address
		expiry uint64
		active bool
	}
	entries := make([]entry, 0, 60)
	for i := 0; i < 60; i++ {
		e := entry{
			owner:  owners[rng.Intn(len(owners))],
			key:    independentAddress(uint64(0x2000 + i)),
			expiry: uint64(20 + rng.Intn(5)),
			active: true,
		}
		selector := [4]byte{0x70, byte(i >> 16), byte(i >> 8), byte(i)}
		if err := independentCreate(db, e.owner, e.key, 10, e.expiry, selector); err != nil {
			t.Fatalf("create randomized registry entry %d: %v", i, err)
		}
		entries = append(entries, e)
	}
	assertIndexes := func(label string) {
		t.Helper()
		ownerCounts, bucketCounts := make(map[common.Address]uint64), make(map[common.Address]uint64)
		for _, e := range entries {
			if !e.active {
				if registry.Get(db, e.key) != nil || !registry.Used(db, e.key) {
					t.Fatalf("%s: removed key %s is active or reusable", label, e.key)
				}
				continue
			}
			ownerCounts[e.owner]++
			bucketAddr := independentBucket(e.expiry)
			bucketCounts[bucketAddr]++
			if registry.Get(db, e.key) == nil {
				t.Fatalf("%s: active key %s has no configuration", label, e.key)
			}
			ownerIndex := db.GetState(registry.Address, registry.Slot("ownerIndex", e.key, 0)).Big().Uint64()
			if got := common.BytesToAddress(
				db.GetState(registry.Address, registry.Slot("ownerList", e.owner, ownerIndex)).Bytes(),
			); got != e.key {
				t.Fatalf("%s: owner index for %s points to %s", label, e.key, got)
			}
			bucketIndex := db.GetState(registry.Address, registry.Slot("bucketIndex", e.key, 0)).Big().Uint64()
			if got := common.BytesToAddress(
				db.GetState(registry.Address, registry.Slot("bucketList", bucketAddr, bucketIndex)).Bytes(),
			); got != e.key {
				t.Fatalf("%s: bucket index for %s points to %s", label, e.key, got)
			}
		}
		for _, owner := range owners {
			got := db.GetState(registry.Address, registry.Slot("ownerCount", owner, 0)).Big().Uint64()
			if got != ownerCounts[owner] {
				t.Fatalf("%s: owner %s count=%d, want %d", label, owner, got, ownerCounts[owner])
			}
		}
		for expiry := uint64(20); expiry <= 24; expiry++ {
			bucketAddr := independentBucket(expiry)
			got := db.GetState(registry.Address, registry.Slot("bucketCount", bucketAddr, 0)).Big().Uint64()
			if got != bucketCounts[bucketAddr] {
				t.Fatalf("%s: bucket %d count=%d, want %d", label, expiry, got, bucketCounts[bucketAddr])
			}
		}
	}
	assertIndexes("created")
	order := rng.Perm(len(entries))
	for _, idx := range order {
		if rng.Intn(2) == 0 {
			if err := registry.Revoke(db, entries[idx].owner, entries[idx].key); err != nil {
				t.Fatalf("revoke randomized entry %d: %v", idx, err)
			}
			entries[idx].active = false
		}
		assertIndexes("random revoke sequence")
	}
	for expiry := uint64(20); expiry <= 24; expiry++ {
		registry.Cleanup(db, expiry)
		for i := range entries {
			if entries[i].active && entries[i].expiry == expiry {
				entries[i].active = false
			}
		}
		assertIndexes("random expiry cleanup")
	}
}

func TestRegistrySessionKeysIndependentBuiltinABIValidation(t *testing.T) {
	db := independentRegistryState(t)
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner, key, target :=
		crypto.PubkeyToAddress(ownerKey.PublicKey),
		crypto.PubkeyToAddress(sessionKey.PublicKey),
		independentAddress(0x9000)
	selector := [4]byte{0x11, 0x22, 0x33, 0x44}
	chainID := big.NewInt(1)
	input := independentPackCreate(
		t, owner, sessionKey, key, target, [][4]byte{selector},
		big.NewInt(20), big.NewInt(30), big.NewInt(110), chainID,
	)
	_, remaining, err := registry.Run(
		db, owner, input, registry.CreateGas+registry.SelectorGas, 100, false, chainID,
	)
	if err != nil || remaining != 0 {
		t.Fatalf("owner creation via builtin: remaining=%d err=%v", remaining, err)
	}
	if got := registry.Get(db, key); got == nil || got.Owner != owner {
		t.Fatalf("builtin did not create the owner session: %#v", got)
	}

	revoke, err := registry.ABI.Pack("RevokeSessionKey", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Run(
		db, independentAddress(0xa1), revoke, registry.RevokeGas, 100, false, chainID,
	); err == nil {
		t.Fatal("builtin allowed a non-owner to revoke a session")
	}
	if _, _, err := registry.Run(db, owner, revoke, registry.RevokeGas, 100, true, chainID); err == nil {
		t.Fatal("read-only builtin call changed session state")
	}
	if registry.Get(db, key) == nil {
		t.Fatal("failed builtin calls changed session state")
	}
	if _, _, err := registry.Run(
		db, owner, input[:len(input)-1], registry.CreateGas+registry.SelectorGas,
		100, false, chainID,
	); err == nil {
		t.Fatal("builtin accepted non-canonical truncated ABI input")
	}
	if _, _, err := registry.Run(
		db, owner, input, registry.CreateGas+registry.SelectorGas-1, 100, false, chainID,
	); err == nil {
		t.Fatal("builtin accepted insufficient gas")
	}
	if registry.Get(db, independentAddress(0xb1)) != nil {
		t.Fatal("invalid builtin calls wrote a session")
	}

}

func TestRegistrySessionKeysRejectsInvalidSigningDomainsIndependent(t *testing.T) {
	db := independentRegistryState(t)
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	key := crypto.PubkeyToAddress(sessionKey.PublicKey)
	target := independentAddress(0x9000)
	selectors := [][4]byte{{1, 2, 3, 4}}
	aplo, gaplo, expiry := big.NewInt(20), big.NewInt(30), big.NewInt(110)
	chainID := big.NewInt(1)
	proof := independentSessionProof(
		t, sessionKey, owner, key, target, selectors,
		aplo, gaplo, expiry, chainID,
	)
	input, err := registry.ABI.Pack(
		"CreateSessionKey", key, target, selectors, aplo, gaplo, expiry, proof,
	)
	if err != nil {
		t.Fatal(err)
	}
	domains := []struct {
		name string
		id   *big.Int
	}{
		{name: "nil"},
		{name: "negative", id: big.NewInt(-1)},
		{name: "larger than uint256", id: new(big.Int).Lsh(big.NewInt(1), 256)},
	}
	for _, domain := range domains {
		t.Run(domain.name, func(t *testing.T) {
			before := db.Copy().IntermediateRoot(false)
			if _, _, err := registry.Run(
				db, owner, input, registry.CreateGas+registry.SelectorGas,
				100, false, domain.id,
			); err == nil {
				t.Fatal("registry accepted an invalid signing domain")
			}
			if got := db.Copy().IntermediateRoot(false); got != before {
				t.Fatalf("invalid signing domain mutated state: before=%s after=%s", before, got)
			}
			if registry.Used(db, key) || registry.Get(db, key) != nil {
				t.Fatal("invalid signing domain reserved or registered the key")
			}
			if registry.VerifyProof(
				owner, key, target, selectors, aplo, gaplo, expiry, domain.id, proof,
			) {
				t.Fatal("proof verification accepted an invalid signing domain")
			}
		})
	}
}

func TestRegistrySessionKeysIndependentProofOfPossessionPreventsAddressSquatting(t *testing.T) {
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	victimKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	forgedKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	victim := crypto.PubkeyToAddress(victimKey.PublicKey)
	target := independentAddress(0x9000)
	selector := [4]byte{0x41, 0x42, 0x43, 0x44}
	selectors := [][4]byte{selector}
	chainID := big.NewInt(1)
	aplo, gaplo, expiry := big.NewInt(50), big.NewInt(70), big.NewInt(110)

	baseProof := independentSessionProof(
		t, victimKey, owner, victim, target, selectors, aplo, gaplo, expiry, chainID,
	)
	// Convert the valid low-s signature to its malleable high-s counterpart.
	highS := append([]byte(nil), baseProof...)
	order := crypto.S256().Params().N
	highSValue := new(big.Int).Sub(order, new(big.Int).SetBytes(highS[32:64]))
	copy(highS[32:64], common.LeftPadBytes(highSValue.Bytes(), 32))
	highS[64] ^= 1

	wrongSelector := [][4]byte{{0x51, 0x52, 0x53, 0x54}}
	forgedHash := registry.ProofHash(owner, victim, target, selectors, aplo, gaplo, expiry, chainID)
	forgedProof, err := crypto.Sign(forgedHash[:], forgedKey)
	if err != nil {
		t.Fatal(err)
	}
	wrongOwner := independentAddress(0xa1)
	wrongChainID := big.NewInt(2)
	tests := []struct {
		name      string
		caller    common.Address
		key       common.Address
		target    common.Address
		selectors [][4]byte
		aplo      *big.Int
		gaplo     *big.Int
		expiry    *big.Int
		chainID   *big.Int
		proof     []byte
	}{
		{
			name: "attacker cannot claim known victim key", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: chainID, proof: forgedProof,
		},
		{
			name: "proof is bound to owner", caller: wrongOwner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: chainID, proof: baseProof,
		},
		{
			name: "proof is bound to chain id", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: wrongChainID, proof: baseProof,
		},
		{
			name: "proof is bound to target", caller: owner,
			key: victim, target: independentAddress(0x9002), selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: chainID, proof: baseProof,
		},
		{
			name: "proof is bound to selector list", caller: owner,
			key: victim, target: target, selectors: wrongSelector,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: chainID, proof: baseProof,
		},
		{
			name: "proof is bound to APLO budget", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: big.NewInt(51), gaplo: gaplo, expiry: expiry, chainID: chainID, proof: baseProof,
		},
		{
			name: "proof is bound to GAplo budget", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: big.NewInt(71), expiry: expiry, chainID: chainID, proof: baseProof,
		},
		{
			name: "proof is bound to expiry", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: big.NewInt(111), chainID: chainID, proof: baseProof,
		},
		{
			name: "empty proof is rejected", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: chainID, proof: []byte{},
		},
		{
			name: "high-s proof is noncanonical", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: chainID, proof: highS,
		},
		{
			name: "truncated proof is rejected", caller: owner,
			key: victim, target: target, selectors: selectors,
			aplo: aplo, gaplo: gaplo, expiry: expiry, chainID: chainID, proof: baseProof[:64],
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := independentRegistryState(t)
			input, err := registry.ABI.Pack(
				"CreateSessionKey", test.key, test.target, test.selectors,
				test.aplo, test.gaplo, test.expiry, test.proof,
			)
			if err != nil {
				t.Fatal(err)
			}
			before := db.Copy().IntermediateRoot(false)
			if _, _, err := registry.Run(
				db, test.caller, input, registry.CreateGas+registry.SelectorGas,
				100, false, test.chainID,
			); err == nil {
				t.Fatal("creation with absent or mismatched key-possession proof succeeded")
			}
			if after := db.Copy().IntermediateRoot(false); after != before {
				t.Fatalf("rejected proof changed state root: before=%s after=%s", before, after)
			}
			if registry.Get(db, victim) != nil ||
				registry.Used(db, victim) ||
				registry.Recipient(db, victim) != victim {
				t.Fatal("rejected proof registered or redirected a known victim address")
			}
		})
	}

	// The actual session private key can claim its own address and enable the
	// redirect; a proof from any other key cannot squat on it.
	db := independentRegistryState(t)
	input := independentPackCreate(
		t, owner, victimKey, victim, target, selectors, aplo, gaplo, expiry, chainID,
	)
	if _, _, err := registry.Run(
		db, owner, input, registry.CreateGas+registry.SelectorGas, 100, false, chainID,
	); err != nil {
		t.Fatalf("legitimate key-possession proof rejected: %v", err)
	}
	if got := registry.Recipient(db, victim); got != owner {
		t.Fatalf("legitimate session key not redirected to its owner: got %s want %s", got, owner)
	}
}

func TestRegistrySessionKeysIndependentPublicSelectorCapBoundary(t *testing.T) {
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	target := independentAddress(0x9000)
	chainID := big.NewInt(1)
	for _, count := range []int{registry.MaxSelectors, registry.MaxSelectors + 1} {
		t.Run(big.NewInt(int64(count)).String()+"_selectors", func(t *testing.T) {
			sessionKey, err := crypto.GenerateKey()
			if err != nil {
				t.Fatal(err)
			}
			key := crypto.PubkeyToAddress(sessionKey.PublicKey)
			selectors := make([][4]byte, count)
			for i := range selectors {
				selectors[i] = [4]byte{0x42, byte(i >> 16), byte(i >> 8), byte(i)}
			}
			aplo, gaplo, expiry := big.NewInt(50), big.NewInt(70), big.NewInt(110)
			input := independentPackCreate(
				t, owner, sessionKey, key, target, selectors, aplo, gaplo, expiry, chainID,
			)
			db := independentRegistryState(t)
			before := db.Copy().IntermediateRoot(false)
			_, _, err = registry.Run(
				db, owner, input, registry.CreateGas+registry.SelectorGas*uint64(count),
				100, false, chainID,
			)
			if count <= registry.MaxSelectors {
				if err != nil {
					t.Fatalf("maximum selector count rejected: %v", err)
				}
				if session := registry.Get(db, key); session == nil || len(session.Selectors) != count {
					t.Fatalf("registered selector count mismatch: session=%#v", session)
				}
			} else {
				if err == nil {
					t.Fatal("selector count above public ABI cap was accepted")
				}
				if after := db.Copy().IntermediateRoot(false); after != before {
					t.Fatalf("over-cap request mutated state root: before=%s after=%s", before, after)
				}
				if registry.Get(db, key) != nil || registry.Used(db, key) {
					t.Fatal("over-cap request registered or permanently reserved its key")
				}
			}
		})
	}
}

func FuzzSessionKeysRunCanonicalABIIndependent(f *testing.F) {
	create, _ := registry.ABI.Pack(
		"CreateSessionKey", independentAddress(0xb0), independentAddress(0x9000),
		[][4]byte{{1, 2, 3, 4}}, big.NewInt(50), big.NewInt(70),
		big.NewInt(101), []byte{},
	)
	get, _ := registry.ABI.Pack("getSession", independentAddress(0xb0))
	revoke, _ := registry.ABI.Pack("RevokeSessionKey", independentAddress(0xb0))
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3})
	f.Add(create)
	f.Add(create[:len(create)-1])
	f.Add(get)
	f.Add(revoke)
	f.Fuzz(func(t *testing.T, input []byte) {
		db := independentRegistryState(t)
		before := db.Copy().IntermediateRoot(false)
		_, _, err := registry.Run(
			db, independentAddress(0xa0), input, 2_000_000, 1, false, big.NewInt(1),
		)
		if err != nil {
			after := db.Copy().IntermediateRoot(false)
			if after != before {
				t.Fatalf("rejected ABI input mutated registry state: before=%s after=%s input=%x", before, after, input)
			}
		}
	})
}
