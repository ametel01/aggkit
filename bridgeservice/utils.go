package bridgeservice

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

const (
	claimTypeMessage = "message"
)

const (
	// DefaultPageSize is the default number of records to be fetched
	DefaultPageSize = uint32(20)
	// MaxPageSize is the maximum number of records to be fetched
	MaxPageSize = 200
	// DefaultPage is the default page number to be used when fetching records
	DefaultPage = uint32(1)
)

// validatePaginationParams validates the page number and page size
func validatePaginationParams(pageNumber, pageSize uint32) error {
	if pageNumber == 0 {
		return bridgesync.ErrInvalidPageNumber
	}

	if pageSize == 0 {
		return bridgesync.ErrInvalidPageSize
	}

	if pageSize > MaxPageSize {
		return fmt.Errorf("page size must be less than or equal to %d", MaxPageSize)
	}

	return nil
}

type UintParam interface {
	~uint32 | ~uint64
}

// parseUintQuery parses a uint32 or uint64 query parameter from the request context.
// If the parameter is mandatory and not present or invalid, it returns an error.
// If the parameter is optional, it returns the default value if not provided or invalid.
func parseUintQuery[T UintParam](c *gin.Context, key string, mandatory bool, defaultVal T) (T, error) {
	paramStr := c.Query(key)
	if paramStr == "" {
		if mandatory {
			return 0, fmt.Errorf("%s is mandatory", key)
		}
		return defaultVal, nil
	}

	param64, err := strconv.ParseUint(paramStr, 10, 64)
	if err != nil {
		if mandatory {
			return 0, fmt.Errorf("invalid %s parameter: %w", key, err)
		}
		return defaultVal, nil
	}

	var result T
	switch any(result).(type) {
	case uint32:
		if param64 > uint64(^uint32(0)) {
			return 0, fmt.Errorf("%s value out of range for uint32", key)
		}
		result = T(uint32(param64))
	case uint64:
		result = T(param64)
	default:
		return 0, fmt.Errorf("unsupported type for %s", key)
	}

	return result, nil
}

// parseUint32SliceParam parses a slice of uint32 parameters from the request context
func parseUint32SliceParam(c *gin.Context, key string) ([]uint32, error) {
	vals := c.QueryArray(key)
	result := make([]uint32, 0, len(vals))
	for _, v := range vals {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, err
		}
		result = append(result, uint32(n))
	}
	return result, nil
}

// hashToString converts a hash to string, returning empty string for zero hash
func hashToString(hash common.Hash) bridgetypes.Hash {
	if hash == (common.Hash{}) {
		return bridgetypes.Hash("")
	}
	return bridgetypes.Hash(hash.Hex())
}

// NewBridgeResponse creates a new BridgeResponse instance out of the provided Bridge instance
func NewBridgeResponse(bridge *bridgesync.Bridge) *bridgetypes.BridgeResponse {
	return &bridgetypes.BridgeResponse{
		BlockNum:           bridge.BlockNum,
		BlockPos:           bridge.BlockPos,
		FromAddress:        bridgetypes.Address(bridge.FromAddress.Hex()),
		BridgeTxHash:       hashToString(bridge.BridgeTxHash),
		Calldata:           fmt.Sprintf("0x%s", hex.EncodeToString(bridge.Calldata)),
		BlockTimestamp:     bridge.BlockTimestamp,
		LeafType:           bridge.LeafType,
		OriginNetwork:      bridge.OriginNetwork,
		OriginAddress:      bridgetypes.Address(bridge.OriginAddress.Hex()),
		DestinationNetwork: bridge.DestinationNetwork,
		DestinationAddress: bridgetypes.Address(bridge.DestinationAddress.Hex()),
		Amount:             bridgetypes.BigIntString(bridge.Amount.String()),
		Metadata:           fmt.Sprintf("0x%s", hex.EncodeToString(bridge.Metadata)),
		DepositCount:       bridge.DepositCount,
		IsNativeToken:      bridge.IsNativeToken,
		BridgeHash:         bridgetypes.Hash(bridge.Hash().Hex()),
	}
}

// NewClaimResponse creates ClaimResponse instance out of the provided Claim
func NewClaimResponse(claim *bridgesync.Claim) *bridgetypes.ClaimResponse {
	return NewClaimResponseWithBridge(claim, nil)
}

// NewClaimResponseWithBridge creates ClaimResponse instance with optional bridge data for more accurate type detection
func NewClaimResponseWithBridge(claim *bridgesync.Claim, bridge *bridgesync.Bridge) *bridgetypes.ClaimResponse {
	claimType := "asset"

	// Primary check: Use the IsMessage field which is set correctly during claim processing
	if claim.IsMessage {
		claimType = claimTypeMessage
	} else {
		// Fallback heuristic for backward compatibility:
		// Message bridges typically have amount = 0 and destination = BridgeExtension contract (same as origin)
		zero := big.NewInt(0)
		if claim.Amount.Cmp(zero) == 0 && claim.DestinationAddress == claim.OriginAddress {
			claimType = claimTypeMessage
		}
	}

	return &bridgetypes.ClaimResponse{
		GlobalIndex:        bridgetypes.BigIntString(claim.GlobalIndex.String()),
		DestinationNetwork: claim.DestinationNetwork,
		BridgeTxHash:       hashToString(claim.BridgeTxHash),
		ClaimTxHash:        hashToString(claim.ClaimTxHash),
		Amount:             bridgetypes.BigIntString(claim.Amount.String()),
		BlockNum:           claim.BlockNum,
		FromAddress:        bridgetypes.Address(claim.FromAddress.Hex()),
		DestinationAddress: bridgetypes.Address(claim.DestinationAddress.Hex()),
		OriginAddress:      bridgetypes.Address(claim.OriginAddress.Hex()),
		OriginNetwork:      claim.OriginNetwork,
		BlockTimestamp:     claim.BlockTimestamp,
		MainnetExitRoot:    bridgetypes.Hash(claim.MainnetExitRoot.Hex()),
		Status:             "completed",
		Type:               claimType,
	}
}

