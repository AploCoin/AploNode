package core

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/builtin/sessionkeys"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
)

func processCreate2SecurityBlock(
	t *testing.T,
	f *independentSessionProcessorFixture,
	parent *types.Block,
	txs ...*types.Transaction,
) (*types.Block, types.Receipts, uint64, error) {
	t.Helper()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     new(big.Int).Add(parent.Number(), big.NewInt(1)),
		GasLimit:   8_000_000,
		Time:       parent.Time() + 1,
		Difficulty: big.NewInt(1),
		Coinbase:   f.coinbase,
		BaseFee:    new(big.Int),
	}
	block := types.NewBlock(header, txs, nil, nil, trie.NewStackTrie(nil))
	receipts, _, usedGas, err := f.chain.processor.Process(block, f.state, vm.Config{})
	return block, receipts, usedGas, err
}

// create2SecurityFactoryRuntime embeds initCode and runs CREATE2 with a fixed
// salt, storing the returned address (or zero on collision) in storage slot 0.
func create2SecurityFactoryRuntime(initCode []byte, salt common.Hash) []byte {
	code := []byte{
		0x61, byte(len(initCode) >> 8), byte(len(initCode)),
		0x61, 0, 0, 0x60, 0, 0x39,
		0x7f,
	}
	code = append(code, salt[:]...)
	code = append(code,
		0x61, byte(len(initCode)>>8), byte(len(initCode)),
		0x60, 0, 0x60, 0, 0xf5,
		0x60, 0, 0x55, 0x00,
	)
	codeOffset := len(code)
	code[4], code[5] = byte(codeOffset>>8), byte(codeOffset)
	return append(code, initCode...)
}

// create2SecurityContractInitCode deploys an embedded runtime from a signed
// top-level contract-creation transaction.
func create2SecurityContractInitCode(runtime []byte) []byte {
	code := []byte{
		0x61, byte(len(runtime) >> 8), byte(len(runtime)),
		0x61, 0, 0, 0x60, 0, 0x39,
		0x61, byte(len(runtime) >> 8), byte(len(runtime)), 0x60, 0, 0xf3,
	}
	code[4], code[5] = byte(len(code)>>8), byte(len(code))
	return append(code, runtime...)
}

