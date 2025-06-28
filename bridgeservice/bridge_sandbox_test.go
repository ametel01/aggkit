package bridgeservice

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/config"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBridgeService_SandboxMode(t *testing.T) {
	t.Run("isSandboxMode returns true when sandbox config is enabled", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled: true,
		}

		assert.True(t, bridgeMocks.bridge.isSandboxMode())
	})

	t.Run("isSandboxMode returns false when sandbox config is disabled", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled: false,
		}

		assert.False(t, bridgeMocks.bridge.isSandboxMode())
	})

	t.Run("isSandboxMode returns false when sandbox config is nil", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = nil

		assert.False(t, bridgeMocks.bridge.isSandboxMode())
	})
}

func TestBridgeService_CreateSandboxMetadata(t *testing.T) {
	t.Run("returns nil when not in sandbox mode", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = nil

		metadata := bridgeMocks.bridge.createSandboxMetadata()
		assert.Nil(t, metadata)
	})

	t.Run("returns sandbox metadata when in sandbox mode", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled:          true,
			AutoSettle:       true,
			InstantClaims:    true,
			MockFinalization: true,
			SettlementDelay:  configtypes.NewDuration(5 * time.Second),
			L1Node: config.SandboxNodeConfig{
				ChainID: 31337,
			},
			L2Node: config.SandboxNodeConfig{
				ChainID: 31338,
			},
		}

		metadata := bridgeMocks.bridge.createSandboxMetadata()
		require.NotNil(t, metadata)

		assert.True(t, metadata.SandboxMode)
		assert.True(t, metadata.AutoSettle)
		assert.True(t, metadata.InstantClaims)
		assert.True(t, metadata.MockFinalization)
		assert.Equal(t, "5s", metadata.SettlementDelay)
		assert.NotZero(t, metadata.GeneratedAt)

		assert.Equal(t, "sandbox", metadata.DevMetadata["bridge_mode"])
		assert.Equal(t, uint64(31337), metadata.DevMetadata["l1_chain_id"])
		assert.Equal(t, uint64(31338), metadata.DevMetadata["l2_chain_id"])
	})
}

func TestBridgeService_GetBridgesHandler_Sandbox(t *testing.T) {
	t.Run("includes sandbox metadata in bridge responses", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled:          true,
			AutoSettle:       true,
			InstantClaims:    true,
			MockFinalization: true,
			SettlementDelay:  configtypes.NewDuration(2 * time.Second),
			L1Node: config.SandboxNodeConfig{
				ChainID: 31337,
			},
			L2Node: config.SandboxNodeConfig{
				ChainID: 31338,
			},
		}

		bridges := []*bridgesync.Bridge{
			{
				BlockNum:           1,
				DepositCount:       1,
				OriginNetwork:      0,
				DestinationNetwork: 1,
				LeafType:           1,
			},
		}

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(bridges, 1, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/bridges?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusOK, response.Code)

		var result types.BridgesResult
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)

		// Check that the result has sandbox metadata
		require.NotNil(t, result.SandboxMetadata)
		assert.True(t, result.SandboxMetadata.SandboxMode)
		assert.True(t, result.SandboxMetadata.AutoSettle)
		assert.True(t, result.SandboxMetadata.InstantClaims)

		// Check that individual bridge responses have sandbox metadata
		require.Len(t, result.Bridges, 1)
		bridgeResp := result.Bridges[0]
		require.NotNil(t, bridgeResp.SandboxMetadata)
		assert.True(t, bridgeResp.SandboxMetadata.SandboxMode)
		assert.True(t, bridgeResp.SandboxMetadata.InstantClaims)
	})
}

func TestBridgeService_GetClaimsHandler_Sandbox(t *testing.T) {
	t.Run("includes sandbox metadata in claims responses", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled:       true,
			InstantClaims: true,
		}

		claims := []*bridgesync.Claim{
			{
				GlobalIndex:        big.NewInt(1),
				OriginNetwork:      0,
				DestinationNetwork: 1,
				BlockNum:           100,
			},
		}

		bridgeMocks.bridgeL2.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(claims, 1, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, fmt.Sprintf("%d", l2NetworkID))

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/claims?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusOK, response.Code)

		var result types.ClaimsResult
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)

		// Check that the result has sandbox metadata with instant claims info
		require.NotNil(t, result.SandboxMetadata)
		assert.True(t, result.SandboxMetadata.SandboxMode)
		assert.True(t, result.SandboxMetadata.InstantClaims)

		// Check dev metadata for instant claims
		assert.Equal(t, true, result.SandboxMetadata.DevMetadata["claims_instantly_ready"])
		assert.Equal(t, "0s", result.SandboxMetadata.DevMetadata["claim_processing_time"])
	})
}

