package core

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

var independentSessionSelector = [4]byte{0x70, 0x11, 0x22, 0x33}

func independentSessionProof(
	t *testing.T,
	owner, key, target common.Address,
	signer *ecdsa.PrivateKey,
	selectors [][4]byte,
	aplo, gaplo, expiry, chainID *big.Int,
) []byte {
	t.Helper()
	if got := crypto.PubkeyToAddress(signer.PublicKey); got != key {
		t.Fatalf("proof signer address %s does not match session key %s", got, key)
	}
	hash := sessionkeys.ProofHash(owner, key, target, selectors, aplo, gaplo, expiry, chainID)
	proof, err := crypto.Sign(hash[:], signer)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

type independentSessionFixture struct {
	config     *params.ChainConfig
	db         *state.StateDB
	ownerKey   *ecdsa.PrivateKey
	sessionKey *ecdsa.PrivateKey
	owner      common.Address
	session    common.Address
	target     common.Address
	coinbase   common.Address
	header     *types.Header
}

// newIndependentSessionFixture funds an owner and the canonical GAplo contract,
// initializes the native registry, and seeds one proof-backed session key.
func newIndependentSessionFixture(
	t *testing.T,
	targetCode []byte,
	expiry uint64,
	aploAllowance, gaploAllowance *big.Int,
) *independentSessionFixture {
	t.Helper()
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	configCopy := *params.TestChainConfig
	config := &configCopy
	db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	session := crypto.PubkeyToAddress(sessionKey.PublicKey)
	target := common.HexToAddress("0x0000000000000000000000000000000000009000")
	coinbase := common.HexToAddress("0x0000000000000000000000000000000000009001")
	header := &types.Header{
		Number:     big.NewInt(1),
		GasLimit:   1_000_000,
		Time:       1,
		Difficulty: big.NewInt(1),
		BaseFee:    big.NewInt(0),
		Coinbase:   coinbase,
	}
	db.SetBalance(owner, big.NewInt(1000))
	db.SetNonce(owner, 7)
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetNonce(sessionkeys.Address, 1)
	db.SetState(
		params.GAploContractAddress, sessionkeys.GaploSlot(owner),
		common.BigToHash(big.NewInt(1_000_000_000)),
	)
	db.SetState(
		params.GAploContractAddress, common.HexToHash("0x2"),
		common.BigToHash(big.NewInt(1_000_000_000)),
	)
	db.SetCode(target, targetCode)
	if err := sessionkeys.ValidateState(db); err != nil {
		t.Fatalf("invalid native Session Keys state: %v", err)
	}
	if err := sessionkeys.Create(
		db, owner, session, target, [][4]byte{independentSessionSelector},
		aploAllowance, gaploAllowance, expiry, header.Number.Uint64(),
	); err != nil {
		t.Fatalf("create test session: %v", err)
	}
	return &independentSessionFixture{
		config: config, db: db, ownerKey: ownerKey, sessionKey: sessionKey,
		owner: owner, session: session, target: target, coinbase: coinbase, header: header,
	}
}

func (f *independentSessionFixture) signedTx(
	t *testing.T,
	key *ecdsa.PrivateKey,
	nonce uint64,
	to common.Address,
	value *big.Int,
	gas uint64,
	data []byte,
) *types.Transaction {
	t.Helper()
	unsigned := types.NewTransaction(nonce, to, value, gas, big.NewInt(1), data)
	tx, err := types.SignTx(unsigned, types.MakeSigner(f.config, f.header.Number), key)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func (f *independentSessionFixture) apply(t *testing.T, tx *types.Transaction) (*types.Receipt, error) {
	t.Helper()
	f.db.Prepare(tx.Hash(), 0)
	var usedGas uint64
	return ApplyTransaction(
		f.config, nil, &f.coinbase,
		new(GasPool).AddGas(f.header.GasLimit), f.db, f.header, tx,
		&usedGas, vm.Config{}, nil,
	)
}

func independentEVMCall(
	f *independentSessionFixture,
	config *params.ChainConfig,
	caller, target common.Address,
	input []byte,
) error {
	rules := config.Rules(f.header.Number, false)
	if rules.IsBerlin {
		f.db.PrepareAccessList(caller, &target, vm.ActivePrecompiles(rules), nil)
	}
	evm := vm.NewEVM(
		vm.BlockContext{
			CanTransfer: CanTransfer,
			Transfer:    Transfer,
			GetHash:     func(uint64) common.Hash { return common.Hash{} },
			Coinbase:    f.coinbase,
			GasLimit:    f.header.GasLimit,
			BlockNumber: f.header.Number,
			Time:        new(big.Int).SetUint64(f.header.Time),
			Difficulty:  f.header.Difficulty,
			BaseFee:     f.header.BaseFee,
		},
		vm.TxContext{Origin: caller, GasPrice: big.NewInt(1)},
		f.db, config, vm.Config{}, nil,
	)
	_, _, err := evm.Call(types.AccountRef(caller), target, input, 1_000_000, new(big.Int))
	return err
}

func TestSessionKeysEVMRejectsMissingSigningDomainWithoutMutationIndependent(t *testing.T) {
	f := newIndependentSessionFixture(t, nil, 100, big.NewInt(100), big.NewInt(100_000))
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	session := crypto.PubkeyToAddress(key.PublicKey)
	selectors := [][4]byte{independentSessionSelector}
	aplo, gaplo, expiry := big.NewInt(10), big.NewInt(20), big.NewInt(50)
	proof := independentSessionProof(
		t, f.owner, session, f.target, key, selectors, aplo, gaplo, expiry, big.NewInt(1),
	)
	input, err := sessionkeys.ABI.Pack(
		"CreateSessionKey", session, f.target, selectors, aplo, gaplo, expiry, proof,
	)
	if err != nil {
		t.Fatal(err)
	}
	config := *f.config
	config.ChainID = nil
	before := f.db.Copy().IntermediateRoot(false)
	err = independentEVMCall(f, &config, f.owner, sessionkeys.Address, input)
	if !errors.Is(err, vm.ErrExecutionReverted) {
		t.Fatalf("missing EVM signing domain error=%v, want execution revert", err)
	}
	if after := f.db.Copy().IntermediateRoot(false); after != before {
		t.Fatalf("missing signing domain mutated state: before=%s after=%s", before, after)
	}
	if sessionkeys.Used(f.db, session) || sessionkeys.Get(f.db, session) != nil {
		t.Fatal("missing signing domain reserved or registered the key")
	}
}

func independentCallerOriginCode() []byte {
	// ORIGIN; MSTORE[0]; CALLER; MSTORE[1]; STOP.
	return []byte{0x32, 0x60, 0x00, 0x55, 0x33, 0x60, 0x01, 0x55, 0x00}
}

func TestSessionKeysSignedApplyTransactionIdentityAndSeparateFundsIndependent(t *testing.T) {
	f := newIndependentSessionFixture(
		t, independentCallerOriginCode(), 1000, big.NewInt(100), big.NewInt(1_000_000),
	)
	data := append(independentSessionSelector[:], 0x01)
	tx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(11), 120_000, data)
	ownerAPLOBefore := new(big.Int).Set(f.db.GetBalance(f.owner))
	ownerGAploBefore := sessionkeys.GaploBalance(f.db, f.owner)
	receipt, err := f.apply(t, tx)
	if err != nil {
		t.Fatalf("signed session transaction rejected: %v", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.GasUsed == 0 {
		t.Fatalf("session transaction receipt: status=%d gas=%d", receipt.Status, receipt.GasUsed)
	}
	if got := common.BytesToAddress(f.db.GetState(f.target, common.Hash{}).Bytes()); got != f.owner {
		t.Fatalf("contract observed tx.origin %s, want owner %s", got, f.owner)
	}
	if got := common.BytesToAddress(
		f.db.GetState(f.target, common.BigToHash(big.NewInt(1))).Bytes(),
	); got != f.session {
		t.Fatalf("contract observed msg.sender %s, want session key %s", got, f.session)
	}
	wantOwnerAPLO := new(big.Int).Sub(ownerAPLOBefore, big.NewInt(11))
	if got := f.db.GetBalance(f.owner); got.Cmp(wantOwnerAPLO) != 0 {
		t.Fatalf("owner APLO balance=%s, want %s after value transfer", got, wantOwnerAPLO)
	}
	if f.db.GetBalance(f.session).Sign() != 0 ||
		f.db.GetNonce(f.owner) != 7 || f.db.GetNonce(f.session) != 1 {
		t.Fatalf(
			"account ownership/nonce mismatch: owner APLO=%s owner nonce=%d session APLO=%s session nonce=%d",
			f.db.GetBalance(f.owner), f.db.GetNonce(f.owner),
			f.db.GetBalance(f.session), f.db.GetNonce(f.session),
		)
	}
	s := sessionkeys.Get(f.db, f.session)
	wantAPLO := big.NewInt(89)
	wantGAplo := new(big.Int).Sub(big.NewInt(1_000_000), new(big.Int).SetUint64(receipt.GasUsed))
	if s.AploSpent.Cmp(wantAPLO) != 0 || s.GAploSpent.Cmp(wantGAplo) != 0 {
		t.Fatalf("session allowances APLO=%s GAplo=%s, want %s/%s", s.AploSpent, s.GAploSpent, wantAPLO, wantGAplo)
	}
	wantOwnerGAplo := new(big.Int).Sub(ownerGAploBefore, new(big.Int).SetUint64(receipt.GasUsed))
	if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(wantOwnerGAplo) != 0 {
		t.Fatalf("owner GAplo balance=%s, want %s after actual gas charge", got, wantOwnerGAplo)
	}
	if sessionkeys.GaploBalance(f.db, f.session).Sign() != 0 {
		t.Fatal("session key paid GAplo fees from its own address")
	}

	ownerBeforeReplay := new(big.Int).Set(f.db.GetBalance(f.owner))
	gaBeforeReplay := sessionkeys.GaploBalance(f.db, f.owner)
	sessionBeforeReplay := sessionkeys.Get(f.db, f.session)
	if _, err := f.apply(t, tx); err == nil {
		t.Fatal("replayed signed transaction was accepted")
	}
	if f.db.GetBalance(f.owner).Cmp(ownerBeforeReplay) != 0 ||
		sessionkeys.GaploBalance(f.db, f.owner).Cmp(gaBeforeReplay) != 0 ||
		f.db.GetNonce(f.session) != 1 {
		t.Fatal("rejected replay changed balances or nonce")
	}
	if got := sessionkeys.Get(f.db, f.session); got.AploSpent.Cmp(sessionBeforeReplay.AploSpent) != 0 ||
		got.GAploSpent.Cmp(sessionBeforeReplay.GAploSpent) != 0 {
		t.Fatal("rejected replay changed session allowances")
	}
}

func TestSessionKeysInvalidSignedTransactionsLeaveStateUnchangedIndependent(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *independentSessionFixture) *types.Transaction
	}{
		{
			name: "unlisted selector",
			mutate: func(t *testing.T, f *independentSessionFixture) *types.Transaction {
				return f.signedTx(
					t, f.sessionKey, 0, f.target, big.NewInt(1), 100_000,
					[]byte{0xde, 0xad, 0xbe, 0xef},
				)
			},
		},
		{
			name: "wrong target",
			mutate: func(t *testing.T, f *independentSessionFixture) *types.Transaction {
				return f.signedTx(
					t, f.sessionKey, 0, common.HexToAddress("0x9002"),
					big.NewInt(1), 100_000, independentSessionSelector[:],
				)
			},
		},
		{
			name: "short selector",
			mutate: func(t *testing.T, f *independentSessionFixture) *types.Transaction {
				return f.signedTx(
					t, f.sessionKey, 0, f.target, big.NewInt(1), 100_000,
					independentSessionSelector[:3],
				)
			},
		},
		{
			name: "native allowance",
			mutate: func(t *testing.T, f *independentSessionFixture) *types.Transaction {
				return f.signedTx(
					t, f.sessionKey, 0, f.target, big.NewInt(101), 100_000,
					independentSessionSelector[:],
				)
			},
		},
		{
			name: "fee allowance",
			mutate: func(t *testing.T, f *independentSessionFixture) *types.Transaction {
				return f.signedTx(
					t, f.sessionKey, 0, f.target, big.NewInt(0), 1_000_001,
					independentSessionSelector[:],
				)
			},
		},
		{
			name: "wrong chain id",
			mutate: func(t *testing.T, f *independentSessionFixture) *types.Transaction {
				unsigned := types.NewTransaction(
					0, f.target, big.NewInt(1), 100_000, big.NewInt(1),
					independentSessionSelector[:],
				)
				tx, err := types.SignTx(unsigned, types.NewEIP155Signer(big.NewInt(999)), f.sessionKey)
				if err != nil {
					t.Fatal(err)
				}
				return tx
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newIndependentSessionFixture(
				t, independentCallerOriginCode(), 1000,
				big.NewInt(100), big.NewInt(1_000_000),
			)
			tx := test.mutate(t, f)
			ownerAPLO := new(big.Int).Set(f.db.GetBalance(f.owner))
			ownerGAplo := sessionkeys.GaploBalance(f.db, f.owner)
			session := sessionkeys.Get(f.db, f.session)
			if _, err := f.apply(t, tx); err == nil {
				t.Fatal("invalid session transaction was accepted")
			}
			current := sessionkeys.Get(f.db, f.session)
			if f.db.GetBalance(f.owner).Cmp(ownerAPLO) != 0 ||
				sessionkeys.GaploBalance(f.db, f.owner).Cmp(ownerGAplo) != 0 ||
				f.db.GetNonce(f.session) != 0 {
				t.Fatal("invalid transaction changed native funds, fees, or session nonce")
			}
			if current.AploSpent.Cmp(session.AploSpent) != 0 ||
				current.GAploSpent.Cmp(session.GAploSpent) != 0 {
				t.Fatal("invalid transaction changed session allowances")
			}
		})
	}
}

// Reverts roll back APLO value while retaining the session nonce and actual fee charge.
func TestSessionKeysRevertAndOutOfGasChargeOnlyActualFeesIndependent(t *testing.T) {
	tests := []struct {
		name string
		code []byte
	}{
		{name: "revert", code: []byte{0x60, 0x00, 0x60, 0x00, 0xfd}},
		{name: "out of gas", code: []byte{0x62, 0xff, 0xff, 0xff, 0x51, 0x00}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newIndependentSessionFixture(
				t, test.code, 1000, big.NewInt(100), big.NewInt(1_000_000),
			)
			tx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(11), 100_000, independentSessionSelector[:])
			ownerAPLO := new(big.Int).Set(f.db.GetBalance(f.owner))
			ownerGAplo := sessionkeys.GaploBalance(f.db, f.owner)
			receipt, err := f.apply(t, tx)
			if err != nil {
				t.Fatalf("reverting session transaction rejected as invalid: %v", err)
			}
			if receipt.Status != types.ReceiptStatusFailed || receipt.GasUsed == 0 {
				t.Fatalf("failed execution receipt status=%d gas=%d", receipt.Status, receipt.GasUsed)
			}
			if test.name == "out of gas" && receipt.GasUsed != tx.Gas() {
				t.Fatalf("out-of-gas transaction used %d gas, want full limit %d", receipt.GasUsed, tx.Gas())
			}
			if f.db.GetBalance(f.owner).Cmp(ownerAPLO) != 0 ||
				f.db.GetBalance(f.target).Sign() != 0 {
				t.Fatal("reverted value transfer was not rolled back")
			}
			if f.db.GetNonce(f.session) != 1 {
				t.Fatalf("failed EVM execution did not consume session nonce: %d", f.db.GetNonce(f.session))
			}
			s := sessionkeys.Get(f.db, f.session)
			if s.AploSpent.Cmp(big.NewInt(100)) != 0 {
				t.Fatalf("reverted APLO value consumed allowance: %s", s.AploSpent)
			}
			actualFee := new(big.Int).SetUint64(receipt.GasUsed)
			if s.GAploSpent.Cmp(new(big.Int).Sub(big.NewInt(1_000_000), actualFee)) != 0 {
				t.Fatalf("GAplo allowance was not charged by actual gas: %s", s.GAploSpent)
			}
			if sessionkeys.GaploBalance(f.db, f.owner).Cmp(
				new(big.Int).Sub(ownerGAplo, actualFee),
			) != 0 {
				t.Fatal("owner did not pay the actual GAplo fee")
			}
		})
	}
}

