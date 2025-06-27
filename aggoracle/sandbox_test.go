package aggoracle

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockChainSender implements the ChainSender interface for testing
type MockChainSender struct {
	mock.Mock
}

func (m *MockChainSender) IsGERInjected(ger common.Hash) (bool, error) {
	args := m.Called(ger)
	return args.Bool(0), args.Error(1)
}

func (m *MockChainSender) InjectGER(ctx context.Context, ger common.Hash) error {
	args := m.Called(ctx, ger)
	return args.Error(0)
}

// MockBridgeDataProvider implements BridgeDataProvider for testing
type MockBridgeDataProvider struct {
	mock.Mock
}

func (m *MockBridgeDataProvider) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	args := m.Called(ctx)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockBridgeDataProvider) GetBridges(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Bridge, error) {
	args := m.Called(ctx, fromBlock, toBlock)
	return args.Get(0).([]bridgesync.Bridge), args.Error(1)
}

func TestNewSandboxAggOracle(t *testing.T) {
	// Setup
	logger := log.GetDefaultLogger()
	mockSender := &MockChainSender{}
	
	baseOracle := &AggOracle{
		logger:            logger,
		chainSender:       mockSender,
		waitPeriodNextGER: 5 * time.Second,
	}
	
	sandboxConfig := SandboxConfig{
		Enabled:          true,
		AutoSettle:       true,
		SettlementDelay:  2 * time.Second,
		MockFinalization: true,
		InstantClaims:    true,
	}
	
	mockL1BridgeSync := &MockBridgeDataProvider{}
	mockL2BridgeSync := &MockBridgeDataProvider{}
	
	// Test
	sandboxOracle := NewSandboxAggOracle(
		baseOracle,
		sandboxConfig,
		mockL1BridgeSync,
		mockL2BridgeSync,
		logger,
	)
	
	// Assertions
	assert.NotNil(t, sandboxOracle)
	assert.Equal(t, baseOracle, sandboxOracle.AggOracle)
	assert.Equal(t, sandboxConfig, sandboxOracle.sandboxConfig)
	assert.Equal(t, mockL1BridgeSync, sandboxOracle.l1BridgeSync)
	assert.Equal(t, mockL2BridgeSync, sandboxOracle.l2BridgeSync)
	assert.True(t, sandboxOracle.IsSandboxMode())
}

func TestSandboxAggOracle_GetSandboxConfig(t *testing.T) {
	// Setup
	expectedConfig := SandboxConfig{
		Enabled:          true,
		AutoSettle:       true,
		SettlementDelay:  3 * time.Second,
		MockFinalization: true,
		InstantClaims:    true,
	}
	
	sandboxOracle := &SandboxAggOracle{
		sandboxConfig: expectedConfig,
	}
	
	// Test
	result := sandboxOracle.GetSandboxConfig()
	
	// Assertions
	assert.Equal(t, expectedConfig, result)
}

func TestSandboxAggOracle_calculateGERFromBridgeEvents(t *testing.T) {
	tests := []struct {
		name           string
		bridges        []bridgesync.Bridge
		lastBlock      uint64
		mockFinalized  bool
		expectedResult bool // whether a GER should be returned
	}{
		{
			name:           "no bridge events",
			bridges:        []bridgesync.Bridge{},
			lastBlock:      100,
			mockFinalized:  true,
			expectedResult: false,
		},
		{
			name: "single bridge event with mock finalization",
			bridges: []bridgesync.Bridge{
				{
					BlockNum:     100,
					DepositCount: 1,
					LeafType:     1,
				},
			},
			lastBlock:      100,
			mockFinalized:  true,
			expectedResult: true,
		},
		{
			name: "multiple bridge events",
			bridges: []bridgesync.Bridge{
				{
					BlockNum:     95,
					DepositCount: 1,
					LeafType:     1,
				},
				{
					BlockNum:     100,
					DepositCount: 2,
					LeafType:     1,
				},
			},
			lastBlock:      100,
			mockFinalized:  true,
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := log.GetDefaultLogger()
			mockL1BridgeSync := &MockBridgeDataProvider{}
			mockL2BridgeSync := &MockBridgeDataProvider{}
			
			sandboxConfig := SandboxConfig{
				MockFinalization: tt.mockFinalized,
			}
			
			sandboxOracle := &SandboxAggOracle{
				sandboxConfig: sandboxConfig,
				l1BridgeSync:  mockL1BridgeSync,
				l2BridgeSync:  mockL2BridgeSync,
				logger:        logger,
			}
			
			// Setup mocks
			mockL1BridgeSync.On("GetLastProcessedBlock", mock.Anything).Return(tt.lastBlock, nil)
			mockL1BridgeSync.On("GetBridges", mock.Anything, uint64(0), mock.AnythingOfType("uint64")).Return(tt.bridges, nil)
			
			// Test
			ctx := context.Background()
			result, err := sandboxOracle.calculateGERFromBridgeEvents(ctx)
			
			// Assertions
			require.NoError(t, err)
			if tt.expectedResult {
				assert.NotEqual(t, common.Hash{}, result)
			} else {
				assert.Equal(t, common.Hash{}, result)
			}
			
			mockL1BridgeSync.AssertExpectations(t)
		})
	}
}

