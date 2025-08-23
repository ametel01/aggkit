package bridgesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/bridgel2sovereignchain"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	rpctypes "github.com/0xPolygon/cdk-rpc/types"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/golang-collections/collections/stack"
)

var (
	// non-sovereign chain contract events
	bridgeEventSignature = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))
	claimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	tokenMappingEventSignature  = crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))

	// sovereign chain contract events
	setSovereignTokenEventSignature = crypto.Keccak256Hash([]byte(
		"SetSovereignTokenAddress(uint32,address,address,bool)",
	))
	migrateLegacyTokenEventSignature = crypto.Keccak256Hash([]byte(
		"MigrateLegacyToken(address,address,address,uint256)",
	))
	removeLegacySovereignTokenEventSignature = crypto.Keccak256Hash([]byte(
		"RemoveLegacySovereignTokenAddress(address)",
	))

	claimAssetEtrogMethodID      = common.Hex2Bytes("1b260e3b")
	claimMessageEtrogMethodID    = common.Hex2Bytes("af837c76")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")
	zeroAddress                  = common.HexToAddress("0x0")
)

const (
	// debugTraceTxEndpoint is the name of the debug method used to trace a transaction.
	debugTraceTxEndpoint = "debug_traceTransaction"

	// callTracerType is the name of the call tracer
	callTracerType = "callTracer"

	// methodIDLength is the length of the method ID in bytes
	methodIDLength = 4

	// Chain ID constants
	chainIDEthereum = 1    // L1 Ethereum
	chainIDL2       = 1101 // L2 chain ID
	chainIDL3       = 137  // L3 chain ID

	// Network ID constants for multi-L2 setup
	networkIDMainnet = 0
	networkIDL2      = 1
	networkIDL3      = 2
)

// getDestinationNetwork maps chain ID to network ID for destination network
func getDestinationNetwork(chainID uint64) uint32 {
	// Map chain IDs to network IDs for multi-L2 setup
	switch chainID {
	case chainIDEthereum:
		return networkIDMainnet
	case chainIDL2:
		return networkIDL2
	case chainIDL3:
		return networkIDL3
	default:
		// Fallback: use chain ID as network ID for unknown chains
		return uint32(chainID)
	}
}

func buildAppender(
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	syncFullClaims bool,
	bridgeContractV2 *polygonzkevmbridgev2.Polygonzkevmbridgev2,
	logger *logger.Logger,
	syncerType BridgeSyncerType,
) (sync.LogAppenderMap, error) {
	bridgeContractV1, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create PolygonZkEVMBridge SC binding (bridge addr: %s): %w", bridgeAddr, err)
	}

	bridgeSovereignChain, err := bridgel2sovereignchain.NewBridgel2sovereignchain(bridgeAddr, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create BridgeL2SovereignChain SC binding (bridge addr: %s): %w",
			bridgeAddr, err)
	}

	gasTokenAddress, err := bridgeContractV2.GasTokenAddress(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas token address: %w", err)
	}

	appender := make(sync.LogAppenderMap)

	// Add event handlers for the bridge contract
	appender[bridgeEventSignature] = buildBridgeEventHandler(
		bridgeContractV2, client, bridgeAddr, gasTokenAddress, logger)
	appender[claimEventSignature] = buildClaimEventHandler(
		bridgeContractV2, client, bridgeAddr, syncFullClaims, logger, syncerType)
	appender[claimEventSignaturePreEtrog] = buildClaimEventHandlerPreEtrog(
		bridgeContractV1, client,
		bridgeAddr, syncFullClaims, logger, syncerType)
	appender[tokenMappingEventSignature] = buildTokenMappingHandler(
		bridgeContractV2, client, bridgeAddr, logger)
	appender[setSovereignTokenEventSignature] = buildSetSovereignTokenHandler(
		bridgeSovereignChain, client, bridgeAddr, logger)
	appender[migrateLegacyTokenEventSignature] = buildMigrateLegacyTokenHandler(
		bridgeSovereignChain, client, bridgeAddr, logger)
	appender[removeLegacySovereignTokenEventSignature] = buildRemoveLegacyTokenHandler(
		bridgeSovereignChain)

	return appender, nil
}