func TestSessionKeysRejectsMaximumSessionNonceIndependent(t *testing.T) {
	f := newIndependentSessionFixture(
		t, independentCallerOriginCode(), 1000, big.NewInt(100), big.NewInt(1_000_000),
	)
	maxNonce := ^uint64(0)
	f.db.SetNonce(f.session, maxNonce)
	tx := f.signedTx(t, f.sessionKey, maxNonce, f.target, big.NewInt(1), 100_000, independentSessionSelector[:])
	ownerGAplo := sessionkeys.GaploBalance(f.db, f.owner)
	session := sessionkeys.Get(f.db, f.session)
	if _, err := f.apply(t, tx); !errors.Is(err, ErrNonceMax) {
		t.Fatalf("maximum-nonce session transaction error=%v, want %v", err, ErrNonceMax)
	}
	current := sessionkeys.Get(f.db, f.session)
	if f.db.GetNonce(f.session) != maxNonce ||
		f.db.GetNonce(f.owner) != 7 ||
		sessionkeys.GaploBalance(f.db, f.owner).Cmp(ownerGAplo) != 0 {
		t.Fatal("rejected maximum-nonce transaction changed an account nonce or owner fees")
	}
	if current == nil || current.AploSpent.Cmp(session.AploSpent) != 0 ||
		current.GAploSpent.Cmp(session.GAploSpent) != 0 {
		t.Fatal("rejected maximum-nonce transaction changed session allowances")
	}
}

