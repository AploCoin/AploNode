package builtin

import (
	"encoding/hex"
	"errors"

	"github.com/ethereum/go-ethereum/builtin/aplo"
	"github.com/ethereum/go-ethereum/builtin/blockoracle"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var BuiltInContracts map[common.Address]*map[string]types.Function

// readOnlySelectors lists the function selectors that do NOT modify state,
// per contract address. Only these may be invoked via STATICCALL.
var readOnlySelectors map[common.Address]map[string]bool

func init() {
	BuiltInContracts = make(map[common.Address]*map[string]types.Function)
	BuiltInContracts[common.HexToAddress("0x0000000000000000000000000000000000001235")] = &aplo.Functions
	BuiltInContracts[common.HexToAddress("0x0000000000000000000000000000000000001236")] = &blockoracle.Functions

	sel := func(sig string) string {
		return hex.EncodeToString(crypto.Keccak256([]byte(sig))[:4])
	}

	readOnlySelectors = map[common.Address]map[string]bool{
		common.HexToAddress("0x0000000000000000000000000000000000001235"): {
			"70a08231":                    true, // balanceOf(address)
			"06fdde03":                    true, // name()
			"95d89b41":                    true, // symbol()
			"313ce567":                    true, // decimals()
			"01ffc9a7":                    true, // supportsInterface(bytes4)
			sel("getStake(address)"):      true, // getStake(address)
			sel("getMultiplier(address)"): true, // getMultiplier(address)
		},
		common.HexToAddress("0x0000000000000000000000000000000000001236"): {
			"efd87d07": true, // GetBlockHash(uint256)
		},
	}
}

// IsReadOnly reports whether the call to contractAddress with the given input
// is safe to execute in a static (read-only) context. Returns false for any
// unknown contract, short input, or state-mutating selector.
func IsReadOnly(contractAddress *common.Address, input []byte) bool {
	selectors, ok := readOnlySelectors[*contractAddress]
	if !ok {
		return false
	}
	if len(input) < 4 {
		return false
	}
	return selectors[hex.EncodeToString(input[0:4])]
}

func GetFunction(contractAddress *common.Address, input []byte) (types.Function, error) {
	contract, ok := BuiltInContracts[*contractAddress]
	if !ok {
		return nil, errors.New("No contract found")
	}

	if len(input) < 4 {
		function, ok := (*contract)["fallback"]
		if !ok {
			return nil, errors.New("No function found")
		}
		return function, nil
	}

	selector := hex.EncodeToString(input[0:4])
	function, ok := (*contract)[selector]
	if !ok {
		return nil, errors.New("No function found")
	}

	return function, nil
}
