package gaplo

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
)

type MinerParameters struct {
	LastBlock         *big.Int
	CurrentDifficulty *big.Int
	TotalMined        *big.Int
	PrevHash          *big.Int
	Staked            *big.Int
}

type Gaplo struct {
	address common.Address
	evm     *vm.EVM
}

func New(address common.Address, vm *vm.EVM) Gaplo {
	return Gaplo{
		address: address,
		evm:     vm,
	}
}

func (g *Gaplo) AddGaplo(address common.Address, amount *big.Int) ([]byte, error) {
	transferInput := crypto.Keccak256([]byte("refund(address,uint256)"))[0:4]
	paddedAmount := common.LeftPadBytes(amount.Bytes(), 32)
	paddedAddress := common.LeftPadBytes(address.Bytes(), 32)
	transferInput = append(transferInput, append(paddedAddress, paddedAmount...)...)

	rev, _, err := g.evm.Call(
		types.AccountRef(common.HexToAddress("0x0000000000000000000000000000000000000000")),
		g.address,
		transferInput,
		1000000000000000000,
		big.NewInt(0),
	)
	return rev, err
}

func (g *Gaplo) SubGaplo(address common.Address, amount *big.Int) error {
	transferInput := crypto.Keccak256([]byte("takeFee(uint256)"))[0:4]
	paddedAmount := common.LeftPadBytes(amount.Bytes(), 32)
	transferInput = append(transferInput, paddedAmount...)

	_, _, err := g.evm.Call(
		types.AccountRef(address),
		g.address,
		transferInput,
		1000000000000000000,
		big.NewInt(0),
	)
	return err
}

func (g *Gaplo) GetMinerParameters(address common.Address) (*MinerParameters, error) {
	minerParametersInput := crypto.Keccak256([]byte("miner_params(address)"))[0:4]
	paddedAddress := common.LeftPadBytes(address.Bytes(), 32)
	minerParametersInput = append(minerParametersInput, paddedAddress...)

	parameters, _, err := g.evm.Call(
		types.AccountRef(common.HexToAddress("0x0000000000000000000000000000000000000000")),
		g.address,
		minerParametersInput,
		1000000000000000000,
		big.NewInt(0),
	)
	if err != nil {
		return nil, err
	}

	if len(parameters) != 320 {
		return nil, errors.New("expected 320 bytes")
	}

	lastBlock := (&big.Int{}).SetBytes(parameters[0:64])
	currentDifficulty := (&big.Int{}).SetBytes(parameters[64:128])
	totalMined := (&big.Int{}).SetBytes(parameters[128:192])
	prevHash := (&big.Int{}).SetBytes(parameters[192:256])
	staked := (&big.Int{}).SetBytes(parameters[256:320])

	deserialized := &MinerParameters{
		LastBlock:         lastBlock,
		CurrentDifficulty: currentDifficulty,
		TotalMined:        totalMined,
		PrevHash:          prevHash,
		Staked:            staked,
	}

	return deserialized, err
}