func TestSessionKeysRejectsMissingOwnerAPLOOrGAploWithoutMutationIndependent(t *testing.T) {
	for _, shortage := range []string{"native value", "GAplo gas fee"} {
		t.Run(shortage, func(t *testing.T) {
			f := newIndependentSessionFixture(
				t, independentCallerOriginCode(), 1000,
				big.NewInt(100), big.NewInt(1_000_000),
			)
			var wantErr error
			if shortage == "native value" {
				f.db.SetBalance(f.owner, new(big.Int))
				wantErr = ErrInsufficientFundsForTransfer
			} else {
				f.db.SetState(params.GAploContractAddress, sessionkeys.GaploSlot(f.owner), common.Hash{})
				wantErr = ErrInsufficientFunds
			}
			tx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(1), 100_000, independentSessionSelector[:])
			ownerNative := new(big.Int).Set(f.db.GetBalance(f.owner))
			ownerGAplo := sessionkeys.GaploBalance(f.db, f.owner)
			before := sessionkeys.Get(f.db, f.session)
			if _, err := f.apply(t, tx); !errors.Is(err, wantErr) {
				t.Fatalf("missing %s error=%v, want %v", shortage, err, wantErr)
			}
			current := sessionkeys.Get(f.db, f.session)
			if f.db.GetBalance(f.owner).Cmp(ownerNative) != 0 ||
				sessionkeys.GaploBalance(f.db, f.owner).Cmp(ownerGAplo) != 0 ||
				f.db.GetNonce(f.session) != 0 {
				t.Fatal("rejected underfunded transaction changed native/GAplo balances or session nonce")
			}
			if current == nil || current.AploSpent.Cmp(before.AploSpent) != 0 ||
				current.GAploSpent.Cmp(before.GAploSpent) != 0 {
				t.Fatal("rejected underfunded transaction changed session allowances")
			}
		})
	}
}

func TestSessionKeysNativeModePreservesRegularEOAIdentityIndependent(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	configCopy := *params.TestChainConfig
	config := &configCopy
	db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	target := common.HexToAddress("0x9000")
	coinbase := common.HexToAddress("0x9001")
	db.SetBalance(from, big.NewInt(1000))
	db.SetNonce(from, 4)
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetNonce(sessionkeys.Address, 1)
	db.SetState(
		params.GAploContractAddress, sessionkeys.GaploSlot(from),
		common.BigToHash(big.NewInt(1_000_000_000)),
	)
	db.SetState(
		params.GAploContractAddress, common.HexToHash("0x2"),
		common.BigToHash(big.NewInt(1_000_000_000)),
	)
	db.SetCode(target, independentCallerOriginCode())
	header := &types.Header{
		Number: big.NewInt(1), GasLimit: 1_000_000, Time: 1,
		Difficulty: big.NewInt(1), BaseFee: big.NewInt(0), Coinbase: coinbase,
	}
	tx, err := types.SignTx(
		types.NewTransaction(4, target, big.NewInt(1), 120_000, big.NewInt(1), nil),
		types.MakeSigner(config, header.Number), key,
	)
	if err != nil {
		t.Fatal(err)
	}
	var usedGas uint64
	db.Prepare(tx.Hash(), 0)
	receipt, err := ApplyTransaction(
		config, nil, &coinbase, new(GasPool).AddGas(header.GasLimit),
		db, header, tx, &usedGas, vm.Config{}, nil,
	)
	if err != nil {
		t.Fatalf("regular EOA transaction changed in native mode: %v", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("regular EOA receipt status=%d", receipt.Status)
	}
	if got := common.BytesToAddress(db.GetState(target, common.Hash{}).Bytes()); got != from {
		t.Fatalf("regular EOA tx.origin=%s, want %s", got, from)
	}
	if got := common.BytesToAddress(db.GetState(target, common.BigToHash(big.NewInt(1))).Bytes()); got != from {
		t.Fatalf("regular EOA msg.sender=%s, want %s", got, from)
	}
}

func TestSessionKeysSelectorRemainsExactIndependent(t *testing.T) {
	f := newIndependentSessionFixture(
		t, independentCallerOriginCode(), 1000, big.NewInt(100), big.NewInt(1_000_000),
	)
	data := append([]byte(nil), independentSessionSelector[:]...)
	data = append(data, 0x00, 0x01)
	data[3] ^= 0x01
	tx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(0), 100_000, data)
	if _, err := f.apply(t, tx); err == nil {
		t.Fatal("selector prefix with one changed byte was accepted")
	}
}

