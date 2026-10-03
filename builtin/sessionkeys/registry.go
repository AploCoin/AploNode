// Package sessionkeys implements the native, journaled session authorization registry.
package sessionkeys

import (
	"bytes"
	"errors"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// Address is the reserved builtin address that stores the session registry.
var Address = common.HexToAddress("0x1237")

const (
	// MaxLifetime bounds the number of blocks a new session may remain active.
	MaxLifetime uint64 = 1000

	// MaxSelectors bounds the selector whitelist and the work needed to read it.
	MaxSelectors = 32

	// MaxOwnerSessions bounds the number of live sessions registered by one owner.
	MaxOwnerSessions uint64 = 64

	// MaxBucketEntries bounds end-of-block cleanup for a single expiry height.
	MaxBucketEntries uint64 = 32

	// CreateGas pays for registration metadata and bounded future expiry cleanup.
	CreateGas uint64 = 400000

	// SelectorGas pays for one whitelist slot and its eventual removal.
	SelectorGas uint64 = 25000

	// RevokeGas covers removal of a session and both packed-list indexes.
	RevokeGas uint64 = 300000
)

var (
	// ErrInvalid is returned for malformed, unauthorized or inadmissible operations.
	ErrInvalid = errors.New("invalid session key operation")

	// ErrOutOfGas is returned when the caller cannot pay the builtin's fixed cost.
	ErrOutOfGas = errors.New("session builtin out of gas")
)

// ABI describes the registry's public methods and lifecycle events.
var ABI = mustABI(`
[
  {
    "type": "function",
    "name": "CreateSessionKey",
    "inputs": [
      {
        "name": "key",
        "type": "address"
      },
      {
        "name": "target",
        "type": "address"
      },
      {
        "name": "selectors",
        "type": "bytes4[]"
      },
      {
        "name": "aplo",
        "type": "uint256"
      },
      {
        "name": "gaplo",
        "type": "uint256"
      },
      {
        "name": "expiry",
        "type": "uint256"
      },
      {
        "name": "proof",
        "type": "bytes"
      }
    ],
    "outputs": []
  },
  {
    "type": "function",
    "name": "RevokeSessionKey",
    "inputs": [
      {
        "name": "key",
        "type": "address"
      }
    ],
    "outputs": []
  },
  {
    "type": "function",
    "name": "getSession",
    "inputs": [
      {
        "name": "key",
        "type": "address"
      }
    ],
    "outputs": [
      {
        "name": "owner",
        "type": "address"
      },
      {
        "name": "target",
        "type": "address"
      },
      {
        "name": "aplo",
        "type": "uint256"
      },
      {
        "name": "gaplo",
        "type": "uint256"
      },
      {
        "name": "nonce",
        "type": "uint256"
      },
      {
        "name": "expiry",
        "type": "uint256"
      }
    ]
  },
  {
    "type": "event",
    "name": "SessionKeyCreated",
    "inputs": [
      {
        "name": "owner",
        "type": "address",
        "indexed": true
      },
      {
        "name": "key",
        "type": "address",
        "indexed": true
      },
      {
        "name": "target",
        "type": "address",
        "indexed": false
      },
      {
        "name": "aplo",
        "type": "uint256",
        "indexed": false
      },
      {
        "name": "gaplo",
        "type": "uint256",
        "indexed": false
      },
      {
        "name": "expiry",
        "type": "uint256",
        "indexed": false
      }
    ]
  },
  {
    "type": "event",
    "name": "SessionKeyRevoked",
    "inputs": [
      {
        "name": "owner",
        "type": "address",
        "indexed": true
      },
      {
        "name": "key",
        "type": "address",
        "indexed": true
      }
    ]
  }
]
`)

// mustABI parses the static registry ABI, failing fast on an invalid declaration.
func mustABI(s string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic(err)
	}
	return parsed
}

// Slot derives a namespaced registry storage key for a record or list entry.
func Slot(kind string, addr common.Address, index uint64) common.Hash {
	return crypto.Keccak256Hash(
		[]byte("aplo.sessionkeys.v1/"+kind),
		addr[:],
		common.LeftPadBytes(new(big.Int).SetUint64(index).Bytes(), 32),
	)
}

