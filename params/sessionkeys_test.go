package params

import (
	"math/big"
	"testing"
)

func TestSessionKeysForkConfiguration(t *testing.T) {
	config := *TestChainConfig
	if config.IsSessionKeys(big.NewInt(100)) {
		t.Fatal("session keys enabled by default")
	}
	config.SessionKeysBlock = big.NewInt(10)
	if config.IsSessionKeys(big.NewInt(9)) || !config.IsSessionKeys(big.NewInt(10)) || !config.IsSessionKeys(big.NewInt(11)) {
		t.Fatal("wrong fork boundary")
	}
	next := config
	next.SessionKeysBlock = big.NewInt(12)
	if err := config.CheckCompatible(&next, 9); err != nil {
		t.Fatal(err)
	}
	if err := config.CheckCompatible(&next, 10); err == nil || err.RewindTo != 9 {
		t.Fatalf("missing incompatible fork check: %v", err)
	}
	next = config
	next.SessionKeysBlock = nil
	if config.CheckCompatible(&next, 10) == nil {
		t.Fatal("active fork can be removed")
	}
	if config.CheckConfigForkOrder() != nil {
		t.Fatal("valid activation rejected")
	}
	config.EIP155Block = big.NewInt(11)
	if config.CheckConfigForkOrder() == nil {
		t.Fatal("unprotected domain activation accepted")
	}
	config.SessionKeysBlock = big.NewInt(-1)
	if config.CheckConfigForkOrder() == nil {
		t.Fatal("negative fork accepted")
	}
}

func TestSessionKeysSigningDomainCompatibility(t *testing.T) {
	// A valid pre-EIP158 custom chain must still lock its active session domain.
	config := ChainConfig{ChainID: big.NewInt(10), HomesteadBlock: big.NewInt(0), EIP150Block: big.NewInt(0), EIP155Block: big.NewInt(0), SessionKeysBlock: big.NewInt(5)}
	if err := config.CheckConfigForkOrder(); err != nil {
		t.Fatal(err)
	}
	next := config
	next.ChainID = big.NewInt(11)
	if err := config.CheckCompatible(&next, 4); err != nil {
		t.Fatalf("unused session domain locked too soon: %v", err)
	}
	if err := config.CheckCompatible(&next, 5); err == nil || err.RewindTo != 4 || err.What != "Session keys chain ID" {
		t.Fatalf("active primary domain not locked: %v", err)
	}
	// ALT can be scheduled/changed before its first session-enabled block.
	config = *TestChainConfig
	config.SessionKeysBlock, config.EthPoWForkBlock, config.ChainID_ALT = big.NewInt(5), big.NewInt(10), big.NewInt(20)
	next = config
	next.ChainID_ALT = big.NewInt(21)
	if err := config.CheckCompatible(&next, 9); err != nil {
		t.Fatalf("unused alternate domain locked too soon: %v", err)
	}
	if err := config.CheckCompatible(&next, 10); err == nil || err.RewindTo != 9 || err.What != "Session keys EthPoW chain ID" {
		t.Fatalf("active alternate domain not locked: %v", err)
	}
	// If EthPoW came first, session activation is the first relevant ALT height.
	config.SessionKeysBlock, config.EthPoWForkBlock = big.NewInt(10), big.NewInt(5)
	next = config
	next.ChainID_ALT = nil
	if err := config.CheckCompatible(&next, 9); err != nil {
		t.Fatalf("disabled session domain locked: %v", err)
	}
	if err := config.CheckCompatible(&next, 10); err == nil || err.RewindTo != 9 {
		t.Fatalf("nil alternate domain not rejected: %v", err)
	}
	config.SessionKeysBlock, config.EthPoWForkBlock = big.NewInt(0), big.NewInt(0)
	next = config
	next.ChainID_ALT = big.NewInt(22)
	if err := config.CheckCompatible(&next, 0); err == nil || err.RewindTo != 0 {
		t.Fatalf("genesis alternate domain not locked: %v", err)
	}
}