func signedCreate2SecurityContractTx(
	t *testing.T,
	f *independentSessionProcessorFixture,
	key *ecdsa.PrivateKey,
	nonce uint64,
	initCode []byte,
	block uint64,
) *types.Transaction {
	t.Helper()
	unsigned := types.NewContractCreation(nonce, new(big.Int), 2_000_000, big.NewInt(1), initCode)
	tx, err := types.SignTx(
		unsigned, types.MakeSigner(f.config, new(big.Int).SetUint64(block)), key,
	)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// create2RegistryAttemptInitCode makes a real CREATE2 constructor which calls
// the registry with its constructor caller and transaction origin as context.
// The constructor records the CALL result, ORIGIN, and CALLER, then succeeds.
func create2RegistryAttemptInitCode(input []byte) []byte {
	code := []byte{
		0x32, 0x60, 1, 0x55, // ORIGIN -> storage[1]
		0x33, 0x60, 2, 0x55, // CALLER -> storage[2]
		0x61, byte(len(input) >> 8), byte(len(input)),
		0x61, 0, 0, 0x60, 0, 0x39, // copy registry calldata to memory[0]
		0x60, 0, 0x60, 0,
		0x61, byte(len(input) >> 8), byte(len(input)),
		0x60, 0, 0x60, 0, 0x73,
	}
	code = append(code, sessionkeys.Address[:]...)
	code = append(code,
		0x62, 0x0f, 0x42, 0x40, 0xf1, // CALL with up to 1,000,000 gas
		0x60, 0, 0x55, // CALL result -> storage[0]
		0x60, 0, 0x60, 0, 0xf3, // return empty runtime code
	)
	codeOffset := len(code)
	code[12], code[13] = byte(codeOffset>>8), byte(codeOffset)
	return append(code, input...)
}

func TestSessionKeysSignedBlockCreate2ConstructorCannotRegisterFromOwnerOriginIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	factoryAddress := crypto.CreateAddress(f.owner, 0)
	selector := independentSessionSelector
	selectors := [][4]byte{selector}
	registeredAplo, registeredGaplo := big.NewInt(100), big.NewInt(5_000_000)
	registration := independentSessionProof(
		t, f.owner, f.session, factoryAddress, f.sessionKey, selectors,
		registeredAplo, registeredGaplo, big.NewInt(100), f.config.ChainID,
	)
	registrationInput, err := sessionkeys.ABI.Pack(
		"CreateSessionKey", f.session, factoryAddress, selectors,
		registeredAplo, registeredGaplo, big.NewInt(100), registration,
	)
	if err != nil {
		t.Fatal(err)
	}
	newKeySigner, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newKey := crypto.PubkeyToAddress(newKeySigner.PublicKey)
	newKeySelectors := [][4]byte{selector}
	newKeyAplo, newKeyGaplo, newKeyExpiry := big.NewInt(10), big.NewInt(20), big.NewInt(100)
	newKeyProof := independentSessionProof(
		t, f.owner, newKey, factoryAddress, newKeySigner, newKeySelectors,
		newKeyAplo, newKeyGaplo, newKeyExpiry, f.config.ChainID,
	)
	constructorInput, err := sessionkeys.ABI.Pack(
		"CreateSessionKey", newKey, factoryAddress, newKeySelectors,
		newKeyAplo, newKeyGaplo, newKeyExpiry, newKeyProof,
	)
	if err != nil {
		t.Fatal(err)
	}
	salt := common.HexToHash("0xc2")
	initCode := create2RegistryAttemptInitCode(constructorInput)
	factoryRuntime := create2SecurityFactoryRuntime(initCode, salt)
	factoryDeployTx := signedCreate2SecurityContractTx(
		t, f, f.ownerKey, 0, create2SecurityContractInitCode(factoryRuntime), 1,
	)
	createdAddress := crypto.CreateAddress2(
		factoryAddress, salt, crypto.Keccak256Hash(initCode).Bytes(),
	)
	createTx := f.signedTx(
		t, f.ownerKey, 1, sessionkeys.Address,
		new(big.Int), 900_000, registrationInput, 1,
	)
	useTx := f.signedTx(
		t, f.sessionKey, f.state.GetNonce(f.session), factoryAddress,
		new(big.Int), 2_500_000, selector[:], 1,
	)
	_, receipts, usedGas, err := processCreate2SecurityBlock(t, f, f.genesis, factoryDeployTx, createTx, useTx)
	if err != nil {
		t.Fatalf("StateProcessor.Process signed registration/CREATE2 block: %v", err)
	}
	if len(receipts) != 3 || usedGas == 0 {
		t.Fatalf("processor returned %d receipts and %d gas", len(receipts), usedGas)
	}
	for i, receipt := range receipts {
		if receipt.Status != types.ReceiptStatusSuccessful {
			t.Fatalf("transaction %d failed: status=%d", i, receipt.Status)
		}
	}
	if f.state.GetNonce(f.owner) != 2 || f.state.GetNonce(f.session) != 1 {
		t.Fatalf("signed nonce lanes owner=%d session=%d, want 2/1", f.state.GetNonce(f.owner), f.state.GetNonce(f.session))
	}
	if current := sessionkeys.Get(f.state, f.session); current == nil || current.Owner != f.owner || current.Nonce != 1 {
		t.Fatalf("direct owner registration/session call state: %#v", current)
	}
	if got := common.BytesToAddress(f.state.GetState(factoryAddress, common.Hash{}).Bytes()); got != createdAddress {
		t.Fatalf("factory CREATE2 result=%s, want %s", got, createdAddress)
	}
	if got := f.state.GetCode(factoryAddress); string(got) != string(factoryRuntime) {
		t.Fatalf("factory was not deployed by its signed creation transaction: %x", got)
	}
	if f.state.GetNonce(createdAddress) != 1 || f.state.GetCodeSize(createdAddress) != 0 {
		t.Fatalf("constructor result account nonce=%d code-size=%d", f.state.GetNonce(createdAddress), f.state.GetCodeSize(createdAddress))
	}
	if f.state.GetState(createdAddress, common.Hash{}) != (common.Hash{}) {
		t.Fatal("constructor registry CALL reported success for a nested caller with valid PoP")
	}
	if got := common.BytesToAddress(f.state.GetState(createdAddress, common.BigToHash(big.NewInt(1))).Bytes()); got != f.owner {
		t.Fatalf("constructor ORIGIN=%s, want owner %s", got, f.owner)
	}
	if got := common.BytesToAddress(f.state.GetState(createdAddress, common.BigToHash(big.NewInt(2))).Bytes()); got != factoryAddress {
		t.Fatalf("constructor CALLER=%s, want factory %s", got, factoryAddress)
	}
	if sessionkeys.Used(f.state, newKey) || sessionkeys.Get(f.state, newKey) != nil {
		t.Fatal("CREATE2 constructor registered or reserved a key through owner origin")
	}
	if got := f.state.GetState(sessionkeys.Address, sessionkeys.Slot("ownerCount", f.owner, 0)).Big().Uint64(); got != 1 {
		t.Fatalf("owner session count=%d, want only the directly registered key", got)
	}
}

