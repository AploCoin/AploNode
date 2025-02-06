package builtin

import (
	"encoding/hex"
	"errors"

	"github.com/ethereum/go-ethereum/builtin/aplo"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var BuiltInContracts map[common.Address]*map[string]types.Function

func init() {
	BuiltInContracts[common.HexToAddress("0x0000000000000000000000000000000000001235")] = &aplo.AploFunctions
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
