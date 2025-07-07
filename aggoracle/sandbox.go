package aggoracle

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// SandboxConfig represents the configuration for sandbox mode operation
type SandboxConfig struct {
	Enabled          bool
	AutoSettle       bool
	SettlementDelay  time.Duration
	MockFinalization bool
	InstantClaims    bool
}

// BridgeDataProvider defines the interface for bridge data access
type BridgeDataProvider interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
	GetBridges(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Bridge, error)
}

// SandboxAggOracle wraps the standard AggOracle with sandbox functionality
// It bypasses AggLayer integration and directly calculates GER from bridge events
type SandboxAggOracle struct {
	*AggOracle
	sandboxConfig SandboxConfig
	l1BridgeSync  BridgeDataProvider
	l2BridgeSync  BridgeDataProvider
	logger        *log.Logger
}

// NewSandboxAggOracle creates a new sandbox-enabled AggOracle
func NewSandboxAggOracle(
	baseOracle *AggOracle,
	sandboxConfig SandboxConfig,
	l1BridgeSync BridgeDataProvider,
	l2BridgeSync BridgeDataProvider,
	logger *log.Logger,
) *SandboxAggOracle {
	return &SandboxAggOracle{
		AggOracle:     baseOracle,
		sandboxConfig: sandboxConfig,
		l1BridgeSync:  l1BridgeSync,
		l2BridgeSync:  l2BridgeSync,
		logger:        logger,
	}
}

// Start overrides the standard AggOracle start method with sandbox logic
func (s *SandboxAggOracle) Start(ctx context.Context) {
	s.logger.Info("Starting AggOracle in sandbox mode")

	ticker := time.NewTicker(s.waitPeriodNextGER)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.processLatestGERSandbox(ctx); err != nil {
				s.logger.Errorf("Error processing GER in sandbox mode: %v", err)
			}

		case <-ctx.Done():
			s.logger.Info("Sandbox AggOracle context cancelled, stopping")
			return
		}
	}
}

// processLatestGERSandbox implements the core sandbox logic for GER processing
func (s *SandboxAggOracle) processLatestGERSandbox(ctx context.Context) error {
	s.logger.Debug("Processing latest GER in sandbox mode")

	// Calculate GER based on the latest exit-roots from *both* bridges (L1 & L2)
	gerToInject, err := s.calculateGERFromExitRoots(ctx)
	if err != nil {
		return fmt.Errorf("failed to calculate GER from bridge events: %w", err)
	}

	// If no new GER to inject, return
	if gerToInject == (common.Hash{}) {
		s.logger.Debug("No new GER to inject")
		return nil
	}

	// Check if GER is already injected
	isGERInjected, err := s.chainSender.IsGERInjected(gerToInject)
	if err != nil {
		return fmt.Errorf("error checking if GER is already injected: %w", err)
	}

	if isGERInjected {
		s.logger.Debugf("GER %s is already injected", gerToInject.Hex())
		return nil
	}

	// Apply settlement delay if configured
	if s.sandboxConfig.SettlementDelay > 0 && s.sandboxConfig.AutoSettle {
		s.logger.Debugf("Waiting %v before settling GER %s", s.sandboxConfig.SettlementDelay, gerToInject.Hex())
		select {
		case <-time.After(s.sandboxConfig.SettlementDelay):
			// Continue with injection
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Inject the GER
	s.logger.Infof("Injecting new GER in sandbox mode: %s", gerToInject.Hex())
	if err := s.chainSender.InjectGER(ctx, gerToInject); err != nil {
		return fmt.Errorf("error injecting GER %s: %w", gerToInject.Hex(), err)
	}

	s.logger.Infof("GER %s injected successfully in sandbox mode", gerToInject.Hex())
	return nil
}

// calculateGERFromExitRoots returns the Global Exit Root as keccak256(mainnetExitRoot, rollupExitRoot).
// mainnetExitRoot  – last exit-root of the *L1* bridge (bridges from / to mainnet)
// rollupExitRoot   – last exit-root of the *L2* bridge (bridges from / to the rollup we are running on)
func (s *SandboxAggOracle) calculateGERFromExitRoots(ctx context.Context) (common.Hash, error) {
	// First, try to get the latest GER from L1InfoTree if available
	if s.AggOracle.l1Info != nil {
		latestInfo, err := s.AggOracle.l1Info.GetLastInfo()
		if err == nil {
			s.logger.Debugf("Using GER from L1InfoTree: %s (MainnetExitRoot: %s, RollupExitRoot: %s)",
				latestInfo.GlobalExitRoot.Hex(), latestInfo.MainnetExitRoot.Hex(), latestInfo.RollupExitRoot.Hex())
			return latestInfo.GlobalExitRoot, nil
		}
		s.logger.Debugf("Could not get latest L1InfoTree data, falling back to bridge calculation: %v", err)
	}

	getLastRoot := func(p BridgeDataProvider) (common.Hash, error) {
		// Get last processed block for this provider
		lastBlock, err := p.GetLastProcessedBlock(ctx)
		if err != nil {
			return common.Hash{}, err
		}

		// Fetch all bridge events up to the last processed block – we only need the most recent one.
		bridges, err := p.GetBridges(ctx, 0, lastBlock)
		if err != nil {
			return common.Hash{}, err
		}
		if len(bridges) == 0 {
			// No deposits yet – the exit-root is zero.
			return common.Hash{}, nil
		}

		latest := bridges[len(bridges)-1]

		// Try to obtain the exit-root from the BridgeSync implementation if available.
		// This gives the *state root* (root of the exit tree) instead of a single leaf hash.
		if bs, ok := p.(*bridgesync.BridgeSync); ok {
			root, err := bs.GetExitRootByIndex(ctx, latest.DepositCount)
			if err == nil {
				return root.Hash, nil
			}
			// Fall back to leaf hash on error but still log it.
			s.logger.Debugf("fallback to leaf hash for exit-root (deposit %d): %v", latest.DepositCount, err)
		}

		// Fallback: use the leaf hash itself (not perfect but better than zero).
		return latest.Hash(), nil
	}

	mainnetExitRoot, err := getLastRoot(s.l1BridgeSync)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to get mainnet exit root: %w", err)
	}

	rollupExitRoot, err := getLastRoot(s.l2BridgeSync)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to get rollup exit root: %w", err)
	}

	ger := crypto.Keccak256Hash(mainnetExitRoot.Bytes(), rollupExitRoot.Bytes())
	s.logger.Debugf("Computed GER – mainnetExitRoot:%s rollupExitRoot:%s => GER:%s",
		mainnetExitRoot.Hex(), rollupExitRoot.Hex(), ger.Hex())

	return ger, nil
}

