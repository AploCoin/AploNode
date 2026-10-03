package forkid

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

func TestSessionKeysForkIDBoundary(t *testing.T) {
	disabled := *params.TestChainConfig
	enabled := disabled
	enabled.SessionKeysBlock = big.NewInt(7)
	genesis := common.HexToHash("0x9000")
	base := NewID(&disabled, genesis, 7)
	before := NewID(&enabled, genesis, 6)
	after := NewID(&enabled, genesis, 7)
	if before.Hash != base.Hash || before.Next != 7 {
		t.Fatalf("session activation missing from advertised next fork: %+v", before)
	}
	if after.Hash == base.Hash || after.Next != 0 || NewID(&enabled, genesis, 8) != after {
		t.Fatalf("session activation missing from passed-fork checksum: %+v", after)
	}
}
