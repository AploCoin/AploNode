package params

import (
	"math/big"
	"testing"
)

// Native proof domains remain locked even before the unrelated EIP158 fork.
func TestSessionKeysNativeSigningDomainCompatibility(t *testing.T) {
	config := ChainConfig{ChainID: big.NewInt(10), HomesteadBlock: big.NewInt(0), EIP150Block: big.NewInt(0), EIP155Block: big.NewInt(0)}
	if err := config.CheckConfigForkOrder(); err != nil {
		t.Fatal(err)
	}
	next := config
	next.ChainID = big.NewInt(11)
	for _, height := range []uint64{0, 1, 100} {
		if err := config.CheckCompatible(&next, height); err == nil || err.RewindTo != 0 || err.What != "Chain ID" {
			t.Fatalf("primary domain mutable at height %d: %v", height, err)
		}
	}
	config = *TestChainConfig
	config.EthPoWForkBlock, config.ChainID_ALT = big.NewInt(10), big.NewInt(20)
	next = config
	next.ChainID_ALT = big.NewInt(21)
	if err := config.CheckCompatible(&next, 9); err != nil {
		t.Fatalf("unused alternate domain locked too soon: %v", err)
	}
	if err := config.CheckCompatible(&next, 10); err == nil || err.RewindTo != 9 || err.What != "EthPoW chain ID" {
		t.Fatalf("active alternate domain mutable: %v", err)
	}
	config.EthPoWForkBlock = big.NewInt(0)
	next = config
	next.ChainID_ALT = nil
	if err := config.CheckCompatible(&next, 0); err == nil || err.RewindTo != 0 {
		t.Fatalf("genesis alternate domain mutable: %v", err)
	}
}

func TestSessionKeysNativeSigningDomainBounds(t *testing.T) {
	for _, domain := range []*big.Int{nil, big.NewInt(-1), new(big.Int).Lsh(big.NewInt(1), 256)} {
		config := *TestChainConfig
		config.ChainID = domain
		if err := config.CheckConfigForkOrder(); err == nil {
			t.Fatalf("invalid primary domain accepted: %v", domain)
		}
		config = *TestChainConfig
		config.EthPoWForkBlock, config.ChainID_ALT = big.NewInt(0), domain
		if err := config.CheckConfigForkOrder(); err == nil {
			t.Fatalf("invalid alternate domain accepted: %v", domain)
		}
	}
}