func TestSessionKeysNativeBuiltinTransferUsesOwnerAllowanceIndependent(t *testing.T) {
	f := newIndependentSessionFixture(t, nil, 1000, big.NewInt(100), big.NewInt(1_000_000))
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	session := crypto.PubkeyToAddress(key.PublicKey)
	if err := sessionkeys.Create(
		f.db, f.owner, session, params.AploContractAddress,
		[][4]byte{{0xa9, 0x05, 0x9c, 0xbb}},
		big.NewInt(13), big.NewInt(1_000_000), 1000, 1,
	); err != nil {
		t.Fatalf("create native-transfer session: %v", err)
	}
	recipient := common.HexToAddress("0x9010")
	data := append([]byte{0xa9, 0x05, 0x9c, 0xbb}, common.LeftPadBytes(recipient.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(big.NewInt(13).Bytes(), 32)...)
	tx := f.signedTx(t, key, 0, params.AploContractAddress, new(big.Int), 150_000, data)
	receipt, err := f.apply(t, tx)
	if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("native transfer transaction failed: receipt=%v err=%v", receipt, err)
	}
	if got := f.db.GetBalance(recipient); got.Cmp(big.NewInt(13)) != 0 {
		t.Fatalf(
			"recipient received native APLO %s, want 13 (owner=%s session=%s status=%d logs=%d)",
			got, f.db.GetBalance(f.owner), f.db.GetBalance(session), receipt.Status, len(receipt.Logs),
		)
	}
	if got := f.db.GetBalance(f.owner); got.Cmp(big.NewInt(987)) != 0 {
		t.Fatalf("owner paid transfer from balance %s, want 987", got)
	}
	if got := sessionkeys.Get(f.db, session); got == nil ||
		got.AploSpent.Sign() != 0 ||
		got.GAploSpent.Cmp(new(big.Int).Sub(big.NewInt(1_000_000), new(big.Int).SetUint64(receipt.GasUsed))) != 0 {
		t.Fatalf("native transfer allowance accounting is wrong: %#v", got)
	}
}

func TestSessionKeysIncomingNativeTransfersRedirectThroughCallAndSelfDestructIndependent(t *testing.T) {
	for _, route := range []string{"internal call", "selfdestruct"} {
		t.Run(route, func(t *testing.T) {
			f := newIndependentSessionFixture(
				t, nil, 1000, big.NewInt(100), big.NewInt(1_000_000),
			)
			source := common.HexToAddress("0x9011")
			const amount = int64(17)
			if route == "internal call" {
				f.db.SetCode(source, independentCallWithValueCode(f.session, uint64(amount)))
			} else {
				f.db.SetCode(
					source, append([]byte{0x73}, append(f.session.Bytes(), 0xff)...),
				) // PUSH20 session; SELFDESTRUCT
			}
			f.db.SetBalance(source, big.NewInt(amount))
			ownerBefore := new(big.Int).Set(f.db.GetBalance(f.owner))
			tx := f.signedTx(t, f.ownerKey, f.db.GetNonce(f.owner), source, new(big.Int), 250_000, nil)
			receipt, err := f.apply(t, tx)
			if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
				t.Fatalf("%s transaction failed: receipt=%v err=%v", route, receipt, err)
			}
			wantOwner := new(big.Int).Add(ownerBefore, big.NewInt(amount))
			if got := f.db.GetBalance(f.owner); got.Cmp(wantOwner) != 0 {
				t.Fatalf("owner received %s APLO, want %s", got, wantOwner)
			}
			if f.db.GetBalance(f.session).Sign() != 0 || f.db.GetBalance(source).Sign() != 0 {
				t.Fatalf(
					"redirect route left APLO on session/source: session=%s source=%s",
					f.db.GetBalance(f.session), f.db.GetBalance(source),
				)
			}
			if !sessionkeys.Used(f.db, f.session) {
				t.Fatal("redirect test lost the permanent session-key marker")
			}
		})
	}
}

func TestSessionKeysCanonicalGAploTransferRedirectsSessionRecipientIndependent(t *testing.T) {
	f := newIndependentSessionFixture(
		t, nil, 1000, big.NewInt(100), big.NewInt(1_000_000),
	)
	fromKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := crypto.PubkeyToAddress(fromKey.PublicKey)
	const fundedGAplo = int64(1_000_000)
	const transferAmount = int64(321)
	f.db.SetState(
		params.GAploContractAddress, sessionkeys.GaploSlot(from),
		common.BigToHash(big.NewInt(fundedGAplo)),
	)
	f.db.SetState(
		params.GAploContractAddress, common.HexToHash("0x2"),
		common.BigToHash(big.NewInt(1_000_000_000+fundedGAplo)),
	)
	// Exercise the canonical GAplo contract's public transfer path.
	transferSelector := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]
	data := append([]byte(nil), transferSelector...)
	data = append(data, common.LeftPadBytes(f.session.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(big.NewInt(transferAmount).Bytes(), 32)...)
	tx := f.signedTx(t, fromKey, 0, params.GAploContractAddress, new(big.Int), 150_000, data)
	receipt, err := f.apply(t, tx)
	if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("canonical GAplo transfer failed: receipt=%v err=%v", receipt, err)
	}
	if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(
		new(big.Int).Add(big.NewInt(1_000_000_000), big.NewInt(transferAmount)),
	) != 0 {
		t.Fatalf("owner GAplo balance after redirected credit=%s", got)
	}
	if got := sessionkeys.GaploBalance(f.db, f.session); got.Sign() != 0 {
		t.Fatalf("GAplo credited session address instead of owner: %s", got)
	}
	wantSender := new(big.Int).Sub(
		big.NewInt(fundedGAplo-transferAmount),
		new(big.Int).SetUint64(receipt.GasUsed),
	)
	if got := sessionkeys.GaploBalance(f.db, from); got.Cmp(wantSender) != 0 {
		t.Fatalf("sender GAplo balance=%s, want %s after transfer and fee", got, wantSender)
	}
}

