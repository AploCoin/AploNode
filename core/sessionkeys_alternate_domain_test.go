package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// The EthPoW fork changes both EIP-155 transaction signatures and the
// Session Keys proof domain to ChainID_ALT.
func TestSessionKeysAlternateDomainRegistrationAndUseIndependent(t *testing.T) {
	for _, test := range []struct {
		name        string
		proofDomain func(*params.ChainConfig) *big.Int
		wantSuccess bool
	}{
		{
			name: "alternate proof domain",
			proofDomain: func(config *params.ChainConfig) *big.Int {
				return config.ChainID_ALT
			},
			wantSuccess: true,
		},
		{
			name: "primary proof domain rejected after fork",
			proofDomain: func(config *params.ChainConfig) *big.Int {
				return config.ChainID
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newIndependentSessionProcessorFixture(t)
			f.config.EthPoWForkBlock = big.NewInt(1)
			f.config.ChainID_ALT = big.NewInt(2)
			selectors := [][4]byte{independentSessionSelector}
			aplo, gaplo, expiry := big.NewInt(100), big.NewInt(1_000_000), big.NewInt(10)
			proof := independentSessionProof(
				t, f.owner, f.session, f.target, f.sessionKey,
				selectors, aplo, gaplo, expiry, test.proofDomain(f.config),
			)
			create, err := sessionkeys.ABI.Pack(
				"CreateSessionKey", f.session, f.target, selectors,
				aplo, gaplo, expiry, proof,
			)
			if err != nil {
				t.Fatal(err)
			}
			createTx := f.signedTx(
				t, f.ownerKey, 0, sessionkeys.Address, new(big.Int), 700_000, create, 1,
			)
			if !createTx.Protected() || createTx.ChainId().Cmp(f.config.ChainID_ALT) != 0 {
				t.Fatalf("registration transaction does not use alternate EIP-155 domain: protected=%t chainID=%s", createTx.Protected(), createTx.ChainId())
			}
			txs := []*types.Transaction{createTx}
			if test.wantSuccess {
				useData := append(independentSessionSelector[:], 0x01)
				txs = append(txs, f.signedTx(
					t, f.sessionKey, 0, f.target, big.NewInt(11), 120_000, useData, 1,
				))
				if !txs[1].Protected() || txs[1].ChainId().Cmp(f.config.ChainID_ALT) != 0 {
					t.Fatalf("session use transaction does not use alternate EIP-155 domain: protected=%t chainID=%s", txs[1].Protected(), txs[1].ChainId())
				}
			}
			receipts, _ := f.process(t, 1, txs...)
			if len(receipts) != len(txs) {
				t.Fatalf("processor returned %d receipts for %d transactions", len(receipts), len(txs))
			}
			if !test.wantSuccess {
				if receipts[0].Status != types.ReceiptStatusFailed {
					t.Fatalf("primary-domain proof registration status=%d, want failed", receipts[0].Status)
				}
				if sessionkeys.Get(f.state, f.session) != nil || sessionkeys.Used(f.state, f.session) {
					t.Fatal("primary-domain proof registered or reserved a key after the alternate-domain fork")
				}
				return
			}
			for i, receipt := range receipts {
				if receipt.Status != types.ReceiptStatusSuccessful {
					t.Fatalf("alternate-domain transaction %d failed: status=%d", i, receipt.Status)
				}
			}
			if got := common.BytesToAddress(f.state.GetState(f.target, common.Hash{}).Bytes()); got != f.owner {
				t.Fatalf("alternate-domain session tx.origin=%s, want owner %s", got, f.owner)
			}
			if got := common.BytesToAddress(
				f.state.GetState(f.target, common.BigToHash(big.NewInt(1))).Bytes(),
			); got != f.session {
				t.Fatalf("alternate-domain session msg.sender=%s, want key %s", got, f.session)
			}
		})
	}
}
