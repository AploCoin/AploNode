package aplo

import (
	"encoding/hex"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
)

// contractAddr is the canonical address of this built-in contract.
var contractAddr = common.HexToAddress("0x0000000000000000000000000000000000001235")

// stakeUnit is the base staking step: 1 000 APLO expressed in wei (18 decimals).
var stakeUnit = new(big.Int).Mul(
	big.NewInt(1000),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
)

const maxTier = int64(6)

var AploInterfaces = map[string]bool{
	"70a08231": true,
	"a9059cbb": true,
	"06fdde03": true,
	"95d89b41": true,
	"313ce567": true,
	"01ffc9a7": true,
}

// Functions is populated in init() so staking-function selectors can be derived
// from their human-readable signatures instead of being hardcoded.
var Functions map[string]types.Function

func init() {
	Functions = map[string]types.Function{
		"70a08231": fnBalanceOf,
		"a9059cbb": fnTransfer,
		"06fdde03": fnName,
		"95d89b41": fnSymbol,
		"313ce567": fnDecimals,
		"01ffc9a7": fnSupportsInterface,
	}
	// Staking functions — selector derived at runtime.
	reg := func(sig string, fn types.Function) {
		sel := hex.EncodeToString(crypto.Keccak256([]byte(sig))[:4])
		Functions[sel] = fn
	}
	reg("stake(uint256)", fnStake)
	reg("unstake()", fnUnstake)
	reg("getStake(address)", fnGetStake)
	reg("getMultiplier(address)", fnGetMultiplier)
}

// ─── storage helpers ──────────────────────────────────────────────────────────

// stakingSlot returns the StateDB storage key for stakes[addr].
// Layout mirrors Solidity: keccak256(abi.encode(addr, uint256(0)))
// where 0 is the mapping's slot index.
func stakingSlot(addr common.Address) common.Hash {
	buf := make([]byte, 64)
	copy(buf[12:32], addr.Bytes()) // address zero-padded to 32 bytes at offset 0
	// slot index 0 → bytes 32-63 stay zero
	return crypto.Keccak256Hash(buf)
}

// tierMultiplier returns the reward multiplier for stakedAmount, scaled by 10
// to avoid floating-point arithmetic (10 = 1.0×, 17 = 1.7×).
// Returns 0 when stakedAmount is below the minimum staking threshold.
func tierMultiplier(stakedAmount *big.Int) int64 {
	if stakedAmount.Sign() == 0 || stakedAmount.Cmp(stakeUnit) < 0 {
		return 0
	}
	tier := new(big.Int).Div(stakedAmount, stakeUnit).Int64() - 1
	if tier > maxTier {
		tier = maxTier
	}
	return 11 + tier // 10, 11, 12, ..., 17
}

// StakingMultiplier is exported so state_transition.go can read the tier
// directly from StateDB without going through the EVM call machinery.
func StakingMultiplier(state types.StateDB, addr common.Address) int64 {
	return tierMultiplier(state.GetState(contractAddr, stakingSlot(addr)).Big())
}

// ─── view functions ───────────────────────────────────────────────────────────

var fnBalanceOf types.Function = func(_ types.Blockchain, state types.StateDB, _ types.ContractRef, input []byte, _ uint64) ([]byte, uint64, error) {
	if len(input) != 36 {
		return nil, 0, errors.New("execution reverted")
	}
	balance := common.LeftPadBytes(state.GetBalance(common.BytesToAddress(input[4:36])).Bytes(), 32)
	return balance, 0, nil
}

var fnName types.Function = func(_ types.Blockchain, _ types.StateDB, _ types.ContractRef, _ []byte, _ uint64) ([]byte, uint64, error) {
	ret := []byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 32,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 11,
		65, 112, 108, 111, 32, 110, 97, 116, 105, 118, 101, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	return ret, 0, nil
}

var fnSymbol types.Function = func(_ types.Blockchain, _ types.StateDB, _ types.ContractRef, _ []byte, _ uint64) ([]byte, uint64, error) {
	ret := []byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 32,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4,
		65, 80, 76, 79, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	return ret, 0, nil
}

var fnDecimals types.Function = func(_ types.Blockchain, _ types.StateDB, _ types.ContractRef, _ []byte, _ uint64) ([]byte, uint64, error) {
	ret := [32]byte{}
	ret[31] = 18
	return ret[:], 0, nil
}

var fnSupportsInterface types.Function = func(_ types.Blockchain, _ types.StateDB, _ types.ContractRef, input []byte, _ uint64) ([]byte, uint64, error) {
	if len(input) < 8 {
		return nil, 0, errors.New("execution reverted")
	}
	interfaceID := hex.EncodeToString(input[4:8])
	if AploInterfaces[interfaceID] {
		return []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, 0, nil
	}
	log.Error("unsupported interface", "interfaceID", interfaceID)
	return make([]byte, 32), 0, nil
}

// getStake(address) → uint256
var fnGetStake types.Function = func(_ types.Blockchain, state types.StateDB, _ types.ContractRef, input []byte, _ uint64) ([]byte, uint64, error) {
	if len(input) != 36 {
		return nil, 0, errors.New("execution reverted")
	}
	addr := common.BytesToAddress(input[4:36])
	staked := state.GetState(contractAddr, stakingSlot(addr)).Big()
	return common.LeftPadBytes(staked.Bytes(), 32), 0, nil
}