// buildBridgeEventHandler creates a handler for the Bridge event log.
func buildBridgeEventHandler(contract *polygonzkevmbridgev2.Polygonzkevmbridgev2,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address, gasTokenAddress common.Address, logger *logger.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		bridgeEvent, err := contract.ParseBridgeEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing BridgeEvent log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash, logger)
		if err != nil {
			return fmt.Errorf("failed to extract bridge event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		isNativeToken := bridgeEvent.OriginAddress == gasTokenAddress || bridgeEvent.OriginAddress == zeroAddress

		b.Events = append(b.Events, Event{Bridge: &Bridge{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			FromAddress:        foundCall.From,
			BridgeTxHash:       l.TxHash,
			Calldata:           foundCall.Input,
			BlockTimestamp:     b.Timestamp,
			LeafType:           bridgeEvent.LeafType,
			OriginNetwork:      bridgeEvent.OriginNetwork,
			OriginAddress:      bridgeEvent.OriginAddress,
			DestinationNetwork: bridgeEvent.DestinationNetwork,
			DestinationAddress: bridgeEvent.DestinationAddress,
			Amount:             bridgeEvent.Amount,
			Metadata:           bridgeEvent.Metadata,
			DepositCount:       bridgeEvent.DepositCount,
			IsNativeToken:      isNativeToken,
		}})
		return nil
	}
}

// buildClaimEventHandler creates a handler for the Claim event log.
func buildClaimEventHandler(contract *polygonzkevmbridgev2.Polygonzkevmbridgev2,
	client aggkittypes.EthClienter, bridgeAddr common.Address, syncFullClaims bool, logger *logger.Logger,
	_ BridgeSyncerType,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing Claim event log %+v: %w", l, err)
		}

		// Determine destination network (chain ID) based on the processing chain
		var destinationNetwork uint32
		chainID, err := client.ChainID(context.Background())
		if err != nil {
			logger.Warnf("failed to get chain ID, defaulting to chain ID 1: %v", err)
			destinationNetwork = 1
		} else {
			// Use chain ID as destination network
			destinationNetwork = getDestinationNetwork(chainID.Uint64())
		}

		// Calculate the proper global index for post-Etrog claims
		// The claimEvent.GlobalIndex is actually the deposit count, not the global index
		mainnetFlag := claimEvent.OriginNetwork == 0
		rollupIndex := claimEvent.OriginNetwork
		if mainnetFlag {
			rollupIndex = 0
		}
		globalIndex := aggkitcommon.GenerateGlobalIndex(
			mainnetFlag,
			rollupIndex,
			uint32(claimEvent.GlobalIndex.Uint64()),
		)

		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			GlobalIndex:        globalIndex,
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			DestinationNetwork: destinationNetwork,
			Amount:             claimEvent.Amount,
			BlockTimestamp:     b.Timestamp,
			BridgeTxHash:       common.Hash{}, // Will be populated by processor
			ClaimTxHash:        l.TxHash,
			FromAddress:        l.Address,
		}

		if syncFullClaims {
			if err := claim.setClaimCalldata(client, bridgeAddr, l.TxHash, logger); err != nil {
				return err
			}
		}

		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildClaimEventHandlerPreEtrog creates a handler for the Claim event log for pre-Etrog contracts.
func buildClaimEventHandlerPreEtrog(contract *polygonzkevmbridge.Polygonzkevmbridge,
	client aggkittypes.EthClienter, bridgeAddr common.Address, syncFullClaims bool, logger *logger.Logger,
	_ BridgeSyncerType,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing Claim event log %+v: %w", l, err)
		}

		// Determine destination network (chain ID) based on the processing chain
		var destinationNetwork uint32
		chainID, err := client.ChainID(context.Background())
		if err != nil {
			logger.Warnf("failed to get chain ID, defaulting to chain ID 1: %v", err)
			destinationNetwork = 1
		} else {
			// Use chain ID as destination network
			destinationNetwork = getDestinationNetwork(chainID.Uint64())
		}

		// Calculate the proper global index for pre-Etrog claims
		// The claimEvent.Index is actually the deposit count, not the global index
		mainnetFlag := claimEvent.OriginNetwork == 0
		rollupIndex := claimEvent.OriginNetwork
		if mainnetFlag {
			rollupIndex = 0
		}
		globalIndex := aggkitcommon.GenerateGlobalIndex(mainnetFlag, rollupIndex, claimEvent.Index)

		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			GlobalIndex:        globalIndex,
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			DestinationNetwork: destinationNetwork,
			Amount:             claimEvent.Amount,
			BridgeTxHash:       common.Hash{}, // Will be populated by processor
			ClaimTxHash:        l.TxHash,
			BlockTimestamp:     b.Timestamp,
			FromAddress:        l.Address,
		}

		if syncFullClaims {
			if err := claim.setClaimCalldata(client, bridgeAddr, l.TxHash, logger); err != nil {
				return err
			}
		}

		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildTokenMappingHandler creates a handler for the NewWrappedToken event log.