func TestSessionKeysMalformedCanonicalGAploRecipientWordsAreRejectedIndependent(t *testing.T) {
	const transferAmount = int64(321)
	amount := big.NewInt(transferAmount)
	malformedWord := func(recipient common.Address) []byte {
		word := common.LeftPadBytes(recipient.Bytes(), 32)
		word[0] = 1 // The high 12 bytes make this a non-canonical ABI address.
		return word
	}
	call := func(f *independentSessionFixture, caller common.Address, input []byte) error {
		return independentEVMCall(f, f.config, caller, params.GAploContractAddress, input)
	}
	word := func(address common.Address) []byte {
		return common.LeftPadBytes(address.Bytes(), 32)
	}
	uintWord := func(value *big.Int) []byte {
		return common.LeftPadBytes(value.Bytes(), 32)
	}
	input := func(signature string, words ...[]byte) []byte {
		data := append([]byte(nil), crypto.Keccak256([]byte(signature))[:4]...)
		for _, arg := range words {
			data = append(data, arg...)
		}
		return data
	}
	addToSupply := func(f *independentSessionFixture, amount *big.Int) {
		totalSupply := new(big.Int).Add(
			sessionkeys.GaploBalance(f.db, common.Address{}), amount,
		)
		f.db.SetState(
			params.GAploContractAddress, common.HexToHash("0x2"),
			common.BigToHash(totalSupply),
		)
	}

	t.Run("transfer", func(t *testing.T) {
		f := newIndependentSessionFixture(
			t, nil, 1000, big.NewInt(100), big.NewInt(1_000_000),
		)
		fromKey, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		from := crypto.PubkeyToAddress(fromKey.PublicKey)
		const initialBalance = int64(10_000)
		f.db.SetState(
			params.GAploContractAddress, sessionkeys.GaploSlot(from),
			common.BigToHash(big.NewInt(initialBalance)),
		)
		addToSupply(f, big.NewInt(initialBalance))
		before := sessionkeys.GaploBalance(f.db, f.owner)
		transfer := func(recipient []byte) []byte {
			return input("transfer(address,uint256)", recipient, uintWord(amount))
		}
		if err := call(f, from, transfer(word(f.session))); err != nil {
			t.Fatalf("well-formed transfer control failed: %v", err)
		}
		wantOwner := new(big.Int).Add(before, amount)
		if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(wantOwner) != 0 {
			t.Fatalf("well-formed control credited owner with %s, want %s", got, wantOwner)
		}
		rootBefore := f.db.Copy().IntermediateRoot(false)
		if err := call(f, from, transfer(malformedWord(f.session))); !errors.Is(err, vm.ErrExecutionReverted) {
			t.Fatalf("malformed transfer error=%v, want ABI revert", err)
		}
		if got := f.db.Copy().IntermediateRoot(false); got != rootBefore {
			t.Fatalf("malformed transfer mutated state: before=%s after=%s", rootBefore, got)
		}
		if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(wantOwner) != 0 {
			t.Fatalf("malformed transfer credited owner again: %s", got)
		}
		if got := sessionkeys.GaploBalance(f.db, f.session); got.Sign() != 0 {
			t.Fatalf("malformed transfer credited session key: %s", got)
		}
	})

	t.Run("transferFrom", func(t *testing.T) {
		f := newIndependentSessionFixture(
			t, nil, 1000, big.NewInt(100), big.NewInt(1_000_000),
		)
		from := common.HexToAddress("0x7001")
		spender := common.HexToAddress("0x7002")
		const initialBalance = int64(10_000)
		f.db.SetState(
			params.GAploContractAddress, sessionkeys.GaploSlot(from),
			common.BigToHash(big.NewInt(initialBalance)),
		)
		addToSupply(f, big.NewInt(initialBalance))
		allowance := new(big.Int).Mul(amount, big.NewInt(2))
		if err := call(
			f, from,
			input("approve(address,uint256)", word(spender), uintWord(allowance)),
		); err != nil {
			t.Fatalf("allowance setup failed: %v", err)
		}
		before := sessionkeys.GaploBalance(f.db, f.owner)
		transferFrom := func(recipient []byte) []byte {
			return input(
				"transferFrom(address,address,uint256)",
				word(from), recipient, uintWord(amount),
			)
		}
		if err := call(f, spender, transferFrom(word(f.session))); err != nil {
			t.Fatalf("well-formed transferFrom control failed: %v", err)
		}
		wantOwner := new(big.Int).Add(before, amount)
		if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(wantOwner) != 0 {
			t.Fatalf("well-formed control credited owner with %s, want %s", got, wantOwner)
		}
		rootBefore := f.db.Copy().IntermediateRoot(false)
		if err := call(f, spender, transferFrom(malformedWord(f.session))); !errors.Is(err, vm.ErrExecutionReverted) {
			t.Fatalf("malformed transferFrom error=%v, want ABI revert", err)
		}
		if got := f.db.Copy().IntermediateRoot(false); got != rootBefore {
			t.Fatalf("malformed transferFrom mutated state: before=%s after=%s", rootBefore, got)
		}
		if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(wantOwner) != 0 {
			t.Fatalf("malformed transferFrom credited owner again: %s", got)
		}
		if got := sessionkeys.GaploBalance(f.db, f.session); got.Sign() != 0 {
			t.Fatalf("malformed transferFrom credited session key: %s", got)
		}
	})

	t.Run("refund", func(t *testing.T) {
		f := newIndependentSessionFixture(
			t, nil, 1000, big.NewInt(100), big.NewInt(1_000_000),
		)
		root := common.Address{}
		before := sessionkeys.GaploBalance(f.db, f.owner)
		refund := func(recipient []byte) []byte {
			return input("refund(address,uint256)", recipient, uintWord(amount))
		}
		if err := call(f, root, refund(word(f.session))); err != nil {
			t.Fatalf("root refund control failed: %v", err)
		}
		wantOwner := new(big.Int).Add(before, amount)
		if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(wantOwner) != 0 {
			t.Fatalf("well-formed refund credited owner with %s, want %s", got, wantOwner)
		}
		rootBefore := f.db.Copy().IntermediateRoot(false)
		if err := call(f, root, refund(malformedWord(f.session))); !errors.Is(err, vm.ErrExecutionReverted) {
			t.Fatalf("malformed refund error=%v, want ABI revert", err)
		}
		if got := f.db.Copy().IntermediateRoot(false); got != rootBefore {
			t.Fatalf("malformed refund mutated state: before=%s after=%s", rootBefore, got)
		}
		if got := sessionkeys.GaploBalance(f.db, f.owner); got.Cmp(wantOwner) != 0 {
			t.Fatalf("malformed refund credited owner again: %s", got)
		}
		if got := sessionkeys.GaploBalance(f.db, f.session); got.Sign() != 0 {
			t.Fatalf("malformed refund credited session key: %s", got)
		}
	})
}

type independentSessionProcessorFixture struct {
	config     *params.ChainConfig
	chain      *BlockChain
	state      *state.StateDB
	genesis    *types.Block
	ownerKey   *ecdsa.PrivateKey
	sessionKey *ecdsa.PrivateKey
	owner      common.Address
	session    common.Address
	target     common.Address
	coinbase   common.Address
}

// newIndependentSessionProcessorFixture creates a real blockchain and StateDB
// for import, mining-finalizer, reorg, and reopen integration checks.
func newIndependentSessionProcessorFixture(t *testing.T) *independentSessionProcessorFixture {
	t.Helper()
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	session := crypto.PubkeyToAddress(sessionKey.PublicKey)
	target := common.HexToAddress("0x9020")
	coinbase := common.HexToAddress("0x9021")
	configCopy := *params.TestChainConfig
	config := &configCopy
	// Seed both owner funds and total supply so the canonical GAplo code can burn fees.
	gaStorage := map[common.Hash]common.Hash{
		sessionkeys.GaploSlot(owner): common.BigToHash(big.NewInt(1_000_000_000)),
		common.HexToHash("0x2"):      common.BigToHash(big.NewInt(1_000_000_000)),
	}
	gspec := &Genesis{
		Config: config, GasLimit: 8_000_000, Difficulty: big.NewInt(1), Timestamp: 1,
		Alloc: GenesisAlloc{
			owner: {Balance: big.NewInt(1000)},
			params.GAploContractAddress: {
				Balance: new(big.Int),
				Code:    common.FromHex(params.GAPLO),
				Storage: gaStorage,
			},
			target: {Balance: new(big.Int), Code: independentCallerOriginCode()},
		},
	}
	db := rawdb.NewMemoryDatabase()
	genesis := gspec.MustCommit(db)
	chain, err := NewBlockChain(db, nil, config, ethash.NewFaker(), vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("create processor chain: %v", err)
	}
	t.Cleanup(chain.Stop)
	statedb, err := state.New(genesis.Root(), chain.stateCache, nil)
	if err != nil {
		t.Fatalf("load processor genesis state: %v", err)
	}
	if err := sessionkeys.ValidateState(statedb); err != nil {
		t.Fatalf("genesis omitted native Session Keys accounts: %v", err)
	}
	return &independentSessionProcessorFixture{
		config: config, chain: chain, state: statedb, genesis: genesis,
		ownerKey: ownerKey, sessionKey: sessionKey,
		owner: owner, session: session, target: target, coinbase: coinbase,
	}
}

