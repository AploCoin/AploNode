package core

import (
	"fmt"
	"math/big"
	"os"
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
	"github.com/ethereum/go-ethereum/rlp"
)

func ordinaryBuiltinFixture(t *testing.T, registered bool) *independentSessionFixture {
	t.Helper()
	key, err := crypto.HexToECDSA("0123456789012345678901234567890123456789012345678901234567890123")
	if err != nil {
		t.Fatal(err)
	}
	db, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	config := *params.TestChainConfig
	f := &independentSessionFixture{
		db: db, config: &config, ownerKey: key, owner: crypto.PubkeyToAddress(key.PublicKey),
		coinbase: common.HexToAddress("0xc01b"),
		header:   &types.Header{Number: big.NewInt(1), Time: 1, GasLimit: 30000000, Difficulty: big.NewInt(1), BaseFee: big.NewInt(1)},
	}
	db.SetNonce(sessionkeys.Address, 1)
	db.SetCode(params.GAploContractAddress, common.FromHex(params.GAPLO))
	db.SetBalance(f.owner, big.NewInt(123))
	db.SetState(params.GAploContractAddress, sessionkeys.GaploSlot(f.owner), common.BigToHash(big.NewInt(500000)))
	db.SetState(params.GAploContractAddress, common.HexToHash("0x2"), common.BigToHash(big.NewInt(500000)))
	if registered {
		sessionKey, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		f.sessionKey, f.session = sessionKey, crypto.PubkeyToAddress(sessionKey.PublicKey)
		if err := sessionkeys.Create(db, f.owner, f.session, params.AploContractAddress,
			[][4]byte{{0xa9, 0x05, 0x9c, 0xbb}}, big.NewInt(100), big.NewInt(100000), 100, 1); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// The legacy APLO builtin accepts dirty high bytes in an address word and logs
// that exact word. Normalizing it without actual session-recipient routing
// changes receipt roots even though balances and gas remain identical.
func TestOrdinaryAPLOTransferPreservesRecipientWord(t *testing.T) {
	for _, registered := range []bool{false, true} {
		name := "without session"
		if registered {
			name = "registered owner"
		}
		t.Run(name, func(t *testing.T) {
			f := ordinaryBuiltinFixture(t, registered)
			f.db.SetCode(params.AploContractAddress, []byte{0})
			recipient := common.HexToAddress("0xb00b")
			word := common.LeftPadBytes(recipient[:], 32)
			word[0] = 1
			data := append(crypto.Keccak256([]byte("transfer(address,uint256)"))[:4], word...)
			data = append(data, common.LeftPadBytes(big.NewInt(7).Bytes(), 32)...)
			tx := f.signedTx(t, f.ownerKey, 0, params.AploContractAddress, new(big.Int), 100000, data)
			receipt, err := f.apply(t, tx)
			if err != nil || receipt.Status != types.ReceiptStatusSuccessful || receipt.GasUsed != 46368 || len(receipt.Logs) != 4 {
				t.Fatalf("historical transfer failed: err=%v receipt=%+v", err, receipt)
			}
			event := receipt.Logs[1]
			if event.Address != params.AploContractAddress || len(event.Topics) != 3 || event.Topics[2] != common.BytesToHash(word) || new(big.Int).SetBytes(event.Data).Cmp(big.NewInt(7)) != 0 {
				t.Fatalf("recipient word was rewritten: %+v", event)
			}
			if f.db.GetBalance(recipient).Cmp(big.NewInt(7)) != 0 || f.db.GetBalance(f.owner).Cmp(big.NewInt(116)) != 0 || f.db.GetNonce(f.owner) != 1 {
				t.Fatal("ordinary balances or signer nonce changed")
			}
			if registered && (f.db.GetNonce(f.session) != 0 || sessionkeys.Get(f.db, f.session).AploSpent.Cmp(big.NewInt(100)) != 0) {
				t.Fatal("owner transaction consumed session nonce or allowance")
			}
		})
	}
}

// Legacy builtins are not precompiles: an ordinary CALL with zero value must
// still skip an absent account after EIP158. Existing builtins and session
// execution retain their own dispatch rules.
func TestOrdinaryAbsentBuiltinCallRemainsNoop(t *testing.T) {
	for _, registered := range []bool{false, true} {
		for _, target := range []common.Address{params.AploContractAddress, params.BlockOracleContractAddress} {
			t.Run(fmt.Sprintf("%s/registered=%t", target, registered), func(t *testing.T) {
				f := ordinaryBuiltinFixture(t, registered)
				var data []byte
				var intrinsic uint64
				if target == params.AploContractAddress {
					recipient := common.HexToAddress("0xb00b")
					data = append(crypto.Keccak256([]byte("transfer(address,uint256)"))[:4], common.LeftPadBytes(recipient[:], 32)...)
					data = append(data, common.LeftPadBytes(big.NewInt(7).Bytes(), 32)...)
					intrinsic = 21356
				} else {
					data, intrinsic = []byte{0xde, 0xad, 0xbe, 0xef}, 21064
				}
				if f.db.Exist(target) {
					t.Fatal("builtin must initially be absent")
				}
				receipt, err := f.apply(t, f.signedTx(t, f.ownerKey, 0, target, new(big.Int), 100000, data))
				if err != nil || receipt.Status != types.ReceiptStatusSuccessful || receipt.GasUsed != intrinsic || len(receipt.Logs) != 3 {
					t.Fatalf("absent builtin was dispatched: err=%v receipt=%+v", err, receipt)
				}
				if f.db.Exist(target) || f.db.GetBalance(f.owner).Cmp(big.NewInt(123)) != 0 || f.db.GetNonce(f.owner) != 1 {
					t.Fatal("no-op changed native accounts")
				}
			})
		}
	}
}

func TestOrdinaryAPLOTransferStillRoutesUsedSessionRecipient(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(fmt.Sprintf("revoked=%t", revoked), func(t *testing.T) {
			f := ordinaryBuiltinFixture(t, true)
			f.db.SetCode(params.AploContractAddress, []byte{0})
			if revoked {
				if err := sessionkeys.Revoke(f.db, f.owner, f.session); err != nil {
					t.Fatal(err)
				}
			}
			word := common.LeftPadBytes(f.session[:], 32)
			word[0] = 1
			data := append(crypto.Keccak256([]byte("transfer(address,uint256)"))[:4], word...)
			data = append(data, common.LeftPadBytes(big.NewInt(7).Bytes(), 32)...)
			receipt, err := f.apply(t, f.signedTx(t, f.ownerKey, 0, params.AploContractAddress, new(big.Int), 100000, data))
			if err != nil || receipt.Status != types.ReceiptStatusSuccessful || len(receipt.Logs) != 4 {
				t.Fatalf("redirected transfer failed: err=%v receipt=%+v", err, receipt)
			}
			if receipt.Logs[1].Topics[2] != common.BytesToHash(f.owner[:]) || f.db.GetBalance(f.owner).Cmp(big.NewInt(123)) != 0 || f.db.GetBalance(f.session).Sign() != 0 {
				t.Fatal("used-key recipient did not route to owner")
			}
		})
	}
}

// This five-block RLP fixture was generated by pre-session 0416fd355 on an
// explicitly equivalent native genesis. It includes a GAplo read/transfer,
// access-list value, dirty APLO recipient and an absent-oracle CALL. The importer
// checks signatures, gas, receipt bloom/root and state roots, rather than merely
// comparing results from two paths using the same implementation.
func TestOrdinaryHistoricalBuiltinBlockImport(t *testing.T) {
	key, err := crypto.HexToECDSA("0123456789012345678901234567890123456789012345678901234567890123")
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(key.PublicKey)
	config := *params.AllEthashProtocolChanges
	config.ChainID_ALT = config.ChainID
	config.LondonBlock = big.NewInt(0)
	genesis := &Genesis{Config: &config, GasLimit: 30000000, Difficulty: big.NewInt(1), BaseFee: big.NewInt(1), Alloc: GenesisAlloc{
		owner:                      {Balance: big.NewInt(123)},
		sessionkeys.Address:        {Nonce: 1, Balance: new(big.Int)},
		params.AploContractAddress: {Code: []byte{0}, Balance: new(big.Int)},
		params.GAploContractAddress: {Code: common.FromHex(params.GAPLO), Balance: new(big.Int), Storage: map[common.Hash]common.Hash{
			sessionkeys.GaploSlot(owner): common.BigToHash(big.NewInt(1000000)),
			common.HexToHash("0x2"):      common.BigToHash(big.NewInt(1000000)),
		}},
	}}
	encoded, err := os.ReadFile("testdata/ordinary-builtin-blocks.rlp")
	if err != nil {
		t.Fatal(err)
	}
	var blocks types.Blocks
	if err := rlp.DecodeBytes(encoded, &blocks); err != nil {
		t.Fatal(err)
	}
	gas := []uint64{23993, 57256, 25300, 46368, 21064}
	if len(blocks) != len(gas) {
		t.Fatalf("unexpected block count %d", len(blocks))
	}
	disk := rawdb.NewMemoryDatabase()
	parent := genesis.MustCommit(disk)
	if blocks[0].ParentHash() != parent.Hash() {
		t.Fatal("historical fixture genesis mismatch")
	}
	engine := ethash.NewFaker()
	defer engine.Close()
	chain, err := NewBlockChain(disk, nil, &config, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	if _, err := chain.InsertChain(blocks); err != nil {
		t.Fatalf("historical ordinary blocks rejected: %v", err)
	}
	for i, block := range blocks {
		receipts := chain.GetReceiptsByHash(block.Hash())
		if len(receipts) != 1 || receipts[0].Status != types.ReceiptStatusSuccessful || receipts[0].GasUsed != gas[i] {
			t.Fatalf("block %d receipt differs: %+v", i+1, receipts)
		}
	}
}