//
//nolint:dupl
func buildTokenMappingHandler(contract *polygonzkevmbridgev2.Polygonzkevmbridgev2,
	client aggkittypes.EthClienter, bridgeAddr common.Address, logger *logger.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		tokenMappingEvent, err := contract.ParseNewWrappedToken(l)
		if err != nil {
			return fmt.Errorf("error parsing NewWrappedToken event log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash, logger)
		if err != nil {
			return fmt.Errorf("failed to extract the NewWrappedToken event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{TokenMapping: &TokenMapping{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			OriginNetwork:       tokenMappingEvent.OriginNetwork,
			OriginTokenAddress:  tokenMappingEvent.OriginTokenAddress,
			WrappedTokenAddress: tokenMappingEvent.WrappedTokenAddress,
			Metadata:            tokenMappingEvent.Metadata,
			Calldata:            foundCall.Input,
			Type:                bridgetypes.WrappedToken,
		}})
		return nil
	}
}

// buildSetSovereignTokenHandler creates a handler for the SetSovereignTokenAddress event log.
//
//nolint:dupl
func buildSetSovereignTokenHandler(contract *bridgel2sovereignchain.Bridgel2sovereignchain,
	client aggkittypes.EthClienter, bridgeAddr common.Address, logger *logger.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseSetSovereignTokenAddress(l)
		if err != nil {
			return fmt.Errorf("error parsing SetSovereignTokenAddress event log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash, logger)
		if err != nil {
			return fmt.Errorf(
				"failed to extract the SetSovereignTokenAddress event calldata (tx hash: %s): %w",
				l.TxHash,
				err,
			)
		}

		b.Events = append(b.Events, Event{TokenMapping: &TokenMapping{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			OriginNetwork:       event.OriginNetwork,
			OriginTokenAddress:  event.OriginTokenAddress,
			WrappedTokenAddress: event.SovereignTokenAddress,
			IsNotMintable:       event.IsNotMintable,
			Calldata:            foundCall.Input,
			Type:                bridgetypes.SovereignToken,
		}})
		return nil
	}
}

// buildMigrateLegacyTokenHandler creates a handler for the MigrateLegacyToken event log.
func buildMigrateLegacyTokenHandler(contract *bridgel2sovereignchain.Bridgel2sovereignchain,
	client aggkittypes.EthClienter, bridgeAddr common.Address, logger *logger.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseMigrateLegacyToken(l)
		if err != nil {
			return fmt.Errorf("error parsing MigrateLegacyToken event log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash, logger)
		if err != nil {
			return fmt.Errorf(
				"failed to extract the MigrateLegacyToken event calldata (tx hash: %s): %w",
				l.TxHash,
				err,
			)
		}

		b.Events = append(b.Events, Event{LegacyTokenMigration: &LegacyTokenMigration{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			Sender:              event.Sender,
			LegacyTokenAddress:  event.LegacyTokenAddress,
			UpdatedTokenAddress: event.UpdatedTokenAddress,
			Amount:              event.Amount,
			Calldata:            foundCall.Input,
		}})
		return nil
	}
}

// buildRemoveLegacyTokenHandler creates a handler for the RemoveLegacySovereignTokenAddress event log.
func buildRemoveLegacyTokenHandler(contract *bridgel2sovereignchain.Bridgel2sovereignchain) func(*sync.EVMBlock,
	types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseRemoveLegacySovereignTokenAddress(l)
		if err != nil {
			return fmt.Errorf("error parsing RemoveLegacySovereignTokenAddress event log %+v: %w", l, err)
		}

		b.Events = append(b.Events, Event{RemoveLegacyToken: &RemoveLegacyToken{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			LegacyTokenAddress: event.SovereignTokenAddress,
		}})
		return nil
	}
}

type call struct {
	From  common.Address    `json:"from"`
	To    common.Address    `json:"to"`
	Value *rpctypes.ArgBig  `json:"value"`
	Err   *string           `json:"error"`
	Input rpctypes.ArgBytes `json:"input"`
	Calls []call            `json:"calls"`
}

type tracerCfg struct {
	Tracer string `json:"tracer"`
}

// findCall traverses the call trace using DFS and either returns the call or stops when a callback succeeds.
func findCall(rootCall call, targetAddr common.Address, callback func(call) (bool, error), logger *logger.Logger,
) (*call, error) {
	callStack := stack.New()
	callStack.Push(rootCall)

	for callStack.Len() > 0 {
		currentCallInterface := callStack.Pop()
		currentCall, ok := currentCallInterface.(call)
		if !ok {
			return nil, fmt.Errorf("unexpected type for 'currentCall'. Expected 'call', got '%T'", currentCallInterface)
		}

		// Skip reverted calls
		if currentCall.Err != nil {
			logger.Debugf("skipping reverted call to %s from %s: %s",
				currentCall.To.Hex(), currentCall.From.Hex(), *currentCall.Err)
			continue
		}

		if currentCall.To == targetAddr {
			if callback != nil {
				found, err := callback(currentCall)
				if err != nil {
					return nil, err
				}
				if found {
					return &currentCall, nil
				}
			} else {
				return &currentCall, nil
			}
		}

		// Add non-reverted calls to the stack
		for _, c := range currentCall.Calls {
			if c.Err == nil {
				callStack.Push(c)
			} else {
				logger.Debugf("skipping reverted nested call to %s from %s: %s",
					c.To.Hex(), c.From.Hex(), *c.Err)
			}
		}
	}
	return nil, db.ErrNotFound
}

// extractCall tries to extract the call for the transaction identified by transaction hash.
// It relies on debug_traceTransaction JSON RPC function.
func extractCall(client aggkittypes.RPCClienter, contractAddr common.Address, txHash common.Hash, logger *logger.Logger,
) (*call, error) {
	c := &call{To: contractAddr}
	err := client.Call(c, debugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return nil, err
	}

	return findCall(*c, contractAddr, nil, logger)
}

// setClaimCalldata traces the transaction to find and decode calldata for the given bridge address.
//
// Parameters:
// - client: RPC client to fetch the transaction trace.
// - bridge: Target contract address.
// - txHash: Transaction hash to trace.
// - logger: Logger instance for debug logging.
//
// Returns an error if tracing fails. Returns nil if no valid claim calldata is found (this is normal).
func (c *Claim) setClaimCalldata(
	client aggkittypes.RPCClienter,
	bridge common.Address,
	txHash common.Hash,
	logger *logger.Logger,
) error {
	callFrame := &call{}
	err := client.Call(callFrame, debugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return err
	}

	// Check if the root call was successful
	if callFrame.Err != nil {
		return fmt.Errorf("root call reverted: %s", *callFrame.Err)
	}

	_, err = findCall(*callFrame, bridge,
		func(call call) (bool, error) {
			// Skip reverted calls
			if call.Err != nil {
				return false, nil
			}
			return c.tryDecodeClaimCalldata(call.From, call.Input)
		}, logger)

	// If no valid claim calldata is found, this is normal - don't treat it as an error
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}

	return err
}

