package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// A canonical GAplo credit failure after a successful target call must revert
// across the ordinary fee-list reset. This fixture explicitly installs code at
// zero (the protocol credit authority); it is not a claimed reachable CREATE
// address. The code fills totalSupply through an authorized refund, leaving no
// room for the transition's subsequent unused-gas credit.
func TestOrdinaryCreditFailureRestoresStateAndWarmth(t *testing.T) {
	f := newIndependentSessionFixture(t, nil, 100, big.NewInt(100), big.NewInt(1000000))
	root := common.Address{}
	recipient := common.HexToAddress("0x9876")
	code := []byte{}
	store := func(offset byte, word []byte) {
		code = append(code, 0x7f)
		code = append(code, common.RightPadBytes(word, 32)...)
		code = append(code, 0x60, offset, 0x52)
	}
	store(0, crypto.Keccak256([]byte("totalSupply()"))[:4])
	code = append(code, 0x60, 32, 0x60, 0, 0x60, 4, 0x60, 0, 0x73)
	code = append(code, params.GAploContractAddress[:]...)
	code = append(code, 0x5a, 0xfa, 0x50, 0x60, 0, 0x51, 0x7f)
	code = append(code, common.LeftPadBytes(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).Bytes(), 32)...)
	code = append(code, 0x03, 0x60, 36, 0x52)
	store(0, crypto.Keccak256([]byte("refund(address,uint256)"))[:4])
	store(4, common.LeftPadBytes(recipient[:], 32))
	code = append(code, 0x60, 0, 0x60, 0, 0x60, 68, 0x60, 0, 0x60, 0, 0x73)
	code = append(code, params.GAploContractAddress[:]...)
	code = append(code, 0x5a, 0xf1, 0x15, 0x61, 0, 0, 0x57, 0x00)
	fail := len(code)
	code = append(code, 0x5b, 0x60, 0, 0x60, 0, 0xfd)
	code[fail-4] = byte(fail >> 8)
	code[fail-3] = byte(fail)
	f.db.SetCode(root, code)
	before := f.db.Copy().IntermediateRoot(false)
	beforeLogs := len(f.db.Logs())
	old := common.HexToAddress("0x4567")
	slot := common.HexToHash("0x89")
	f.db.AddSlotToAccessList(old, slot)
	outer := f.db.Snapshot()
	tx := f.signedTx(t, f.ownerKey, 7, root, new(big.Int), 500000, nil)
	f.db.Prepare(tx.Hash(), 0)
	msg, err := tx.AsMessage(types.MakeSigner(f.config, f.header.Number), f.header.BaseFee)
	if err != nil {
		t.Fatal(err)
	}
	evm := vm.NewEVM(NewEVMBlockContext(f.header, nil, &f.coinbase), NewEVMTxContext(msg), f.db, f.config, vm.Config{}, nil)
	gp := new(GasPool).AddGas(f.header.GasLimit)
	result, err := ApplyMessage(evm, msg, gp)
	if !errors.Is(err, vm.ErrExecutionReverted) || result != nil {
		t.Fatalf("expected final credit error, result=%+v err=%v", result, err)
	}
	if f.db.Copy().IntermediateRoot(false) != before || gp.Gas() != f.header.GasLimit || f.db.GetNonce(f.owner) != 7 || len(f.db.Logs()) != beforeLogs || f.db.GetRefund() != 0 {
		t.Fatalf("failed credit leaked state: root=%s want=%s gas=%d nonce=%d logs=%d refund=%d", f.db.Copy().IntermediateRoot(false), before, gp.Gas(), f.db.GetNonce(f.owner), len(f.db.Logs()), f.db.GetRefund())
	}
	if _, warm := f.db.SlotInAccessList(params.GAploContractAddress, common.HexToHash("0x2")); warm {
		t.Fatal("fee warmth survived rejection")
	}
	f.db.RevertToSnapshot(outer)
	if _, warm := f.db.SlotInAccessList(old, slot); !warm {
		t.Fatal("caller snapshot did not restore preceding access list")
	}
}