// get reads one registry slot through the journaled state database.
func get(db types.StateDB, kind string, addr common.Address, index uint64) common.Hash {
	return db.GetState(Address, Slot(kind, addr, index))
}

// set writes one registry slot through the journaled state database.
func set(db types.StateDB, kind string, addr common.Address, index uint64, value common.Hash) {
	db.SetState(Address, Slot(kind, addr, index), value)
}

// num encodes an unsigned index or counter as a storage word.
func num(n uint64) common.Hash {
	return common.BigToHash(new(big.Int).SetUint64(n))
}

// bucket encodes an absolute expiry height as a packed-list group address.
func bucket(expiry uint64) common.Address {
	return common.BytesToAddress(new(big.Int).SetUint64(expiry).Bytes())
}

// Used reports whether a key has ever been registered on the current state branch.
func Used(db types.StateDB, key common.Address) bool {
	return get(db, "used", key, 0) != (common.Hash{})
}

// Owner returns the active session owner, or zero after revoke or expiry cleanup.
func Owner(db types.StateDB, key common.Address) common.Address {
	return common.BytesToAddress(get(db, "owner", key, 0).Bytes())
}

// Recipient resolves the permanent credit recipient for a registered key.
// Revocation and expiry remove authorization but retain this owner mapping.
func Recipient(db types.StateDB, key common.Address) common.Address {
	if h := get(db, "used", key, 0); h != (common.Hash{}) {
		return common.BytesToAddress(h[:])
	}
	return key
}

// Session is the active authorization loaded from registry storage.
// AploSpent and GAploSpent are remaining allowances despite their historic names.
type Session struct {
	Owner, Target common.Address // Funding owner and single permitted top-level target.

	AploSpent, GAploSpent *big.Int  // Remaining native-value and gas-fee allowances.
	Nonce, Expiry         uint64    // Key account nonce and inclusive last authorized block.
	Selectors             [][4]byte // Exact four-byte selectors allowed at Target.
}

// Get reads an active session record, including the key account's nonce.
// It returns nil if the record is absent or has an invalid selector count.
func Get(db types.StateDB, key common.Address) *Session {
	o := Owner(db, key)
	if o == (common.Address{}) {
		return nil
	}
	s := &Session{
		Owner:      o,
		Target:     common.BytesToAddress(get(db, "target", key, 0).Bytes()),
		AploSpent:  get(db, "aplo", key, 0).Big(),
		GAploSpent: get(db, "gaplo", key, 0).Big(),
		Nonce:      db.GetNonce(key),
		Expiry:     get(db, "expiry", key, 0).Big().Uint64(),
	}
	n := get(db, "selectors", key, 0).Big().Uint64()
	if n > MaxSelectors {
		return nil
	}
	for i := uint64(0); i < n; i++ {
		h := get(db, "selector", key, i)
		var f [4]byte
		copy(f[:], h[:4])
		s.Selectors = append(s.Selectors, f)
	}
	return s
}

// Validate checks inclusive expiry, the top-level target and selector, and
// the remaining allowances. The fee argument is the maximum fee reservation.
func Validate(s *Session, block uint64, target *common.Address, data []byte, value, fee *big.Int) error {
	if s == nil || block > s.Expiry ||
		target == nil || *target != s.Target || len(data) < 4 ||
		value.Sign() < 0 || fee.Sign() < 0 ||
		value.Cmp(s.AploSpent) > 0 || fee.Cmp(s.GAploSpent) > 0 {
		return ErrInvalid
	}
	for _, f := range s.Selectors {
		if bytes.Equal(f[:], data[:4]) {
			return nil
		}
	}
	return ErrInvalid
}

// GaploSlot returns the canonical GAplo balances mapping slot for addr.
func GaploSlot(addr common.Address) common.Hash {
	return crypto.Keccak256Hash(common.LeftPadBytes(addr[:], 32), make([]byte, 32))
}

// GaploBalance reads addr's balance from the canonical GAplo storage layout.
func GaploBalance(db types.StateDB, addr common.Address) *big.Int {
	return db.GetState(params.GAploContractAddress, GaploSlot(addr)).Big()
}

