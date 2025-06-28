package config

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxConfig_IsSandboxMode(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected bool
	}{
		{
			name: "sandbox enabled",
			config: Config{
				Sandbox: SandboxConfig{
					Enabled: true,
				},
			},
			expected: true,
		},
		{
			name: "sandbox disabled",
			config: Config{
				Sandbox: SandboxConfig{
					Enabled: false,
				},
			},
			expected: false,
		},
		{
			name:     "default config",
			config:   Config{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.IsSandboxMode())
		})
	}
}

func TestSandboxConfig_ValidateSandboxConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid sandbox config",
			config: Config{
				Sandbox: SandboxConfig{
					Enabled:          true,
					AutoSettle:       true,
					SettlementDelay:  types.NewDuration(5 * time.Second),
					MockFinalization: true,
					InstantClaims:    true,
					L1Node: SandboxNodeConfig{
						URL:     "http://localhost:8545",
						ChainID: 31337,
					},
					L2Node: SandboxNodeConfig{
						URL:     "http://localhost:8546",
						ChainID: 31338,
					},
				},
			},
			expectError: false,
		},
		{
			name: "sandbox disabled - no validation needed",
			config: Config{
				Sandbox: SandboxConfig{
					Enabled: false,
				},
			},
			expectError: false,
		},
		{
			name: "missing L1 URL",
			config: Config{
				Sandbox: SandboxConfig{
					Enabled: true,
					L1Node: SandboxNodeConfig{
						URL:     "",
						ChainID: 31337,
					},
					L2Node: SandboxNodeConfig{
						URL:     "http://localhost:8546",
						ChainID: 31338,
					},
				},
			},
			expectError: true,
			errorMsg:    "sandbox L1 node URL cannot be empty",
		},
		{
			name: "same chain IDs",
			config: Config{
				Sandbox: SandboxConfig{
					Enabled: true,
					L1Node: SandboxNodeConfig{
						URL:     "http://localhost:8545",
						ChainID: 31337,
					},
					L2Node: SandboxNodeConfig{
						URL:     "http://localhost:8546",
						ChainID: 31337, // Same as L1
					},
				},
			},
			expectError: true,
			errorMsg:    "sandbox L1 and L2 nodes must have different chain IDs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.ValidateSandboxConfig()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