func TestBridgeService_ClaimProofHandler_Sandbox(t *testing.T) {
	t.Run("includes sandbox metadata in claim proof responses", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled:       true,
			InstantClaims: true,
		}

		l1InfoTreeLeaf := &l1infotreesync.L1InfoTreeLeaf{
			MainnetExitRoot: common.HexToHash("0x1"),
			RollupExitRoot:  common.HexToHash("0x2"),
		}

		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, mock.Anything).
			Return(l1InfoTreeLeaf, nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetProof(mock.Anything, mock.Anything, mock.Anything).
			Return(tree.Proof{}, nil)

		bridgeMocks.l1InfoTree.EXPECT().
			GetRollupExitTreeMerkleProof(mock.Anything, mock.Anything, mock.Anything).
			Return(tree.Proof{}, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, strconv.Itoa(mainnetNetworkID))
		queryParams.Set(leafIndexParam, "1")
		queryParams.Set(depositCountParam, "1")

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/claim-proof?%s", BridgeV1Prefix, queryParams.Encode()), nil)
		require.Equal(t, http.StatusOK, response.Code)

		var result types.ClaimProof
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)

		// Check that the claim proof has sandbox metadata
		require.NotNil(t, result.SandboxMetadata)
		assert.True(t, result.SandboxMetadata.SandboxMode)
		assert.True(t, result.SandboxMetadata.InstantClaims)

		// Check dev metadata for instant claims
		assert.Equal(t, "instant_sandbox_mode", result.SandboxMetadata.DevMetadata["claim_verification"])
		assert.Equal(t, "simplified_local_calculation", result.SandboxMetadata.DevMetadata["proof_method"])

		// Check that L1InfoTreeLeaf also has sandbox metadata
		require.NotNil(t, result.L1InfoTreeLeaf.SandboxMetadata)
		assert.True(t, result.L1InfoTreeLeaf.SandboxMetadata.SandboxMode)
	})
}

func TestBridgeService_GetSyncStatusHandler_Sandbox(t *testing.T) {
	t.Run("includes sandbox metadata in sync status responses", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled:    true,
			AutoSettle: true,
		}

		bridgeMocks.bridgeL1.EXPECT().
			GetContractDepositCount(mock.Anything).
			Return(uint32(10), nil)

		bridgeMocks.bridgeL2.EXPECT().
			GetContractDepositCount(mock.Anything).
			Return(uint32(5), nil)

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{}, 0, nil)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{}, 0, nil)

		response := performRequest(t, bridgeMocks.bridge.router, http.MethodGet,
			fmt.Sprintf("%s/sync-status", BridgeV1Prefix), nil)
		require.Equal(t, http.StatusOK, response.Code)

		var result types.SyncStatus
		err := json.Unmarshal(response.Body.Bytes(), &result)
		require.NoError(t, err)

		// Check that the sync status has sandbox metadata
		require.NotNil(t, result.SandboxMetadata)
		assert.True(t, result.SandboxMetadata.SandboxMode)
		assert.True(t, result.SandboxMetadata.AutoSettle)

		// Check dev metadata for sync mode
		assert.Equal(t, "sandbox_instant", result.SandboxMetadata.DevMetadata["sync_mode"])
		assert.Equal(t, "automatic", result.SandboxMetadata.DevMetadata["settlement_mode"])
	})
}

func TestBridgeService_IsClaimInstantlyReady(t *testing.T) {
	t.Run("returns true when in sandbox mode with instant claims", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled:       true,
			InstantClaims: true,
		}

		assert.True(t, bridgeMocks.bridge.isClaimInstantlyReady())
	})

	t.Run("returns false when not in sandbox mode", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = nil

		assert.False(t, bridgeMocks.bridge.isClaimInstantlyReady())
	})

	t.Run("returns false when in sandbox mode but instant claims disabled", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.bridge.sandboxConfig = &config.SandboxConfig{
			Enabled:       true,
			InstantClaims: false,
		}

		assert.False(t, bridgeMocks.bridge.isClaimInstantlyReady())
	})
}