// CanonicalGaplo reports whether GAplo has the exact supported runtime.
// Recipient rewriting and direct balance reads depend on this bytecode layout.
func CanonicalGaplo(db types.StateDB) bool {
	return db.GetCodeHash(params.GAploContractAddress) == crypto.Keccak256Hash(common.FromHex(params.GAPLO))
}

// reserved excludes zero, precompiles and protocol addresses from key ownership.
func reserved(a common.Address) bool {
	return a == (common.Address{}) || a == Address ||
		a == params.GAploContractAddress || a == common.HexToAddress("0x1235") ||
		a == common.HexToAddress("0x1236") || new(big.Int).SetBytes(a[:]).Cmp(big.NewInt(9)) <= 0
}

// Create registers a fresh key and updates its owner and expiry indexes.
// The caller must already be authenticated and its key-possession proof verified
// by Run. This helper enforces state, budget, whitelist and lifetime constraints.
func Create(db types.StateDB, owner, key, target common.Address, selectors [][4]byte, aplo, gaplo *big.Int, expiry, block uint64) error {
	if reserved(key) || reserved(owner) || key == owner ||
		Used(db, key) || Used(db, owner) || db.Exist(key) || db.GetCodeSize(owner) != 0 ||
		target == (common.Address{}) || target == Address || !CanonicalGaplo(db) ||
		db.GetNonce(Address) != 1 || db.GetCodeSize(Address) != 0 ||
		GaploBalance(db, key).Sign() != 0 || expiry < block ||
		block > math.MaxUint64-MaxLifetime || expiry > block+MaxLifetime ||
		len(selectors) == 0 || len(selectors) > MaxSelectors ||
		aplo.Sign() < 0 || gaplo.Sign() < 0 || aplo.BitLen() > 256 || gaplo.BitLen() > 256 {
		return ErrInvalid
	}
	n := get(db, "ownerCount", owner, 0).Big().Uint64()
	b := bucket(expiry)
	bn := get(db, "bucketCount", b, 0).Big().Uint64()
	if n >= MaxOwnerSessions || bn >= MaxBucketEntries {
		return ErrInvalid
	}
	for i, f := range selectors {
		for j := 0; j < i; j++ {
			if selectors[j] == f {
				return ErrInvalid
			}
		}
	}
	set(db, "used", key, 0, common.BytesToHash(owner[:]))
	set(db, "owner", key, 0, common.BytesToHash(owner[:]))
	set(db, "target", key, 0, common.BytesToHash(target[:]))
	set(db, "aplo", key, 0, common.BigToHash(aplo))
	set(db, "gaplo", key, 0, common.BigToHash(gaplo))
	set(db, "expiry", key, 0, num(expiry))
	set(db, "selectors", key, 0, num(uint64(len(selectors))))
	for i, f := range selectors {
		var h common.Hash
		copy(h[:4], f[:])
		set(db, "selector", key, uint64(i), h)
	}
	set(db, "ownerList", owner, n, common.BytesToHash(key[:]))
	set(db, "ownerIndex", key, 0, num(n))
	set(db, "ownerCount", owner, 0, num(n+1))
	set(db, "bucketList", b, bn, common.BytesToHash(key[:]))
	set(db, "bucketIndex", key, 0, num(bn))
	set(db, "bucketCount", b, 0, num(bn+1))
	data, _ := ABI.Events["SessionKeyCreated"].Inputs.NonIndexed().Pack(target, aplo, gaplo, new(big.Int).SetUint64(expiry))
	db.AddLog(&types.Log{
		Address: Address,
		Topics:  []common.Hash{ABI.Events["SessionKeyCreated"].ID, common.BytesToHash(owner[:]), common.BytesToHash(key[:])},
		Data:    data,
	})
	return nil
}

