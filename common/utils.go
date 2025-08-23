package common

import (
	"math/big"
)

const (
	globalIndexPartSize = 4
	globalIndexMaxSize  = 9
)

func GenerateGlobalIndex(mainnetFlag bool, rollupIndex uint32, localExitRootIndex uint32) *big.Int {
	var (
		globalIndexBytes []byte
		buf              [globalIndexPartSize]byte
	)
	if mainnetFlag {
		globalIndexBytes = append(globalIndexBytes, big.NewInt(1).Bytes()...)
		ri := new(big.Int).FillBytes(buf[:])
		globalIndexBytes = append(globalIndexBytes, ri...)
	} else {
		ri := new(big.Int).SetUint64(uint64(rollupIndex)).FillBytes(buf[:])
		globalIndexBytes = append(globalIndexBytes, ri...)
	}
	leri := new(big.Int).SetUint64(uint64(localExitRootIndex)).FillBytes(buf[:])
	globalIndexBytes = append(globalIndexBytes, leri...)

	result := new(big.Int).SetBytes(globalIndexBytes)

	return result
}

// Decodes global index to its three parts:
// 1. mainnetFlag - first byte
// 2. rollupIndex - next 4 bytes
// 3. localExitRootIndex - last 4 bytes
// NOTE - mainnet flag is not in the global index bytes if it is false
// NOTE - rollup index is 0 if mainnet flag is true
// NOTE - rollup index is not in the global index bytes if mainnet flag is false and rollup index is 0
func DecodeGlobalIndex(globalIndex *big.Int) (mainnetFlag bool,
	rollupIndex uint32, localExitRootIndex uint32, err error) {
	globalIndexBytes := globalIndex.Bytes()
	l := len(globalIndexBytes)

	if l == 0 {
		// false, 0, 0
		return
	}

	if l == globalIndexMaxSize {
		// true, rollupIndex, localExitRootIndex
		mainnetFlag = true
	}

	localExitRootFromIdx := max(l-globalIndexPartSize, 0)
	rollupIndexFromIdx := max(localExitRootFromIdx-globalIndexPartSize, 0)

	rollupIndex = BytesToUint32(globalIndexBytes[rollupIndexFromIdx:localExitRootFromIdx])
	localExitRootIndex = BytesToUint32(globalIndexBytes[localExitRootFromIdx:])

	return
}

// Network ID to Chain ID mappings
const (
	NETWORK_ID_MAINNET    = 0
	NETWORK_ID_AGGLAYER_1 = 1
	NETWORK_ID_AGGLAYER_2 = 2
	NETWORK_ID_BASE       = 3

	CHAIN_ID_MAINNET    = 1
	CHAIN_ID_AGGLAYER_1 = 1101
	CHAIN_ID_AGGLAYER_2 = 137
	CHAIN_ID_BASE       = 8453
)

// chainIDToNetworkID converts chain ID back to network ID for API responses
// This ensures consistent network ID usage in API responses regardless of what's stored in database
func ChainIDToNetworkID(chainID uint32) uint32 {
	switch chainID {
	case CHAIN_ID_MAINNET:
		return NETWORK_ID_MAINNET // Ethereum mainnet
	case CHAIN_ID_AGGLAYER_1:
		return NETWORK_ID_AGGLAYER_1 // Polygon zkEVM L2
	case CHAIN_ID_AGGLAYER_2:
		return NETWORK_ID_AGGLAYER_2 // Polygon
	case CHAIN_ID_BASE:
		return NETWORK_ID_BASE // Base
	default:
		// For unknown chain IDs or if it's already a network ID, return as-is
		// This handles cases where the database already contains network IDs
		return chainID
	}
}

// NetworkIDToChainID converts network ID to chain ID as required by the smart contract
func NetworkIDToChainID(networkID uint32) uint32 {
	switch networkID {
	case NETWORK_ID_MAINNET:
		return CHAIN_ID_MAINNET
	case NETWORK_ID_AGGLAYER_1:
		return CHAIN_ID_AGGLAYER_1
	case NETWORK_ID_AGGLAYER_2:
		return CHAIN_ID_AGGLAYER_2
	default:
		// Default to networkId value
		return networkID
	}
}