// getMultiplier(address) → uint256
// Returns the tier multiplier scaled by 10: 0 = not staked, 10 = 1.0×, …, 17 = 1.7×.
var fnGetMultiplier types.Function = func(_ types.Blockchain, state types.StateDB, _ types.ContractRef, input []byte, _ uint64) ([]byte, uint64, error) {
	if len(input) != 36 {
		return nil, 0, errors.New("execution reverted")
	}
	addr := common.BytesToAddress(input[4:36])
	mult := tierMultiplier(state.GetState(contractAddr, stakingSlot(addr)).Big())
	return common.LeftPadBytes(big.NewInt(mult).Bytes(), 32), 0, nil
}

// ─── state-mutating functions ─────────────────────────────────────────────────

var fnTransfer types.Function = func(_ types.Blockchain, state types.StateDB, from types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
	if len(input) != 68 || gas < 25000 {
		return nil, gas / 2, errors.New("execution reverted")
	}
	to := input[4:36]
	toAddr := common.BytesToAddress(to[12:32])
	amount := input[36:68]
	amountInt := new(big.Int).SetBytes(amount)

	if state.GetBalance(from.Address()).Cmp(amountInt) < 0 {
		return nil, gas / 2, errors.New("execution reverted")
	}

	state.SubBalance(from.Address(), amountInt)
	state.AddBalance(toAddr, amountInt)

	eventTopic := [32]byte{221, 242, 82, 173, 27, 226, 200, 155, 105, 194, 176, 104, 252, 55, 141, 170, 149, 43, 167, 241, 99, 196, 161, 22, 40, 245, 90, 77, 245, 35, 179, 239}
	fromTopic := (*common.Hash)(common.LeftPadBytes(from.Address().Bytes(), 32))
	toTopic := (*common.Hash)(to)
	state.AddLog(&types.Log{
		Address: contractAddr,
		Topics:  []common.Hash{eventTopic, *fromTopic, *toTopic},
		Data:    amount,
	})
	return []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, 25000, nil
}

// stake(uint256 amount)
// Locks `amount` of the caller's APLO inside the contract and records it as their stake.
// Calling stake multiple times accumulates the total stake.
//
// Tier table (stakeUnit = 1 000 APLO):
//
//	stake <  1 000 APLO → multiplier 0   (no mining reward)
//	stake >= 1 000 APLO → multiplier 10  (1.0×)
//	stake >= 2 000 APLO → multiplier 11  (1.1×)
//	…
//	stake >= 8 000 APLO → multiplier 17  (1.7×, max)
var fnStake types.Function = func(_ types.Blockchain, state types.StateDB, caller types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
	const gasUsed uint64 = 30000
	if gas < gasUsed {
		return nil, gas, errors.New("execution reverted: out of gas")
	}
	if len(input) != 36 {
		return nil, gas, errors.New("execution reverted: bad input")
	}
	amount := new(big.Int).SetBytes(input[4:36])
	if amount.Sign() == 0 {
		return nil, gas, errors.New("execution reverted: zero amount")
	}
	if state.GetBalance(caller.Address()).Cmp(amount) < 0 {
		return nil, gas, errors.New("execution reverted: insufficient balance")
	}

	// Lock tokens inside the contract.
	state.SubBalance(caller.Address(), amount)
	state.AddBalance(contractAddr, amount)

	// Accumulate stake record.
	slot := stakingSlot(caller.Address())
	newStake := new(big.Int).Add(state.GetState(contractAddr, slot).Big(), amount)
	state.SetState(contractAddr, slot, common.BigToHash(newStake))

	// Emit Staked(address indexed staker, uint256 amount, uint256 newTotal)
	topic := crypto.Keccak256Hash([]byte("Staked(address,uint256,uint256)"))
	callerTopic := common.BytesToHash(common.LeftPadBytes(caller.Address().Bytes(), 32))
	data := make([]byte, 64)
	copy(data[0:32], common.LeftPadBytes(amount.Bytes(), 32))
	copy(data[32:64], common.LeftPadBytes(newStake.Bytes(), 32))
	state.AddLog(&types.Log{
		Address: contractAddr,
		Topics:  []common.Hash{topic, callerTopic},
		Data:    data,
	})

	return make([]byte, 32), gasUsed, nil
}

// unstake()
// Returns the caller's entire stake and clears their record.
// The caller drops back to multiplier 0 and cannot mine GAplo until they restake.
var fnUnstake types.Function = func(_ types.Blockchain, state types.StateDB, caller types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
	const gasUsed uint64 = 30000
	if gas < gasUsed {
		return nil, gas, errors.New("execution reverted: out of gas")
	}

	slot := stakingSlot(caller.Address())
	staked := state.GetState(contractAddr, slot).Big()
	if staked.Sign() == 0 {
		return nil, gas, errors.New("execution reverted: nothing staked")
	}
	if state.GetBalance(contractAddr).Cmp(staked) < 0 {
		// Defensive — should never occur under normal operation.
		return nil, gas, errors.New("execution reverted: contract balance insufficient")
	}

	// Return tokens.
	state.SubBalance(contractAddr, staked)
	state.AddBalance(caller.Address(), staked)

	// Clear stake record.
	state.SetState(contractAddr, slot, common.Hash{})

	// Emit Unstaked(address indexed staker, uint256 amount)
	topic := crypto.Keccak256Hash([]byte("Unstaked(address,uint256)"))
	callerTopic := common.BytesToHash(common.LeftPadBytes(caller.Address().Bytes(), 32))
	state.AddLog(&types.Log{
		Address: contractAddr,
		Topics:  []common.Hash{topic, callerTopic},
		Data:    common.LeftPadBytes(staked.Bytes(), 32),
	})

	return make([]byte, 32), gasUsed, nil
}
