package config

import (
	"fmt"
	"time"
)

// SandboxConfig represents the configuration for sandbox mode operation
type SandboxConfig struct {
	// Enabled determines if sandbox mode is active
	Enabled bool `mapstructure:"Enabled"`
	
	// AutoSettle automatically settles bridge operations without AggLayer
	AutoSettle bool `mapstructure:"AutoSettle"`
	
	// SettlementDelay adds configurable delay to simulate real-world timing
	SettlementDelay time.Duration `mapstructure:"SettlementDelay"`
	
	// MockFinalization bypasses complex finality validation
	MockFinalization bool `mapstructure:"MockFinalization"`
	
	// InstantClaims makes bridge claims immediately ready
	InstantClaims bool `mapstructure:"InstantClaims"`
	
	// L1Node configuration for sandbox L1 node
	L1Node SandboxNodeConfig `mapstructure:"L1Node"`
	
	// L2Node configuration for sandbox L2 node
	L2Node SandboxNodeConfig `mapstructure:"L2Node"`
}

// SandboxNodeConfig represents configuration for a sandbox node
type SandboxNodeConfig struct {
	// URL is the RPC endpoint for the node
	URL string `mapstructure:"URL"`
	
	// ChainID is the chain identifier
	ChainID uint64 `mapstructure:"ChainID"`
}

// IsSandboxMode returns true if sandbox mode is enabled in the configuration
func (c *Config) IsSandboxMode() bool {
	return c.Sandbox.Enabled
}

// GetSandboxConfig returns the sandbox configuration
func (c *Config) GetSandboxConfig() SandboxConfig {
	return c.Sandbox
}

// ValidateSandboxConfig validates the sandbox configuration
func (c *Config) ValidateSandboxConfig() error {
	if !c.Sandbox.Enabled {
		return nil // No validation needed if sandbox is disabled
	}

	// Validate L1 node configuration
	if c.Sandbox.L1Node.URL == "" {
		return fmt.Errorf("sandbox L1 node URL cannot be empty")
	}
	if c.Sandbox.L1Node.ChainID == 0 {
		return fmt.Errorf("sandbox L1 node ChainID cannot be zero")
	}

	// Validate L2 node configuration
	if c.Sandbox.L2Node.URL == "" {
		return fmt.Errorf("sandbox L2 node URL cannot be empty")
	}
	if c.Sandbox.L2Node.ChainID == 0 {
		return fmt.Errorf("sandbox L2 node ChainID cannot be zero")
	}

	// Ensure L1 and L2 have different chain IDs
	if c.Sandbox.L1Node.ChainID == c.Sandbox.L2Node.ChainID {
		return fmt.Errorf("sandbox L1 and L2 nodes must have different chain IDs")
	}

	// Validate settlement delay
	if c.Sandbox.SettlementDelay < 0 {
		return fmt.Errorf("sandbox settlement delay cannot be negative")
	}

	return nil
} 