// tryDecodeClaimCalldata attempts to find and decode the claim calldata from the provided input bytes.
// It checks if the method ID corresponds to either the claim asset or claim message methods.
// If a match is found, it decodes the calldata using the ABI of the bridge contract and updates the claim object.
// Returns true if the calldata is successfully decoded and matches the expected format, otherwise returns false.
func (c *Claim) tryDecodeClaimCalldata(senderAddr common.Address, input []byte) (bool, error) {
	if len(input) < methodIDLength {
		return false, fmt.Errorf("input too short: %d bytes", len(input))
	}
	methodID := input[:methodIDLength]
	switch {
	case bytes.Equal(methodID, claimAssetEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessageEtrogMethodID):
		bridgeV2ABI, err := polygonzkevmbridgev2.Polygonzkevmbridgev2MetaData.GetAbi()
		if err != nil {
			return false, err
		}
		// Recover Method from signature and ABI
		method, err := bridgeV2ABI.MethodById(methodID)
		if err != nil {
			// Handle case where method ID doesn't exist in ABI
			// This can happen when contract method signatures don't match Go bindings
			// We'll try to decode manually using the known signature parameters
			found, err := c.decodeEtrogCalldataManual(senderAddr, input[methodIDLength:])
			if err != nil {
				return false, fmt.Errorf("failed to manually decode calldata for method 0x%x: %w", methodID, err)
			}
			if found {
				c.IsMessage = bytes.Equal(methodID, claimMessageEtrogMethodID)
			}
			return found, nil
		}

		data, err := method.Inputs.Unpack(input[methodIDLength:])
		if err != nil {
			return false, err
		}

		found, err := c.decodeEtrogCalldata(senderAddr, data)
		if err != nil {
			return false, err
		}

		if found {
			c.IsMessage = bytes.Equal(methodID, claimMessageEtrogMethodID)
		}

		return found, nil

	case bytes.Equal(methodID, claimAssetPreEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessagePreEtrogMethodID):
		bridgeABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
		if err != nil {
			return false, err
		}

		// Recover Method from signature and ABI
		method, err := bridgeABI.MethodById(methodID)
		if err != nil {
			return false, err
		}

		data, err := method.Inputs.Unpack(input[methodIDLength:])
		if err != nil {
			return false, err
		}

		found, err := c.decodePreEtrogCalldata(senderAddr, data)
		if err != nil {
			return false, err
		}

		if found {
			c.IsMessage = bytes.Equal(methodID, claimMessagePreEtrogMethodID)
		}

		return found, nil

	default:
		// Return false instead of an error for unrecognized method IDs
		// This allows the claim processing to continue without failing
		return false, nil
	}
}