func TestSessionKeysSignedBlockRejectsCodeBearingOwnerSignerIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	newKeySigner, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newKey := crypto.PubkeyToAddress(newKeySigner.PublicKey)
	selectors := [][4]byte{independentSessionSelector}
	aplo, gaplo, expiry := big.NewInt(10), big.NewInt(20), big.NewInt(50)
	proof := independentSessionProof(
		t, f.owner, newKey, f.target, newKeySigner, selectors,
		aplo, gaplo, expiry, f.config.ChainID,
	)
	input, err := sessionkeys.ABI.Pack(
		"CreateSessionKey", newKey, f.target, selectors, aplo, gaplo, expiry, proof,
	)
	if err != nil {
		t.Fatal(err)
	}
	// This is synthetic state, not a CREATE2 address construction: it verifies
	// the block-import EOA-signer invariant when the recovered signer has code.
	f.state.SetCode(f.owner, []byte{0x00})
	before := f.state.Copy().IntermediateRoot(false)
	tx := f.signedTx(
		t, f.ownerKey, f.state.GetNonce(f.owner), sessionkeys.Address,
		new(big.Int), 900_000, input, 1,
	)
	_, _, usedGas, err := processCreate2SecurityBlock(t, f, f.genesis, tx)
	if !errors.Is(err, ErrSenderNoEOA) {
		t.Fatalf("signed processor error=%v, want %v", err, ErrSenderNoEOA)
	}
	if usedGas != 0 {
		t.Fatalf("rejected block reported %d gas, want zero", usedGas)
	}
	if after := f.state.Copy().IntermediateRoot(false); after != before {
		t.Fatalf("code-bearing owner rejection changed state root: before=%s after=%s", before, after)
	}
	if f.state.GetNonce(f.owner) != 0 || sessionkeys.Get(f.state, newKey) != nil || sessionkeys.Used(f.state, newKey) {
		t.Fatal("code-bearing signer registration changed the owner nonce or registered the key")
	}
}