func (f *independentSessionProcessorFixture) signedTx(
	t *testing.T,
	key *ecdsa.PrivateKey,
	nonce uint64,
	to common.Address,
	value *big.Int,
	gas uint64,
	data []byte,
	number uint64,
) *types.Transaction {
	t.Helper()
	unsigned := types.NewTransaction(nonce, to, value, gas, big.NewInt(1), data)
	tx, err := types.SignTx(unsigned, types.MakeSigner(f.config, new(big.Int).SetUint64(number)), key)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func (f *independentSessionProcessorFixture) process(
	t *testing.T,
	number uint64,
	txs ...*types.Transaction,
) (types.Receipts, uint64) {
	t.Helper()
	parent := f.genesis
	if number > 1 {
		parent = types.NewBlock(
			&types.Header{
				ParentHash: f.genesis.Hash(), Number: big.NewInt(1),
				GasLimit: 8_000_000, Time: 2, Difficulty: big.NewInt(1),
			},
			nil, nil, nil, trie.NewStackTrie(nil),
		)
	}
	header := &types.Header{
		ParentHash: parent.Hash(), Number: new(big.Int).SetUint64(number),
		GasLimit: 8_000_000, Time: parent.Time() + 1,
		Difficulty: big.NewInt(1), Coinbase: f.coinbase, BaseFee: big.NewInt(0),
	}
	block := types.NewBlock(header, txs, nil, nil, trie.NewStackTrie(nil))
	receipts, _, usedGas, err := f.chain.processor.Process(block, f.state, vm.Config{})
	if err != nil {
		t.Fatalf("StateProcessor.Process block %d: %v", number, err)
	}
	return receipts, usedGas
}

func independentProcessorHeader(f *independentSessionProcessorFixture) *types.Header {
	return &types.Header{
		ParentHash: f.genesis.Hash(), Number: big.NewInt(1), GasLimit: 8_000_000,
		Time: f.genesis.Time() + 1, Difficulty: big.NewInt(1), Coinbase: f.coinbase,
		BaseFee: big.NewInt(0),
	}
}

// independentSeedExpirySession prepares an expiry bucket for both processing paths.
func independentSeedExpirySession(t *testing.T, f *independentSessionProcessorFixture, db *state.StateDB) {
	t.Helper()
	if err := sessionkeys.Create(
		db, f.owner, f.session, f.target, [][4]byte{independentSessionSelector},
		big.NewInt(100), big.NewInt(1_000_000), 1, 0,
	); err != nil {
		t.Fatalf("seed session expiring at block 1: %v", err)
	}
}

func TestSessionKeysMinerFinalizeAndAssembleMatchesImportProcessIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	baseState, err := state.New(f.genesis.Root(), f.chain.stateCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	independentSeedExpirySession(t, f, baseState)

	selectorData := append(independentSessionSelector[:], 0x01)
	tx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(11), 120_000, selectorData, 1)
	txs := []*types.Transaction{tx}

	// Import path: StateProcessor.Process executes the transaction and invokes
	// Engine.Finalize, which performs end-of-block expiry cleanup.
	processState := baseState.Copy()
	processBlock := types.NewBlock(independentProcessorHeader(f), txs, nil, nil, trie.NewStackTrie(nil))
	processReceipts, _, _, err := f.chain.processor.Process(processBlock, processState, vm.Config{})
	if err != nil {
		t.Fatalf("import path StateProcessor.Process: %v", err)
	}
	if len(processReceipts) != 1 ||
		processReceipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("inclusive-expiry transaction did not execute on import path: %#v", processReceipts)
	}
	processRoot := processState.IntermediateRoot(true)

	// Miner path: run the same transaction against the same base root, then use
	// the real engine FinalizeAndAssemble hook instead of StateProcessor.Process.
	minerState := baseState.Copy()
	minerHeader := independentProcessorHeader(f)
	gasPool := new(GasPool).AddGas(minerHeader.GasLimit)
	var minerUsedGas uint64
	var minerReceipts types.Receipts
	for i, included := range txs {
		minerState.Prepare(included.Hash(), i)
		receipt, applyErr := ApplyTransaction(
			f.config, nil, &f.coinbase, gasPool, minerState, minerHeader,
			included, &minerUsedGas, vm.Config{}, nil,
		)
		if applyErr != nil {
			t.Fatalf("miner path ApplyTransaction: %v", applyErr)
		}
		minerReceipts = append(minerReceipts, receipt)
	}
	assembled, err := ethash.NewFaker().FinalizeAndAssemble(
		f.chain, minerHeader, minerState, txs, nil, minerReceipts,
	)
	if err != nil {
		t.Fatalf("miner path FinalizeAndAssemble: %v", err)
	}
	minerRoot := minerState.IntermediateRoot(true)
	if assembled.Root() != processRoot || minerRoot != processRoot {
		t.Fatalf(
			"miner/import state-root mismatch: assembled=%s minerState=%s imported=%s",
			assembled.Root(), minerRoot, processRoot,
		)
	}
	for path, db := range map[string]*state.StateDB{"import": processState, "miner": minerState} {
		if sessionkeys.Get(db, f.session) != nil ||
			!sessionkeys.Used(db, f.session) ||
			sessionkeys.Recipient(db, f.session) != f.owner {
			t.Fatalf("%s finalization did not clean expiry while retaining redirect tombstone", path)
		}
		if db.GetNonce(f.session) != 1 || db.GetBalance(f.target).Cmp(big.NewInt(11)) != 0 {
			t.Fatalf(
				"%s path transaction result mismatch: session nonce=%d target APLO=%s",
				path, db.GetNonce(f.session), db.GetBalance(f.target),
			)
		}
		if got := db.GetState(
			sessionkeys.Address, sessionkeys.Slot("ownerCount", f.owner, 0),
		).Big().Uint64(); got != 0 {
			t.Fatalf("%s finalization left owner index count %d", path, got)
		}
		if got := db.GetState(
			sessionkeys.Address,
			sessionkeys.Slot("bucketCount", common.BigToAddress(big.NewInt(1)), 0),
		).Big().Uint64(); got != 0 {
			t.Fatalf("%s finalization left expiry bucket count %d", path, got)
		}
	}
}

func independentCallWithValueCode(to common.Address, amount uint64) []byte {
	code := []byte{0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, byte(amount), 0x73}
	code = append(code, to.Bytes()...)
	return append(code, 0x5a, 0xf1, 0x00) // GAS; CALL; STOP
}

func independentCreateContextCode() []byte {
	// The constructor records ORIGIN and CALLER; the creator stores CREATE's address.
	initCode := []byte{
		0x32, 0x60, 0x00, 0x55, 0x33, 0x60, 0x01, 0x55,
		0x60, 0x01, 0x60, 0x14, 0x60, 0x00, 0x39,
		0x60, 0x01, 0x60, 0x00, 0xf3, 0x00,
	}
	code := []byte{
		0x60, byte(len(initCode)), 0x60, 0x00, 0x60, 0x00, 0x39,
		0x60, byte(len(initCode)), 0x60, 0x00, 0x60, 0x00, 0xf0,
		0x60, 0x02, 0x55, 0x00,
	}
	code[3] = byte(len(code)) // CODECOPY source offset, immediately after the creator runtime.
	return append(code, initCode...)
}

