package blockoracle

import (
	"errors"

	"encoding/binary"

	"github.com/ethereum/go-ethereum/core/types"
)

var Functions = map[string]types.Function{
	// function GetBlockHash(uint256)
	"efd87d07": func(bc types.Blockchain, state types.StateDB, address types.ContractRef, input []byte, gas uint64) ([]byte, uint64, error) {

		if len(input) != 36 {
			return nil, 0, errors.New("execution reverted")
		}
		blockNumber := binary.BigEndian.Uint64(input[28:36])
		block := bc.GetBlockByNumber(blockNumber)
		if block == nil {
			return nil, 0, errors.New("execution reverted")
		}
		hash := block.Hash()
		return hash.Bytes(), 0, nil
	},
}
