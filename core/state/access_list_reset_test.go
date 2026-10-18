package state

import (
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestPrepareAccessListResetRollback(t *testing.T) {
	db, err := New(common.Hash{}, NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	old := common.HexToAddress("0x1111")
	fee := common.HexToAddress("0x1234")
	other := common.HexToAddress("0x2222")
	a, b, c := common.HexToHash("0xa"), common.HexToHash("0xb"), common.HexToHash("0xc")
	db.AddSlotToAccessList(old, a)
	initial := db.accessList.Copy()
	outer := db.Snapshot()
	// Fee work warms a slot before the target list replaces the fee list.
	db.AddSlotToAccessList(fee, b)
	db.PrepareAccessList(old, &fee, nil, types.AccessList{{Address: fee, StorageKeys: []common.Hash{a}}})
	target := db.accessList.Copy()
	inner := db.Snapshot()
	db.AddSlotToAccessList(fee, c)
	db.PrepareAccessList(other, nil, nil, nil)
	db.AddSlotToAccessList(other, b)
	db.RevertToSnapshot(inner)
	if !reflect.DeepEqual(db.accessList, target) {
		t.Fatal("nested reset did not restore target warmth")
	}
	db.RevertToSnapshot(outer)
	if !reflect.DeepEqual(db.accessList, initial) {
		t.Fatal("fee/reset rollback did not restore the original list")
	}
}