// removeList removes a key from an indexed packed list and repairs the
// moved entry's index. Its caller must supply a live session in that list.
func removeList(db types.StateDB, key, group common.Address, prefix string) {
	count := get(db, prefix+"Count", group, 0).Big().Uint64()
	index := get(db, prefix+"Index", key, 0).Big().Uint64()
	last := get(db, prefix+"List", group, count-1)
	if index != count-1 {
		set(db, prefix+"List", group, index, last)
		set(db, prefix+"Index", common.BytesToAddress(last[:]), 0, num(index))
	}
	set(db, prefix+"List", group, count-1, common.Hash{})
	set(db, prefix+"Count", group, 0, num(count-1))
	set(db, prefix+"Index", key, 0, common.Hash{})
}

// remove erases active authorization and indexes while preserving the
// permanent owner tombstone and the key account's nonce.
func remove(db types.StateDB, key common.Address) {
	s := Get(db, key)
	if s == nil {
		return
	}
	removeList(db, key, s.Owner, "owner")
	removeList(db, key, bucket(s.Expiry), "bucket")
	for i := range s.Selectors {
		set(db, "selector", key, uint64(i), common.Hash{})
	}
	for _, kind := range []string{"owner", "target", "aplo", "gaplo", "expiry", "selectors"} {
		set(db, kind, key, 0, common.Hash{})
	}
}

// Revoke removes an active session owned by owner and emits its lifecycle event.
func Revoke(db types.StateDB, owner, key common.Address) error {
	if owner == (common.Address{}) || Owner(db, key) == (common.Address{}) || Owner(db, key) != owner || Used(db, owner) {
		return ErrInvalid
	}
	remove(db, key)
	db.AddLog(&types.Log{
		Address: Address,
		Topics:  []common.Hash{ABI.Events["SessionKeyRevoked"].ID, common.BytesToHash(owner[:]), common.BytesToHash(key[:])},
	})
	return nil
}

// Cleanup expires the exact absolute-height bucket after block execution.
// Sessions remain usable throughout their expiry block, including its rewards.
func Cleanup(db types.StateDB, block uint64) {
	b := bucket(block)
	for n := get(db, "bucketCount", b, 0).Big().Uint64(); n > 0; n-- {
		key := common.BytesToAddress(get(db, "bucketList", b, n-1).Bytes())
		remove(db, key)
	}
}

// Charge subtracts successful native spending and the actual transaction fee.
// Callers must validate sufficient allowances first and journal failed execution.
func Charge(db types.StateDB, key common.Address, value, fee *big.Int) {
	s := Get(db, key)
	set(db, "aplo", key, 0, common.BigToHash(new(big.Int).Sub(s.AploSpent, value)))
	set(db, "gaplo", key, 0, common.BigToHash(new(big.Int).Sub(s.GAploSpent, fee)))
}

// Run dispatches canonical ABI calls and charges before any state write.
// Mutations require the EVM dispatcher to authenticate a direct owner call first.
// Every call supplies an explicit uint256 chain ID. Registration always verifies
// the proof against that effective signing domain.
func Run(db types.StateDB, caller common.Address, input []byte, gas, block uint64, readOnly bool, chainID *big.Int) ([]byte, uint64, error) {
	if chainID == nil || chainID.Sign() < 0 || chainID.BitLen() > 256 {
		return nil, gas, ErrInvalid
	}
	if len(input) < 4 || len(input) > 4+32*(12+MaxSelectors) {
		return nil, gas, ErrInvalid
	}
	m, err := ABI.MethodById(input[:4])
	if err != nil {
		return nil, gas, ErrInvalid
	}
	args, err := m.Inputs.Unpack(input[4:])
	if err != nil {
		return nil, gas, ErrInvalid
	}
	encoded, err := m.Inputs.Pack(args...)
	if err != nil || !bytes.Equal(encoded, input[4:]) {
		return nil, gas, ErrInvalid
	}
	if m.Name == "getSession" {
		if gas < 100000 {
			return nil, 0, ErrOutOfGas
		}
		s := Get(db, args[0].(common.Address))
		if s == nil {
			s = &Session{AploSpent: new(big.Int), GAploSpent: new(big.Int)}
		}
		r, err := m.Outputs.Pack(
			s.Owner, s.Target, s.AploSpent, s.GAploSpent,
			new(big.Int).SetUint64(s.Nonce), new(big.Int).SetUint64(s.Expiry),
		)
		return r, gas - 100000, err
	}
	if readOnly {
		return nil, gas, ErrInvalid
	}
	switch m.Name {
	case "CreateSessionKey":
		fs := args[2].([][4]byte)
		cost := CreateGas + SelectorGas*uint64(len(fs))
		if gas < cost {
			return nil, 0, ErrOutOfGas
		}
		exp := args[5].(*big.Int)
		if !exp.IsUint64() {
			return nil, gas - cost, ErrInvalid
		}
		key := args[0].(common.Address)
		target := args[1].(common.Address)
		if !VerifyProof(caller, key, target, fs, args[3].(*big.Int), args[4].(*big.Int), exp, chainID, args[6].([]byte)) {
			return nil, gas - cost, ErrInvalid
		}
		err = Create(
			db, caller, args[0].(common.Address), args[1].(common.Address), fs,
			args[3].(*big.Int), args[4].(*big.Int), exp.Uint64(), block,
		)
		return nil, gas - cost, err
	case "RevokeSessionKey":
		if gas < RevokeGas {
			return nil, 0, ErrOutOfGas
		}
		return nil, gas - RevokeGas, Revoke(db, caller, args[0].(common.Address))
	}
	return nil, gas, ErrInvalid
}