func create2TargetVariantInitCode(runtimeFirst, runtimeLater []byte) []byte {
	// Return runtimeFirst at block 1 and runtimeLater on later deployments. The
	// CREATE2 initcode itself remains byte-identical, so its address is stable.
	code := []byte{0x43, 0x60, 1, 0x14, 0x60, 0, 0x57} // NUMBER == 1; jump to first path
	code = append(code,
		0x60, byte(len(runtimeLater)), 0x60, 0, 0x60, 0, 0x39,
		0x60, byte(len(runtimeLater)), 0x60, 0, 0xf3,
		0x5b,
		0x60, byte(len(runtimeFirst)), 0x60, 0, 0x60, 0, 0x39,
		0x60, byte(len(runtimeFirst)), 0x60, 0, 0xf3,
	)
	dataLater := len(code)
	dataFirst := dataLater + len(runtimeLater)
	labelFirst := 19
	code[5] = byte(labelFirst)
	code[10] = byte(dataLater)
	code[23] = byte(dataFirst)
	code = append(code, runtimeLater...)
	code = append(code, runtimeFirst...)
	return code
}

func TestSessionKeysSignedBlockLegacySelfdestructCreate2TargetReplacementIndependent(t *testing.T) {
	f := newIndependentSessionProcessorFixture(t)
	// This protocol test exercises the repository's legacy SELFDESTRUCT behavior.
	f.config.ShanghaiBlock = nil
	selector := independentSessionSelector
	selectors := [][4]byte{selector}
	owner := f.owner
	factoryAddress := crypto.CreateAddress(owner, 0)
	runtimeFirst := []byte{0x60, 0, 0xff} // SELFDESTRUCT to address zero.
	runtimeLater := []byte{
		0x33, 0x60, 0, 0x55, // CALLER -> storage[0]
		0x32, 0x60, 1, 0x55, // ORIGIN -> storage[1]
		0x00,
	}
	initCode := create2TargetVariantInitCode(runtimeFirst, runtimeLater)
	salt := common.HexToHash("0x51")
	targetAddress := crypto.CreateAddress2(
		factoryAddress, salt, crypto.Keccak256Hash(initCode).Bytes(),
	)
	factoryRuntime := create2SecurityFactoryRuntime(initCode, salt)
	factoryDeployTx := signedCreate2SecurityContractTx(
		t, f, f.ownerKey, 0, create2SecurityContractInitCode(factoryRuntime), 1,
	)
	aplo, gaplo, expiry := big.NewInt(100), big.NewInt(10_000_000), big.NewInt(100)
	proof := independentSessionProof(
		t, owner, f.session, targetAddress, f.sessionKey, selectors,
		aplo, gaplo, expiry, f.config.ChainID,
	)
	registrationInput, err := sessionkeys.ABI.Pack(
		"CreateSessionKey", f.session, targetAddress, selectors, aplo, gaplo, expiry, proof,
	)
	if err != nil {
		t.Fatal(err)
	}
	registerTx := f.signedTx(
		t, f.ownerKey, 1, sessionkeys.Address,
		new(big.Int), 900_000, registrationInput, 1,
	)
	deployFirstTx := f.signedTx(
		t, f.ownerKey, 2, factoryAddress,
		new(big.Int), 1_000_000, nil, 1,
	)
	blockOne, receipts, _, err := processCreate2SecurityBlock(
		t, f, f.genesis, factoryDeployTx, registerTx, deployFirstTx,
	)
	if err != nil {
		t.Fatalf("process initial registration/deploy block: %v", err)
	}
	if len(receipts) != 3 {
		t.Fatalf("block one receipts=%d, want 3", len(receipts))
	}
	for i, receipt := range receipts {
		if receipt.Status != types.ReceiptStatusSuccessful {
			t.Fatalf("block one transaction %d status=%d", i, receipt.Status)
		}
	}
	if got := f.state.GetCode(factoryAddress); string(got) != string(factoryRuntime) {
		t.Fatalf("factory was not deployed by its signed creation transaction: %x", got)
	}
	if got := f.state.GetCode(targetAddress); string(got) != string(runtimeFirst) || f.state.GetNonce(targetAddress) != 1 {
		t.Fatalf("initial CREATE2 target code=%x nonce=%d", got, f.state.GetNonce(targetAddress))
	}

	killTx := f.signedTx(
		t, f.sessionKey, f.state.GetNonce(f.session), targetAddress,
		new(big.Int), 150_000, selector[:], 2,
	)
	blockTwo, killReceipts, _, err := processCreate2SecurityBlock(t, f, blockOne, killTx)
	if err != nil || len(killReceipts) != 1 || killReceipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("process whitelisted SELFDESTRUCT call: receipts=%v err=%v", killReceipts, err)
	}
	if got := f.state.GetCode(targetAddress); len(got) != 0 || f.state.GetNonce(targetAddress) != 0 {
		t.Fatalf("legacy SELFDESTRUCT left target code=%x nonce=%d", got, f.state.GetNonce(targetAddress))
	}
	if current := sessionkeys.Get(f.state, f.session); current == nil || current.Target != targetAddress || current.Nonce != 1 {
		t.Fatalf("session lost target authorization after SELFDESTRUCT: %#v", current)
	}

	deployLaterTx := f.signedTx(
		t, f.ownerKey, f.state.GetNonce(owner), factoryAddress,
		new(big.Int), 1_000_000, nil, 3,
	)
	callReplacementTx := f.signedTx(
		t, f.sessionKey, f.state.GetNonce(f.session), targetAddress,
		new(big.Int), 200_000, selector[:], 3,
	)
	_, laterReceipts, _, err := processCreate2SecurityBlock(
		t, f, blockTwo, deployLaterTx, callReplacementTx,
	)
	if err != nil {
		t.Fatalf("process CREATE2 redeploy/replacement session block: %v", err)
	}
	if len(laterReceipts) != 2 {
		t.Fatalf("block three receipts=%d, want 2", len(laterReceipts))
	}
	for i, receipt := range laterReceipts {
		if receipt.Status != types.ReceiptStatusSuccessful {
			t.Fatalf("block three transaction %d status=%d", i, receipt.Status)
		}
	}
	if got := f.state.GetCode(targetAddress); string(got) != string(runtimeLater) {
		t.Fatalf("CREATE2 replacement code=%x, want %x", got, runtimeLater)
	}
	if got := common.BytesToAddress(f.state.GetState(targetAddress, common.Hash{}).Bytes()); got != f.session {
		t.Fatalf("replacement target observed msg.sender=%s, want session key %s", got, f.session)
	}
	if got := common.BytesToAddress(f.state.GetState(targetAddress, common.BigToHash(big.NewInt(1))).Bytes()); got != owner {
		t.Fatalf("replacement target observed tx.origin=%s, want owner %s", got, owner)
	}
	if f.state.GetNonce(owner) != 4 || f.state.GetNonce(f.session) != 2 {
		t.Fatalf("post-redeployment nonce lanes owner=%d session=%d, want 4/2", f.state.GetNonce(owner), f.state.GetNonce(f.session))
	}
}

