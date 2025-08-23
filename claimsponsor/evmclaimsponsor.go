package claimsponsor

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	configTypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	// LeafTypeAsset represents a bridge asset
	LeafTypeAsset uint8 = 0
	// LeafTypeMessage represents a bridge message
	LeafTypeMessage uint8 = 1

	gasTooHighErrTemplate = "Claim tx estimated to consume more gas than the maximum allowed by the service. " +
		"Estimated %d, maximum allowed: %d"

	// Network IDs - now used directly without chain ID mapping
	NETWORK_ID_MAINNET    = 0
	NETWORK_ID_AGGLAYER_1 = 1
	NETWORK_ID_AGGLAYER_2 = 2
)

type EthClienter interface {
	ethereum.GasEstimator
	bind.ContractBackend
}

type EthTxManager interface {
	Remove(ctx context.Context, id common.Hash) error
	ResultsByStatus(
		ctx context.Context,
		statuses []ethtxtypes.MonitoredTxStatus,
	) ([]ethtxtypes.MonitoredTxResult, error)
	Result(ctx context.Context, id common.Hash) (ethtxtypes.MonitoredTxResult, error)
	Add(ctx context.Context, to *common.Address, value *big.Int, data []byte,
		gasOffset uint64, sidecar *types.BlobTxSidecar) (common.Hash, error)
}

type EVMClaimSponsor struct {
	l2Client     EthClienter
	bridgeABI    *abi.ABI
	bridgeAddr   common.Address
	ethTxManager EthTxManager
	sender       common.Address
	gasOffest    uint64
	maxGas       uint64
	l1InfoTree   *l1infotreesync.L1InfoTreeSync
}

type EVMClaimSponsorConfig struct {
	// DBPath path of the DB
	DBPath string `mapstructure:"DBPath"`
	// Enabled indicates if the sponsor should be run or not
	Enabled bool `mapstructure:"Enabled"`
	// SenderAddr is the address that will be used to send the claim txs
	SenderAddr common.Address `mapstructure:"SenderAddr"`
	// BridgeAddrL2 is the address of the bridge smart contract on L2
	BridgeAddrL2 common.Address `mapstructure:"BridgeAddrL2"`
	// MaxGas is the max gas (limit) allowed for a claim to be sponsored
	MaxGas uint64 `mapstructure:"MaxGas"`
	// RetryAfterErrorPeriod is the time that will be waited when an unexpected error happens before retry
	RetryAfterErrorPeriod configTypes.Duration `mapstructure:"RetryAfterErrorPeriod"`
	// MaxRetryAttemptsAfterError is the maximum number of consecutive attempts that will happen before panicing.
	// Any number smaller than zero will be considered as unlimited retries
	MaxRetryAttemptsAfterError int `mapstructure:"MaxRetryAttemptsAfterError"`
	// WaitTxToBeMinedPeriod is the period that will be used to ask if a given tx has been mined (or failed)
	WaitTxToBeMinedPeriod configTypes.Duration `mapstructure:"WaitTxToBeMinedPeriod"`
	// WaitOnEmptyQueue is the time that will be waited before trying to send the next claim of the queue
	// if the queue is empty
	WaitOnEmptyQueue configTypes.Duration `mapstructure:"WaitOnEmptyQueue"`
	// EthTxManager is the configuration of the EthTxManager to be used by the claim sponsor
	EthTxManager ethtxmanager.Config `mapstructure:"EthTxManager"`
	// GasOffset is the gas to add on top of the estimated gas when sending the claim txs
	GasOffset uint64 `mapstructure:"GasOffset"`
	// ClaimAll indicates whether to automatically sponsor all claims
	ClaimAll bool `mapstructure:"ClaimAll"`
}

func NewEVMClaimSponsor(
	logger *log.Logger,
	dbPath string,
	l2Client EthClienter,
	bridgeAddr common.Address,
	sender common.Address,
	maxGas, gasOffset uint64,
	ethTxManager EthTxManager,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	waitTxToBeMinedPeriod time.Duration,
	waitOnEmptyQueue time.Duration,
	l1InfoTree *l1infotreesync.L1InfoTreeSync,
) (*ClaimSponsor, error) {
	abi, err := getPolygonZkEVMBridgeV2ABI()
	if err != nil {
		return nil, err
	}

	evmSponsor := &EVMClaimSponsor{
		l2Client:     l2Client,
		bridgeABI:    &abi,
		bridgeAddr:   bridgeAddr,
		sender:       sender,
		gasOffest:    gasOffset,
		maxGas:       maxGas,
		ethTxManager: ethTxManager,
		l1InfoTree:   l1InfoTree,
	}

	baseSponsor, err := newClaimSponsor(
		logger,
		dbPath,
		evmSponsor,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		waitTxToBeMinedPeriod,
		waitOnEmptyQueue,
	)
	if err != nil {
		return nil, err
	}

	return baseSponsor, nil
}