func TestSessionKeysCreateContextKeepsOwnerOriginAndContractCallerIndependent(t *testing.T) {
	f := newIndependentSessionFixture(
		t, independentCreateContextCode(), 1000, big.NewInt(100), big.NewInt(1_000_000),
	)
	tx := f.signedTx(t, f.sessionKey, 0, f.target, new(big.Int), 300_000, independentSessionSelector[:])
	receipt, err := f.apply(t, tx)
	if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("session transaction with CREATE failed: receipt=%v err=%v", receipt, err)
	}
	created := common.BytesToAddress(f.db.GetState(f.target, common.BigToHash(big.NewInt(2))).Bytes())
	if created == (common.Address{}) {
		t.Fatal("creator contract did not store the CREATE result")
	}
	if got := f.db.GetState(created, common.Hash{}); common.BytesToAddress(got.Bytes()) != f.owner {
		t.Fatalf("constructor observed tx.origin %s, want owner %s", common.BytesToAddress(got.Bytes()), f.owner)
	}
	if got := f.db.GetState(
		created, common.BigToHash(big.NewInt(1)),
	); common.BytesToAddress(got.Bytes()) != f.target {
		t.Fatalf(
			"constructor observed msg.sender %s, want creator contract %s",
			common.BytesToAddress(got.Bytes()), f.target,
		)
	}
	if code := f.db.GetCode(created); len(code) != 1 || code[0] != 0x00 {
		t.Fatalf("CREATE returned unexpected runtime code: %x", code)
	}
}

func independentRegistryCallerCode(input []byte) []byte {
	// Copy calldata into memory, forward it with CALL, and store the success bit.
	code := []byte{
		0x61, byte(len(input) >> 8), byte(len(input)), 0x61, 0, 0,
		0x60, 0x00, 0x39,
	}
	code = append(code, 0x60, 0, 0x51, 0x60, 1, 0x55) // Save the copied ABI selector word at slot 1.
	code = append(
		code, 0x60, 0, 0x60, 0, 0x61, byte(len(input)>>8), byte(len(input)),
		0x60, 0, 0x60, 0, 0x73,
	)
	code = append(code, sessionkeys.Address.Bytes()...)
	code = append(
		code, 0x62, 0x0c, 0x35, 0x00, 0xf1, 0x60, 0, 0x55, 0x00,
	) // CALL; store success at slot 0; STOP
	copyOffset := 4 // byte position of the PUSH2 code offset immediate.
	dataOffset := len(code)
	code[copyOffset] = byte(dataOffset >> 8)
	code[copyOffset+1] = byte(dataOffset)
	return append(code, input...)
}

func TestSessionKeysRegistryRejectsTxOriginOnlyCreateAndRevokeIndependent(t *testing.T) {
	for _, operation := range []string{"create", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			f := newIndependentSessionFixture(t, nil, 1000, big.NewInt(100), big.NewInt(1_000_000))
			var input []byte
			var key common.Address
			if operation == "create" {
				keyKey, err := crypto.GenerateKey()
				if err != nil {
					t.Fatal(err)
				}
				key = crypto.PubkeyToAddress(keyKey.PublicKey)
				selectors := [][4]byte{independentSessionSelector}
				aplo, gaplo, expiry := big.NewInt(10), big.NewInt(20), big.NewInt(1000)
				proof := independentSessionProof(
					t, f.owner, key, f.target, keyKey, selectors,
					aplo, gaplo, expiry, f.config.ChainID,
				)
				input, err = sessionkeys.ABI.Pack(
					"CreateSessionKey", key, f.target, selectors,
					aplo, gaplo, expiry, proof,
				)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				key = f.session
				input, _ = sessionkeys.ABI.Pack("RevokeSessionKey", key)
			}
			contract := common.HexToAddress("0x9024")
			f.db.SetCode(contract, independentRegistryCallerCode(input))
			tx := f.signedTx(t, f.ownerKey, f.db.GetNonce(f.owner), contract, new(big.Int), 1_000_000, nil)
			receipt, err := f.apply(t, tx)
			if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
				t.Fatalf(
					"origin-only %s attempt did not complete its wrapper call: receipt=%v err=%v",
					operation, receipt, err,
				)
			}
			if got, want :=
				f.db.GetState(contract, common.BigToHash(big.NewInt(1))),
				common.BytesToHash(input[:32]); got != want {
				t.Fatalf("wrapper calldata copy mismatch: got %s want %s", got, want)
			}
			if f.db.GetState(contract, common.Hash{}).Big().Sign() != 0 {
				t.Fatalf(
					"registry CALL returned success for contract-mediated %s (key active=%t used=%t owner=%s origin=%s input-selector=%x registry=%s caller-code=%x)",
					operation, sessionkeys.Get(f.db, key) != nil,
					sessionkeys.Used(f.db, key), sessionkeys.Owner(f.db, key), f.owner,
					input[:4], sessionkeys.Address, f.db.GetCode(contract),
				)
			}
			if operation == "create" && (sessionkeys.Get(f.db, key) != nil || sessionkeys.Used(f.db, key)) {
				t.Fatal("origin-only contract call created or reserved a session key")
			}
			if operation == "revoke" && sessionkeys.Get(f.db, key) == nil {
				t.Fatal("origin-only contract call revoked an owner session")
			}
		})
	}
}

func TestSessionKeysStateProcessorSameBlockCreateUseRevokeIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	selector := [4]byte{0x70, 0x11, 0x22, 0x33}
	selectors := [][4]byte{selector}
	aplo, gaplo, expiry := big.NewInt(100), big.NewInt(1_000_000), big.NewInt(1)
	proof := independentSessionProof(
		t, f.owner, f.session, f.target, f.sessionKey,
		selectors, aplo, gaplo, expiry, f.config.ChainID,
	)
	create, err := sessionkeys.ABI.Pack(
		"CreateSessionKey", f.session, f.target, selectors, aplo, gaplo, expiry, proof,
	)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := sessionkeys.ABI.Pack("RevokeSessionKey", f.session)
	if err != nil {
		t.Fatal(err)
	}
	createTx := f.signedTx(t, f.ownerKey, 0, sessionkeys.Address, new(big.Int), 700_000, create, 1)
	useTx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(11), 120_000, append(selector[:], 0x01), 1)
	revokeTx := f.signedTx(t, f.ownerKey, 1, sessionkeys.Address, new(big.Int), 500_000, revoke, 1)
	receipts, usedGas := f.process(t, 1, createTx, useTx, revokeTx)
	if len(receipts) != 3 || usedGas == 0 {
		t.Fatalf("processor returned %d receipts and %d gas", len(receipts), usedGas)
	}
	for i, receipt := range receipts {
		if receipt.Status != types.ReceiptStatusSuccessful {
			t.Fatalf("transaction %d failed in same-block create/use/revoke: status=%d", i, receipt.Status)
		}
	}
	if got := common.BytesToAddress(f.state.GetState(f.target, common.Hash{}).Bytes()); got != f.owner {
		t.Fatalf("same-block session call origin=%s, want owner %s", got, f.owner)
	}
	if got := common.BytesToAddress(
		f.state.GetState(f.target, common.BigToHash(big.NewInt(1))).Bytes(),
	); got != f.session {
		t.Fatalf("same-block session call sender=%s, want session %s", got, f.session)
	}
	if sessionkeys.Get(f.state, f.session) != nil || !sessionkeys.Used(f.state, f.session) {
		t.Fatal("owner revoke did not clear configuration while retaining the no-reuse marker")
	}
	if f.state.GetNonce(f.owner) != 2 || f.state.GetNonce(f.session) != 1 {
		t.Fatalf(
			"same-block nonce ownership owner=%d session=%d, want 2/1",
			f.state.GetNonce(f.owner), f.state.GetNonce(f.session),
		)
	}
}