// NewPendingClaimResponse creates ClaimResponse instance for a pending claim from a Bridge event
func NewPendingClaimResponse(bridge *bridgesync.Bridge) *bridgetypes.ClaimResponse {
	claimType := "asset"
	if bridge.LeafType == 1 {
		claimType = claimTypeMessage
	}

	// For pending claims, we need to calculate the global index
	// Global index = OriginNetwork + DepositCount (encoded as per bridge protocol)
	// If origin network is 0 (mainnet), mainnetFlag = true, otherwise use origin network as rollup index
	mainnetFlag := bridge.OriginNetwork == 0
	rollupIndex := bridge.OriginNetwork
	if mainnetFlag {
		rollupIndex = 0
	}
	globalIndex := aggkitcommon.GenerateGlobalIndex(mainnetFlag, rollupIndex, bridge.DepositCount)

	return &bridgetypes.ClaimResponse{
		GlobalIndex:        bridgetypes.BigIntString(globalIndex.String()),
		DestinationNetwork: bridge.DestinationNetwork,
		BridgeTxHash:       hashToString(bridge.BridgeTxHash),
		ClaimTxHash:        bridgetypes.Hash(""), // Empty for pending claims
		Amount:             bridgetypes.BigIntString(bridge.Amount.String()),
		BlockNum:           bridge.BlockNum,
		FromAddress:        bridgetypes.Address(bridge.FromAddress.Hex()),
		DestinationAddress: bridgetypes.Address(bridge.DestinationAddress.Hex()),
		OriginAddress:      bridgetypes.Address(bridge.OriginAddress.Hex()),
		OriginNetwork:      bridge.OriginNetwork,
		BlockTimestamp:     bridge.BlockTimestamp,
		MainnetExitRoot: bridgetypes.Hash(
			"0x0000000000000000000000000000000000000000000000000000000000000000",
		), // Empty for pending
		Status: "pending",
		Type:   claimType,
	}
}

// NewTokenMappingResponse creates TokenMappingResponse instance out of the provided TokenMapping
func NewTokenMappingResponse(tokenMapping *bridgesync.TokenMapping) *bridgetypes.TokenMappingResponse {
	return &bridgetypes.TokenMappingResponse{
		BlockNum:            tokenMapping.BlockNum,
		BlockPos:            tokenMapping.BlockPos,
		BlockTimestamp:      tokenMapping.BlockTimestamp,
		TxHash:              bridgetypes.Hash(tokenMapping.TxHash.Hex()),
		OriginNetwork:       tokenMapping.OriginNetwork,
		OriginTokenAddress:  bridgetypes.Address(tokenMapping.OriginTokenAddress.Hex()),
		WrappedTokenAddress: bridgetypes.Address(tokenMapping.WrappedTokenAddress.Hex()),
		Metadata:            fmt.Sprintf("0x%s", hex.EncodeToString(tokenMapping.Metadata)),
		IsNotMintable:       tokenMapping.IsNotMintable,
		Calldata:            fmt.Sprintf("0x%s", hex.EncodeToString(tokenMapping.Calldata)),
		Type:                tokenMapping.Type,
	}
}

// NewTokenMigrationResponse creates LegacyTokenMigrationResponse instance out of the provided LegacyTokenMigration
func NewTokenMigrationResponse(
	tokenMigration *bridgesync.LegacyTokenMigration) *bridgetypes.LegacyTokenMigrationResponse {
	return &bridgetypes.LegacyTokenMigrationResponse{
		BlockNum:            tokenMigration.BlockNum,
		BlockPos:            tokenMigration.BlockPos,
		BlockTimestamp:      tokenMigration.BlockTimestamp,
		TxHash:              bridgetypes.Hash(tokenMigration.TxHash.Hex()),
		Sender:              bridgetypes.Address(tokenMigration.Sender.Hex()),
		LegacyTokenAddress:  bridgetypes.Address(tokenMigration.LegacyTokenAddress.Hex()),
		UpdatedTokenAddress: bridgetypes.Address(tokenMigration.UpdatedTokenAddress.Hex()),
		Amount:              bridgetypes.BigIntString(tokenMigration.Amount.String()),
		Calldata:            fmt.Sprintf("0x%s", hex.EncodeToString(tokenMigration.Calldata)),
	}
}

// NewL1InfoTreeLeafResponse creates L1InfoTreeLeafResponse instance out of the provided L1InfoTreeLeaf
func NewL1InfoTreeLeafResponse(leaf *l1infotreesync.L1InfoTreeLeaf) *bridgetypes.L1InfoTreeLeafResponse {
	return &bridgetypes.L1InfoTreeLeafResponse{
		BlockNumber:       leaf.BlockNumber,
		BlockPosition:     leaf.BlockPosition,
		L1InfoTreeIndex:   leaf.L1InfoTreeIndex,
		PreviousBlockHash: bridgetypes.Hash(leaf.PreviousBlockHash.Hex()),
		Timestamp:         leaf.Timestamp,
		MainnetExitRoot:   bridgetypes.Hash(leaf.MainnetExitRoot.Hex()),
		RollupExitRoot:    bridgetypes.Hash(leaf.RollupExitRoot.Hex()),
		GlobalExitRoot:    bridgetypes.Hash(leaf.GlobalExitRoot.Hex()),
		Hash:              bridgetypes.Hash(leaf.Hash.Hex()),
	}
}