// calculateGERFromBridgeEvents is kept only to avoid breaking existing consumers/tests.
// It now acts as a thin wrapper around calculateGERFromExitRoots.
func (s *SandboxAggOracle) calculateGERFromBridgeEvents(ctx context.Context) (common.Hash, error) {
	return s.calculateGERFromExitRoots(ctx)
}

// simulateGERCalculation simulates the Global Exit Root calculation
// In production, this would involve complex merkle tree calculations with AggLayer
// For sandbox mode, we simplify this to enable local development
func (s *SandboxAggOracle) simulateGERCalculation(latestBridge bridgesync.Bridge) common.Hash {
	// In sandbox mode, we need to calculate GER using the proper formula:
	// GER = keccak256(abi.encodePacked(mainnetExitRoot, rollupExitRoot))

	// For sandbox mode, we'll use the L1 info tree data to get the proper roots
	// This ensures compatibility with the bridge contract's expectations

	ctx := context.Background()

	// Get the latest L1 info tree leaf to extract mainnet and rollup exit roots
	l1InfoLeaf, err := s.AggOracle.l1Info.GetLatestInfoUntilBlock(ctx, latestBridge.BlockNum)
	if err != nil {
		// Fallback: if we can't get L1 info, use a deterministic calculation
		s.logger.Warnf("Failed to get L1 info tree leaf for block %d: %v. Using fallback calculation.", latestBridge.BlockNum, err)
		return s.fallbackGERCalculation(latestBridge)
	}

	// Calculate GER using the proper formula: keccak256(abi.encodePacked(mainnetExitRoot, rollupExitRoot))
	ger := s.calculateGERFromRoots(l1InfoLeaf.MainnetExitRoot, l1InfoLeaf.RollupExitRoot)

	s.logger.Debugf("Calculated GER from L1 info tree: %s (mainnetExitRoot: %s, rollupExitRoot: %s)",
		ger.Hex(), l1InfoLeaf.MainnetExitRoot.Hex(), l1InfoLeaf.RollupExitRoot.Hex())

	return ger
}

// calculateGERFromRoots calculates the GER using the standard formula
func (s *SandboxAggOracle) calculateGERFromRoots(mainnetExitRoot, rollupExitRoot common.Hash) common.Hash {
	// This is the standard GER calculation used throughout the Polygon ecosystem
	return crypto.Keccak256Hash(mainnetExitRoot.Bytes(), rollupExitRoot.Bytes())
}

// fallbackGERCalculation provides a fallback when L1 info tree data is not available
func (s *SandboxAggOracle) fallbackGERCalculation(latestBridge bridgesync.Bridge) common.Hash {
	// In sandbox mode, if we can't get proper L1 info tree data,
	// we'll create a deterministic GER based on bridge state
	// This maintains consistency while being different from production

	if s.sandboxConfig.MockFinalization {
		// Use the bridge's hash as a simplified GER
		// This provides a consistent hash that changes with bridge state
		return latestBridge.Hash()
	}

	// If not mocking finalization, still provide a deterministic result
	// based on the bridge deposit count and block number
	return common.BytesToHash([]byte(fmt.Sprintf("sandbox-ger-%d-%d",
		latestBridge.DepositCount, latestBridge.BlockNum)))
}

// IsSandboxMode returns true if this oracle is running in sandbox mode
func (s *SandboxAggOracle) IsSandboxMode() bool {
	return true
}

// GetSandboxConfig returns the sandbox configuration
func (s *SandboxAggOracle) GetSandboxConfig() SandboxConfig {
	return s.sandboxConfig
}