func TestSandboxAggOracle_simulateGERCalculation(t *testing.T) {
	tests := []struct {
		name           string
		bridge         bridgesync.Bridge
		mockFinalized  bool
		expectedResult common.Hash
	}{
		{
			name: "mock finalization uses bridge hash",
			bridge: bridgesync.Bridge{
				BlockNum:     100,
				DepositCount: 1,
				LeafType:     1,
			},
			mockFinalized:  true,
			expectedResult: (&bridgesync.Bridge{BlockNum: 100, DepositCount: 1, LeafType: 1}).Hash(),
		},
		{
			name: "non-mock finalization uses deterministic calculation",
			bridge: bridgesync.Bridge{
				BlockNum:     100,
				DepositCount: 1,
				LeafType:     1,
			},
			mockFinalized:  false,
			expectedResult: common.BytesToHash([]byte("sandbox-ger-1-100")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			sandboxConfig := SandboxConfig{
				MockFinalization: tt.mockFinalized,
			}
			
			sandboxOracle := &SandboxAggOracle{
				sandboxConfig: sandboxConfig,
			}
			
			// Test
			result := sandboxOracle.simulateGERCalculation(tt.bridge)
			
			// Assertions
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestSandboxAggOracle_processLatestGERSandbox(t *testing.T) {
	tests := []struct {
		name          string
		bridges       []bridgesync.Bridge
		isInjected    bool
		autoSettle    bool
		delay         time.Duration
		expectInject  bool
	}{
		{
			name:         "no bridge events - no injection",
			bridges:      []bridgesync.Bridge{},
			isInjected:   false,
			autoSettle:   true,
			delay:        0,
			expectInject: false,
		},
		{
			name: "bridge event, not injected - should inject",
			bridges: []bridgesync.Bridge{
				{
					BlockNum:     100,
					DepositCount: 1,
					LeafType:     1,
				},
			},
			isInjected:   false,
			autoSettle:   true,
			delay:        0,
			expectInject: true,
		},
		{
			name: "bridge event, already injected - no injection",
			bridges: []bridgesync.Bridge{
				{
					BlockNum:     100,
					DepositCount: 1,
					LeafType:     1,
				},
			},
			isInjected:   true,
			autoSettle:   true,
			delay:        0,
			expectInject: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := log.GetDefaultLogger()
			mockSender := &MockChainSender{}
			mockL1BridgeSync := &MockBridgeDataProvider{}
			mockL2BridgeSync := &MockBridgeDataProvider{}
			
			baseOracle := &AggOracle{
				logger:      logger,
				chainSender: mockSender,
			}
			
			sandboxConfig := SandboxConfig{
				AutoSettle:       tt.autoSettle,
				SettlementDelay:  tt.delay,
				MockFinalization: true,
			}
			
			sandboxOracle := &SandboxAggOracle{
				AggOracle:     baseOracle,
				sandboxConfig: sandboxConfig,
				l1BridgeSync:  mockL1BridgeSync,
				l2BridgeSync:  mockL2BridgeSync,
				logger:        logger,
			}
			
			// Setup mocks
			mockL1BridgeSync.On("GetLastProcessedBlock", mock.Anything).Return(uint64(100), nil)
			mockL1BridgeSync.On("GetBridges", mock.Anything, uint64(0), mock.AnythingOfType("uint64")).Return(tt.bridges, nil)
			
			if len(tt.bridges) > 0 {
				expectedGER := tt.bridges[len(tt.bridges)-1].Hash()
				mockSender.On("IsGERInjected", expectedGER).Return(tt.isInjected, nil)
				
				if tt.expectInject {
					mockSender.On("InjectGER", mock.Anything, expectedGER).Return(nil)
				}
			}
			
			// Test
			ctx := context.Background()
			err := sandboxOracle.processLatestGERSandbox(ctx)
			
			// Assertions
			require.NoError(t, err)
			mockL1BridgeSync.AssertExpectations(t)
			mockSender.AssertExpectations(t)
		})
	}
} 