func TestSessionKeysStateProcessorInclusiveExpiryCleanupPreservesRedirectIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	if err := sessionkeys.Create(
		f.state, f.owner, f.session, f.target, [][4]byte{independentSessionSelector},
		big.NewInt(100), big.NewInt(1_000_000), 1, 0,
	); err != nil {
		t.Fatalf("seed expiring session: %v", err)
	}
	useTx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(11), 120_000, independentSessionSelector[:], 1)
	receipts, _ := f.process(t, 1, useTx)
	if len(receipts) != 1 || receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("session was not usable through its expiry block: receipts=%v", receipts)
	}
	if sessionkeys.Get(f.state, f.session) != nil || !sessionkeys.Used(f.state, f.session) {
		t.Fatal("consensus finalization did not clean the exact expiry bucket or lost the tombstone")
	}

	source := common.HexToAddress("0x9022")
	f.state.SetCode(source, independentCallWithValueCode(f.session, 33))
	f.state.SetBalance(source, big.NewInt(33))
	ownerBefore := new(big.Int).Set(f.state.GetBalance(f.owner))
	redirectTx := f.signedTx(t, f.ownerKey, f.state.GetNonce(f.owner), source, new(big.Int), 150_000, nil, 2)
	redirectReceipts, _ := f.process(t, 2, redirectTx)
	if len(redirectReceipts) != 1 || redirectReceipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("post-expiry incoming CALL failed: receipts=%v", redirectReceipts)
	}
	if got := f.state.GetBalance(f.owner); got.Cmp(
		new(big.Int).Add(ownerBefore, big.NewInt(33)),
	) != 0 {
		t.Fatalf(
			"post-expiry redirected APLO owner balance=%s, want %s",
			got, new(big.Int).Add(ownerBefore, big.NewInt(33)),
		)
	}
	if f.state.GetBalance(f.session).Sign() != 0 || f.state.GetBalance(source).Sign() != 0 {
		t.Fatalf(
			"post-expiry redirect left value on session/source: %s/%s",
			f.state.GetBalance(f.session), f.state.GetBalance(source),
		)
	}
}

func TestSessionKeysFinalizeAndAssembleRootMatchesProcessorAtExpiryIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	if err := sessionkeys.Create(
		f.state, f.owner, f.session, f.target, [][4]byte{independentSessionSelector},
		big.NewInt(100), big.NewInt(1_000_000), 1, 0,
	); err != nil {
		t.Fatalf("seed expiring session: %v", err)
	}
	tx := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(7), 120_000, independentSessionSelector[:], 1)
	header := &types.Header{
		ParentHash: f.genesis.Hash(), Number: big.NewInt(1),
		GasLimit: 8_000_000, Time: f.genesis.Time() + 1,
		Difficulty: big.NewInt(1), Coinbase: f.coinbase, BaseFee: big.NewInt(0),
	}
	block := types.NewBlock(header, []*types.Transaction{tx}, nil, nil, trie.NewStackTrie(nil))

	processState := f.state.Copy()
	processReceipts, _, _, err := f.chain.processor.Process(block, processState, vm.Config{})
	if err != nil || len(processReceipts) != 1 ||
		processReceipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("processor path failed at expiry: receipts=%v err=%v", processReceipts, err)
	}

	assemblyState := f.state.Copy()
	assemblyHeader := block.Header()
	assemblyState.Prepare(tx.Hash(), 0)
	var usedGas uint64
	receipt, err := ApplyTransaction(
		f.config, f.chain, &f.coinbase, new(GasPool).AddGas(block.GasLimit()),
		assemblyState, assemblyHeader, tx, &usedGas, vm.Config{}, nil,
	)
	if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("pre-assembly transaction application failed: receipt=%v err=%v", receipt, err)
	}
	assembled, err := f.chain.engine.FinalizeAndAssemble(
		f.chain, assemblyHeader, assemblyState, []*types.Transaction{tx}, nil,
		types.Receipts{receipt},
	)
	if err != nil {
		t.Fatalf("FinalizeAndAssemble: %v", err)
	}
	processorRoot := processState.IntermediateRoot(f.config.IsEIP158(block.Number()))
	if got := assembled.Root(); got != processorRoot {
		t.Fatalf("mining assembly state root=%s, imported processor state root=%s", got, processorRoot)
	}
	for label, db := range map[string]*state.StateDB{"processor": processState, "assembly": assemblyState} {
		if sessionkeys.Get(db, f.session) != nil || !sessionkeys.Used(db, f.session) {
			t.Fatalf("%s path did not clean the inclusive expiry bucket while keeping the tombstone", label)
		}
		if got := db.GetState(
			sessionkeys.Address,
			sessionkeys.Slot("bucketCount", common.BigToAddress(big.NewInt(1)), 0),
		).Big().Uint64(); got != 0 {
			t.Fatalf("%s path left expiry bucket count %d", label, got)
		}
	}
}

func TestSessionKeysStateProcessorCommitReopenAndBranchIsolationIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	if err := sessionkeys.Create(
		f.state, f.owner, f.session, f.target, [][4]byte{independentSessionSelector},
		big.NewInt(100), big.NewInt(1_000_000), 2, 0,
	); err != nil {
		t.Fatalf("seed branch session: %v", err)
	}
	base := f.state.Copy()

	// Apply an authorized session use to one branch and a revocation to another.
	f.state = base.Copy()
	use := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(5), 120_000, independentSessionSelector[:], 1)
	receipts, _ := f.process(t, 1, use)
	if len(receipts) != 1 || receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("session-use branch failed: receipts=%v", receipts)
	}
	useBranch := f.state.Copy()

	f.state = base.Copy()
	revokeData, err := sessionkeys.ABI.Pack("RevokeSessionKey", f.session)
	if err != nil {
		t.Fatal(err)
	}
	revoke := f.signedTx(t, f.ownerKey, 0, sessionkeys.Address, new(big.Int), 500_000, revokeData, 1)
	receipts, _ = f.process(t, 1, revoke)
	if len(receipts) != 1 || receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("revoke branch failed: receipts=%v", receipts)
	}
	revokeBranch := f.state.Copy()

	if sessionkeys.Get(useBranch, f.session) == nil || sessionkeys.Get(revokeBranch, f.session) != nil {
		t.Fatal("alternative block branches shared session configuration state")
	}
	if useBranch.GetNonce(f.session) != 1 || revokeBranch.GetNonce(f.session) != 0 ||
		!sessionkeys.Used(revokeBranch, f.session) {
		t.Fatalf(
			"branch state mismatch: used-branch nonce=%d revoked-branch nonce=%d tombstone=%t",
			useBranch.GetNonce(f.session), revokeBranch.GetNonce(f.session),
			sessionkeys.Used(revokeBranch, f.session),
		)
	}

	commitAndReopen := func(db *state.StateDB) *state.StateDB {
		t.Helper()
		root, err := db.Commit(false)
		if err != nil {
			t.Fatalf("commit branch state: %v", err)
		}
		if err := db.Database().TrieDB().Commit(root, true, nil); err != nil {
			t.Fatalf("persist branch trie: %v", err)
		}
		reopened, err := state.New(root, db.Database(), nil)
		if err != nil {
			t.Fatalf("reopen branch state: %v", err)
		}
		return reopened
	}
	useRestart := commitAndReopen(useBranch)
	revokeRestart := commitAndReopen(revokeBranch)
	if sessionkeys.Get(useRestart, f.session) == nil ||
		sessionkeys.Get(revokeRestart, f.session) != nil ||
		!sessionkeys.Used(revokeRestart, f.session) {
		t.Fatal("session state or tombstone did not survive state commit and reopen")
	}
	if got := useRestart.GetState(
		sessionkeys.Address, sessionkeys.Slot("ownerCount", f.owner, 0),
	).Big().Uint64(); got != 1 {
		t.Fatalf("reopened active branch owner index count=%d, want 1", got)
	}
	if got := revokeRestart.GetState(
		sessionkeys.Address, sessionkeys.Slot("ownerCount", f.owner, 0),
	).Big().Uint64(); got != 0 {
		t.Fatalf("reopened revoked branch owner index count=%d, want 0", got)
	}
}