// ValidateState checks the native protocol accounts without changing state.
// Genesis reserves the code-free registry at nonce one and installs canonical
// GAplo. Existing devnets lacking these accounts must be recreated, not migrated.
func ValidateState(db types.StateDB) error {
	if db.GetNonce(Address) != 1 || db.GetCodeSize(Address) != 0 {
		return errors.New("invalid native session registry: genesis must reserve nonce one without code")
	}
	if !CanonicalGaplo(db) {
		return errors.New("invalid native GAplo runtime: genesis must install canonical bytecode")
	}
	return nil
}

// NativeSpend includes the native-token builtin's explicit transfer amount.
func NativeSpend(target *common.Address, data []byte, value *big.Int) *big.Int {
	total := new(big.Int).Set(value)
	if target != nil && *target == params.AploContractAddress && len(data) == 68 &&
		bytes.Equal(data[:4], crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]) {
		total.Add(total, new(big.Int).SetBytes(data[36:68]))
	}
	return total
}

// ProofHash binds key possession to this exact owner-authorized configuration.
// This uses the chain ID replay domain, as with EIP155 transactions; networks
// intentionally sharing a chain ID must not treat it as a unique genesis ID.
func ProofHash(owner, key, target common.Address, selectors [][4]byte, aplo, gaplo, expiry, chainID *big.Int) common.Hash {
	if chainID == nil || chainID.Sign() < 0 || chainID.BitLen() > 256 {
		return common.Hash{}
	}
	payload, err := ABI.Methods["CreateSessionKey"].Inputs[:6].Pack(key, target, selectors, aplo, gaplo, expiry)
	if err != nil {
		return common.Hash{}
	}
	return crypto.Keccak256Hash(
		[]byte("APLO_SESSION_KEYS_ACCEPT_V1"),
		Address[:],
		common.LeftPadBytes(chainID.Bytes(), 32),
		owner[:],
		payload,
	)
}

// VerifyProof checks a 65-byte low-S secp256k1 acceptance signature with
// recovery ID 0 or 1, bound to the exact owner, configuration and chain ID.
func VerifyProof(owner, key, target common.Address, selectors [][4]byte, aplo, gaplo, expiry, chainID *big.Int, proof []byte) bool {
	if len(proof) != crypto.SignatureLength || proof[64] > 1 || chainID == nil || chainID.Sign() < 0 || chainID.BitLen() > 256 {
		return false
	}
	if !crypto.ValidateSignatureValues(proof[64], new(big.Int).SetBytes(proof[:32]), new(big.Int).SetBytes(proof[32:64]), true) {
		return false
	}
	hash := ProofHash(owner, key, target, selectors, aplo, gaplo, expiry, chainID)
	pub, err := crypto.SigToPub(hash[:], proof)
	return err == nil && crypto.PubkeyToAddress(*pub) == key
}