func (c *EVMClaimSponsor) checkClaim(ctx context.Context, claim *Claim) (*Claim, error) {
	// Check for MainnetExitRoot and RollupExitRoot
	if claimRootsEmpty(claim) {
		_, _, l1InfoIndex, err := aggkitcommon.DecodeGlobalIndex(claim.GlobalIndex)
		if err != nil {
			return claim, fmt.Errorf("error decoding global_index %d: %w", claim.GlobalIndex, err)
		}

		// Set actual roots in the claim. When the claim was created these fields were left empty
		roots, err := GetRoots(ctx, c.l1InfoTree, l1InfoIndex)
		if err != nil {
			return claim, fmt.Errorf("error getting root: %w", err)
		}
		claim.MainnetExitRoot = roots.MainnetExitRoot
		claim.RollupExitRoot = roots.RollupExitRoot
	}

	// TODO: Remove this patch once the inconsistencies between networkID and chainID are solved
	if claim.DestinationNetwork == 0 {
		claim.DestinationNetwork = 1
	}

	data, err := c.buildClaimTxData(claim)
	if err != nil {
		return claim, err
	}

	for {
		gas, err := c.l2Client.EstimateGas(ctx, ethereum.CallMsg{
			From: c.sender,
			To:   &c.bridgeAddr,
			Data: data,
		})
		attempts := 0
		if err != nil {
			// Gas estimation might be failing due to the GER not having been updated yet
			attempts++
			if attempts > 100 {
				return claim, fmt.Errorf("gas estimation failed after 100 attempts: %v", err)
			}
			continue
		}

		if gas > c.maxGas {
			return claim, fmt.Errorf(gasTooHighErrTemplate, gas, c.maxGas)
		}
		log.Infof("Gas estimation successful: %d", gas)
		break
	}

	return claim, nil
}

func (c *EVMClaimSponsor) sendClaim(ctx context.Context, claim *Claim) (string, error) {
	data, err := c.buildClaimTxData(claim)
	if err != nil {
		return "", err
	}

	id, err := c.ethTxManager.Add(ctx, &c.bridgeAddr, common.Big0, data, c.gasOffest, nil)
	if err != nil {
		return "", err
	}

	return id.Hex(), nil
}

func (c *EVMClaimSponsor) claimStatus(ctx context.Context, id string) (ClaimStatus, error) {
	res, err := c.ethTxManager.Result(ctx, common.HexToHash(id))
	if err != nil {
		return "", err
	}
	switch res.Status {
	case ethtxtypes.MonitoredTxStatusCreated,
		ethtxtypes.MonitoredTxStatusSent:
		return WIPClaimStatus, nil
	case ethtxtypes.MonitoredTxStatusFailed:
		return FailedClaimStatus, nil
	case ethtxtypes.MonitoredTxStatusMined,
		ethtxtypes.MonitoredTxStatusSafe,
		ethtxtypes.MonitoredTxStatusFinalized:
		log.Infof("claim tx with id %s mined at block %d", id, res.MinedAtBlockNumber)

		return SuccessClaimStatus, nil
	default:
		return "", fmt.Errorf("unexpected tx status: %v", res.Status)
	}
}

func (c *EVMClaimSponsor) buildClaimTxData(claim *Claim) ([]byte, error) {
	// Use network IDs directly as requested

	var data []byte
	var err error

	switch claim.LeafType {
	case LeafTypeAsset:
		data, err = c.bridgeABI.Pack(
			"claimAsset",
			claim.GlobalIndex,        // uint256 globalIndex
			claim.MainnetExitRoot,    // bytes32 mainnetExitRoot
			claim.RollupExitRoot,     // bytes32 rollupExitRoot
			claim.OriginNetwork,      // uint32 originNetwork (network ID)
			claim.OriginTokenAddress, // address originTokenAddress,
			claim.DestinationNetwork, // uint32 destinationNetwork (network ID)
			claim.DestinationAddress, // address destinationAddress
			claim.Amount,             // uint256 amount
			[]byte(claim.Metadata),   // bytes metadata (convert HexBytes to []byte)
		)
	case LeafTypeMessage:
		data, err = c.bridgeABI.Pack(
			"claimMessage",
			claim.GlobalIndex,        // uint256 globalIndex
			claim.MainnetExitRoot,    // bytes32 mainnetExitRoot
			claim.RollupExitRoot,     // bytes32 rollupExitRoot
			claim.OriginNetwork,      // uint32 originNetwork (network ID)
			claim.OriginTokenAddress, // address originTokenAddress,
			claim.DestinationNetwork, // uint32 destinationNetwork (network ID)
			claim.DestinationAddress, // address destinationAddress
			claim.Amount,             // uint256 amount
			[]byte(claim.Metadata),   // bytes metadata (convert HexBytes to []byte)
		)
	default:
		return nil, fmt.Errorf("unexpected leaf type %d", claim.LeafType)
	}

	if err != nil {
		return nil, fmt.Errorf("error packing calldata: %w", err)
	}

	return data, nil
}

//go:embed polygonzkevmbridgev2_noproof.abi.json
var bridgeABIJSON []byte

func getPolygonZkEVMBridgeV2ABI() (abi.ABI, error) {
	return abi.JSON(bytes.NewReader(bridgeABIJSON))
}

func claimRootsEmpty(claim *Claim) bool {
	return claim.MainnetExitRoot == common.Hash{} && claim.RollupExitRoot == common.Hash{}
}
