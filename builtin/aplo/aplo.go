package aplo

import (
	"encoding/hex"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

var AploInterfaces = map[string]bool{
	"70a08231": true,
	"a9059cbb": true,
	"06fdde03": true,
	"95d89b41": true,
	"313ce567": true,
	"01ffc9a7": true,
}
var Functions = map[string]types.Function{
	// balanceOf
	"70a08231": func(_ types.Blockchain, state types.StateDB, address types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {

		if len(input) != 36 {
			return nil, 0, errors.New("execution reverted")
		}
		of := input[4:36]
		ofAddr := common.BytesToAddress(of[12:32])
		balance := common.LeftPadBytes(state.GetBalance(ofAddr).Bytes(), 32)
		return balance, 0, nil
	},
	// transfer
	"a9059cbb": func(_ types.Blockchain, state types.StateDB, from types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
		if len(input) != 68 || gas < 25000 {
			return nil, gas / 2, errors.New("execution reverted")
		}
		to := input[4:36]
		toAddr := common.BytesToAddress(to[12:32])
		amount := input[36:68]
		amountInt := new(big.Int).SetBytes(amount)

		balance := state.GetBalance(from.Address())

		if balance.Cmp(amountInt) < 0 {
			return nil, gas / 2, errors.New("execution reverted")
		}

		state.SubBalance(from.Address(), amountInt)
		state.AddBalance(toAddr, amountInt)

		eventTopic := [32]byte{221, 242, 82, 173, 27, 226, 200, 155, 105, 194, 176, 104, 252, 55, 141, 170, 149, 43, 167, 241, 99, 196, 161, 22, 40, 245, 90, 77, 245, 35, 179, 239}
		fromTopic := (*common.Hash)(common.LeftPadBytes(from.Address().Bytes(), 32))
		toTopic := (*common.Hash)(to)
		state.AddLog(&types.Log{
			Address: [20]byte{},
			Topics:  []common.Hash{eventTopic, *fromTopic, *toTopic},
			Data:    amount,
		})
		ret := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		return ret, 25000, nil
	},
	// name
	"06fdde03": func(_ types.Blockchain, state types.StateDB, address types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
		ret := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 32, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 11, 65, 112, 108, 111, 32, 110, 97, 116, 105, 118, 101, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		return ret, 0, nil
	},
	// symbol
	"95d89b41": func(_ types.Blockchain, state types.StateDB, address types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
		ret := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 32, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 65, 80, 76, 79, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		return ret, 0, nil
	},
	// decimals
	"313ce567": func(_ types.Blockchain, state types.StateDB, address types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
		ret := [32]byte{
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			0,
			18,
		}
		return ret[0:32], 0, nil
	},
	// supportsinterface
	"01ffc9a7": func(_ types.Blockchain, state types.StateDB, address types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {
		if len(input) < 8 {
			return nil, 0, errors.New("execution reverted")
		}
		interfaceID := hex.EncodeToString(input[4:8])
		var ret []byte
		if _, ok := AploInterfaces[interfaceID]; ok {
			ret = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		} else {

			log.Error("unsupported interface", "interfaceID", interfaceID)
			ret = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		}
		return ret, 0, nil
	},
}