func TestOrdinaryReservationDoesNotCoupleSessionAssets(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(7), big.NewInt(100000))
	f.db.SetBalance(f.owner, big.NewInt(7))
	f.db.SetState(params.GAploContractAddress, crypto.Keccak256Hash(common.LeftPadBytes(f.owner[:], 32), make([]byte, 32)), common.BigToHash(big.NewInt(100000)))
	f.db.SetState(params.GAploContractAddress, common.HexToHash("0x2"), common.BigToHash(big.NewInt(100000)))
	before := f.db.Copy().IntermediateRoot(false)
	ordinary := f.signedTx(t, f.ownerKey, 7, f.target, big.NewInt(7), 100000, independentSessionSelector[:])
	if _, err := f.apply(t, ordinary); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("ordinary must additionally reserve value in GAplo: %v", err)
	}
	if f.db.Copy().IntermediateRoot(false) != before {
		t.Fatal("ordinary refusal changed state")
	}
	delegated := f.signedTx(t, f.sessionKey, 0, f.target, big.NewInt(7), 100000, independentSessionSelector[:])
	rec, err := f.apply(t, delegated)
	if err != nil || rec.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("session must fund value and fees separately: %v %+v", err, rec)
	}
	s := sessionkeys.Get(f.db, f.session)
	if s.AploSpent.Sign() != 0 || s.GAploSpent.Cmp(new(big.Int).Sub(big.NewInt(100000), new(big.Int).SetUint64(rec.GasUsed))) != 0 || f.db.GetBalance(f.owner).Sign() != 0 || f.db.GetBalance(f.target).Cmp(big.NewInt(7)) != 0 || f.db.GetNonce(f.owner) != 7 || f.db.GetNonce(f.session) != 1 {
		t.Fatal("delegated identities, independent assets or actual budgets changed")
	}
}

func TestOrdinaryZeroPriceCreditsRemainObservable(t *testing.T) {
	f := newIndependentSessionFixture(t, []byte{0}, 100, big.NewInt(100), big.NewInt(1000000))
	tx, err := types.SignTx(types.NewTransaction(7, f.target, new(big.Int), 100000, new(big.Int), nil), types.MakeSigner(f.config, f.header.Number), f.ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	beforeBalance := sessionkeys.GaploBalance(f.db, f.owner)
	beforeSupply := f.db.GetState(params.GAploContractAddress, common.HexToHash("0x2"))
	rec, err := f.apply(t, tx)
	if err != nil || rec.Status != types.ReceiptStatusSuccessful || rec.GasUsed != 21000 || len(rec.Logs) != 3 {
		t.Fatalf("ordinary zero-price receipt lost historical events: %v %+v", err, rec)
	}
	zero := common.Address{}
	from := []common.Address{f.owner, zero, zero}
	to := []common.Address{zero, f.owner, f.coinbase}
	for i, event := range rec.Logs {
		if event.Address != params.GAploContractAddress || len(event.Topics) != 3 || event.Topics[0] != crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")) || common.BytesToAddress(event.Topics[1][:]) != from[i] || common.BytesToAddress(event.Topics[2][:]) != to[i] || len(event.Data) != 32 || new(big.Int).SetBytes(event.Data).Sign() != 0 {
			t.Fatalf("wrong zero-fee event %d: %+v", i, event)
		}
	}
	if sessionkeys.GaploBalance(f.db, f.owner).Cmp(beforeBalance) != 0 || f.db.GetState(params.GAploContractAddress, common.HexToHash("0x2")) != beforeSupply || f.db.GetNonce(f.owner) != 8 || f.db.GetNonce(f.session) != 0 {
		t.Fatal("zero-price events changed balances, supply or signer lanes")
	}
}