// This is an EVM invariant check, not a chosen-EOA CREATE2 attack: the
// destination is derived normally, then nonce or Used state is forced in the
// fixture before an actual signed factory call attempts CREATE2.
func TestSessionKeysSyntheticCreate2CollisionGuardForNonceAndUsedTombstonesIndependent(t *testing.T) {
	initCode := []byte{0x60, 0, 0x60, 0, 0xf3} // constructor returns empty runtime code.
	salt := common.HexToHash("0x77")
	tests := []struct {
		name  string
		setup func(*testing.T, *independentSessionProcessorFixture, common.Address)
	}{
		{
			name: "preexisting destination nonce",
			setup: func(t *testing.T, f *independentSessionProcessorFixture, destination common.Address) {
				f.state.SetNonce(destination, 1)
			},
		},
		{
			name: "active used key",
			setup: func(t *testing.T, f *independentSessionProcessorFixture, destination common.Address) {
				// The helper seeds valid registry state directly; it is not a claimed
				// proof-backed registration at this CREATE2-derived address.
				if err := sessionkeys.Create(
					f.state, f.owner, destination, f.target, [][4]byte{independentSessionSelector},
					big.NewInt(1), big.NewInt(1), 100, 0,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "revoked used-key tombstone",
			setup: func(t *testing.T, f *independentSessionProcessorFixture, destination common.Address) {
				if err := sessionkeys.Create(
					f.state, f.owner, destination, f.target, [][4]byte{independentSessionSelector},
					big.NewInt(1), big.NewInt(1), 100, 0,
				); err != nil {
					t.Fatal(err)
				}
				if err := sessionkeys.Revoke(f.state, f.owner, destination); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "expired used-key tombstone",
			setup: func(t *testing.T, f *independentSessionProcessorFixture, destination common.Address) {
				if err := sessionkeys.Create(
					f.state, f.owner, destination, f.target, [][4]byte{independentSessionSelector},
					big.NewInt(1), big.NewInt(1), 0, 0,
				); err != nil {
					t.Fatal(err)
				}
				sessionkeys.Cleanup(f.state, 0)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newIndependentSessionProcessorFixture(t)
			f.config.ShanghaiBlock = nil
			f.state.SetCode(f.target, create2SecurityFactoryRuntime(initCode, salt))
			destination := crypto.CreateAddress2(f.target, salt, crypto.Keccak256Hash(initCode).Bytes())
			test.setup(t, f, destination)
			beforeNonce := f.state.GetNonce(destination)
			tx := f.signedTx(
				t, f.ownerKey, f.state.GetNonce(f.owner), f.target,
				new(big.Int), 700_000, nil, 1,
			)
			_, receipts, _, err := processCreate2SecurityBlock(t, f, f.genesis, tx)
			if err != nil {
				t.Fatalf("signed factory CREATE2 transaction rejected: %v", err)
			}
			if len(receipts) != 1 || receipts[0].Status != types.ReceiptStatusSuccessful {
				t.Fatalf("factory receipt=%v", receipts)
			}
			if got := f.state.GetState(f.target, common.Hash{}); got != (common.Hash{}) {
				t.Fatalf("synthetic CREATE2 collision returned address %s, want zero", common.BytesToAddress(got.Bytes()))
			}
			if f.state.GetNonce(destination) != beforeNonce || f.state.GetCodeSize(destination) != 0 {
				t.Fatalf("CREATE2 collision changed destination nonce/code: %d/%d", f.state.GetNonce(destination), f.state.GetCodeSize(destination))
			}
			if test.name != "preexisting destination nonce" && !sessionkeys.Used(f.state, destination) {
				t.Fatal("used-key collision lost its permanent tombstone")
			}
		})
	}

	// Positive control for the same signed factory body: without forced state,
	// CREATE2 deploys at its computed address and stores that address in slot 0.
	f := newIndependentSessionProcessorFixture(t)
	f.state.SetCode(f.target, create2SecurityFactoryRuntime(initCode, salt))
	destination := crypto.CreateAddress2(f.target, salt, crypto.Keccak256Hash(initCode).Bytes())
	tx := f.signedTx(t, f.ownerKey, 0, f.target, new(big.Int), 700_000, nil, 1)
	_, receipts, _, err := processCreate2SecurityBlock(t, f, f.genesis, tx)
	if err != nil || len(receipts) != 1 || receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("unforced signed CREATE2 control failed: receipts=%v err=%v", receipts, err)
	}
	if got := common.BytesToAddress(f.state.GetState(f.target, common.Hash{}).Bytes()); got != destination {
		t.Fatalf("unforced CREATE2 result=%s, want %s", got, destination)
	}
	if f.state.GetNonce(destination) != 1 {
		t.Fatalf("unforced CREATE2 destination nonce=%d, want 1", f.state.GetNonce(destination))
	}
}
