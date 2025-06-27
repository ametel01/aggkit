package aggoracle

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
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
	
	// Get the latest bridge events to calculate GER
	gerToInject, err := s.calculateGERFromBridgeEvents(ctx)
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

// calculateGERFromBridgeEvents calculates the Global Exit Root based on bridge events
// This bypasses the AggLayer and directly monitors L1 bridge events
func (s *SandboxAggOracle) calculateGERFromBridgeEvents(ctx context.Context) (common.Hash, error) {
	// Get the latest processed block from L1 bridge sync
	lastProcessedBlock, err := s.l1BridgeSync.GetLastProcessedBlock(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to get last processed block: %w", err)
	}

	// Get recent bridge events to calculate the latest GER
	// In sandbox mode, we simplify this by getting the latest exit root from the bridge
	latestRoot, err := s.getLatestExitRootFromBridge(ctx, lastProcessedBlock)
	if err != nil {
		return common.Hash{}, err
	}

	return latestRoot, nil
}

// getLatestExitRootFromBridge retrieves the latest exit root from bridge events
func (s *SandboxAggOracle) getLatestExitRootFromBridge(ctx context.Context, blockNum uint64) (common.Hash, error) {
	// In sandbox mode, we use mock finalization to get immediate access to latest data
	if s.sandboxConfig.MockFinalization {
		// Use latest block instead of waiting for finalization
		blockNum = 0 // 0 means latest block in the bridge sync context
	}

	// Get the latest bridge events up to the specified block
	bridges, err := s.l1BridgeSync.GetBridges(ctx, 0, blockNum)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to get bridges: %w", err)
	}

	// If no bridges found, return zero hash
	if len(bridges) == 0 {
		s.logger.Debug("No bridge events found")
		return common.Hash{}, nil
	}

	// Get the latest bridge event
	latestBridge := bridges[len(bridges)-1]
	
	// In a real implementation, this would calculate the GER based on the bridge state
	// For sandbox mode, we simulate this by using the bridge's local exit root
	// This is a simplified approach for development purposes
	ger := s.simulateGERCalculation(latestBridge)
	
	s.logger.Debugf("Calculated GER from bridge events: %s (from %d bridge events)", ger.Hex(), len(bridges))
	return ger, nil
}

// simulateGERCalculation simulates the Global Exit Root calculation
// In production, this would involve complex merkle tree calculations with AggLayer
// For sandbox mode, we simplify this to enable local development
func (s *SandboxAggOracle) simulateGERCalculation(latestBridge bridgesync.Bridge) common.Hash {
	// Simplified GER calculation for sandbox mode
	// In production, this would involve:
	// 1. Getting the latest L1 info tree root
	// 2. Getting the latest L2 exit tree root  
	// 3. Calculating the combined GER using both roots
	
	// For sandbox, we use a deterministic hash based on the bridge data
	// This ensures consistency while bypassing complex AggLayer integration
	
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