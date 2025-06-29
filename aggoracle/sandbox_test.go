package aggoracle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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

// MockL1InfoTreer is a mock implementation of the L1InfoTreer interface
type MockL1InfoTreer struct {
	mock.Mock
}

func (m *MockL1InfoTreer) GetLatestInfoUntilBlock(ctx context.Context, blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error) {
	args := m.Called(ctx, blockNum)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*l1infotreesync.L1InfoTreeLeaf), args.Error(1)
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
		expectedResult bool
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
					BlockNum:     99,
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
			mockL1Info := &MockL1InfoTreer{}

			// Create base oracle with mock L1 info tree
			baseOracle := &AggOracle{
				l1Info: mockL1Info,
			}

			sandboxConfig := SandboxConfig{
				MockFinalization: tt.mockFinalized,
			}

			sandboxOracle := &SandboxAggOracle{
				AggOracle:     baseOracle,
				sandboxConfig: sandboxConfig,
				l1BridgeSync:  mockL1BridgeSync,
				l2BridgeSync:  mockL2BridgeSync,
				logger:        logger,
			}

			// Setup bridge data mocks
			if tt.mockFinalized {
				// With MockFinalization, GetLastProcessedBlock is called twice:
				// 1. In calculateGERFromBridgeEvents
				// 2. In getLatestExitRootFromBridge
				mockL1BridgeSync.On("GetLastProcessedBlock", mock.Anything).Return(tt.lastBlock, nil).Twice()
			} else {
				// Without MockFinalization, it's only called once in calculateGERFromBridgeEvents
				mockL1BridgeSync.On("GetLastProcessedBlock", mock.Anything).Return(tt.lastBlock, nil).Once()
			}
			mockL1BridgeSync.On("GetBridges", mock.Anything, uint64(0), mock.AnythingOfType("uint64")).Return(tt.bridges, nil)

			// Setup L1 info tree mocks if we have bridges
			if len(tt.bridges) > 0 {
				// Only mock the latest bridge since simulateGERCalculation only calls for the last bridge
				latestBridge := tt.bridges[len(tt.bridges)-1]
				mainnetExitRoot := common.HexToHash("0xa6f1c7537095290a4d4c0fa300186bf138a863b98a2d2257b33af94134b02278")
				rollupExitRoot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
				expectedGER := crypto.Keccak256Hash(mainnetExitRoot.Bytes(), rollupExitRoot.Bytes())

				mockL1InfoLeaf := &l1infotreesync.L1InfoTreeLeaf{
					MainnetExitRoot: mainnetExitRoot,
					RollupExitRoot:  rollupExitRoot,
					GlobalExitRoot:  expectedGER,
				}
				mockL1Info.On("GetLatestInfoUntilBlock", mock.Anything, latestBridge.BlockNum).Return(mockL1InfoLeaf, nil)
			}

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
			if len(tt.bridges) > 0 {
				mockL1Info.AssertExpectations(t)
			}
		})
	}
}

func TestSandboxAggOracle_simulateGERCalculation(t *testing.T) {
	// Expected mainnet and rollup exit roots
	mainnetExitRoot := common.HexToHash("0xa6f1c7537095290a4d4c0fa300186bf138a863b98a2d2257b33af94134b02278")
	rollupExitRoot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
	expectedGER := crypto.Keccak256Hash(mainnetExitRoot.Bytes(), rollupExitRoot.Bytes())

	tests := []struct {
		name                 string
		bridge               bridgesync.Bridge
		mockFinalized        bool
		l1InfoTreeError      error
		expectedUsesFallback bool
	}{
		{
			name: "successful L1 info tree lookup",
			bridge: bridgesync.Bridge{
				BlockNum:     100,
				DepositCount: 1,
				LeafType:     1,
			},
			mockFinalized:        true,
			l1InfoTreeError:      nil,
			expectedUsesFallback: false,
		},
		{
			name: "L1 info tree error - uses fallback",
			bridge: bridgesync.Bridge{
				BlockNum:     100,
				DepositCount: 1,
				LeafType:     1,
			},
			mockFinalized:        true,
			l1InfoTreeError:      errors.New("L1 info tree error"),
			expectedUsesFallback: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fresh mock for each test case
			mockL1Info := &MockL1InfoTreer{}

			// Setup base oracle with mock L1 info tree
			baseOracle := &AggOracle{
				l1Info: mockL1Info,
			}

			// Setup sandbox config
			sandboxConfig := SandboxConfig{
				MockFinalization: tt.mockFinalized,
			}

			sandboxOracle := &SandboxAggOracle{
				AggOracle:     baseOracle,
				sandboxConfig: sandboxConfig,
				logger:        log.GetDefaultLogger(),
			}

			// Setup mock expectations
			if tt.l1InfoTreeError != nil {
				mockL1Info.On("GetLatestInfoUntilBlock", mock.Anything, tt.bridge.BlockNum).Return((*l1infotreesync.L1InfoTreeLeaf)(nil), tt.l1InfoTreeError)
			} else {
				mockL1InfoLeaf := &l1infotreesync.L1InfoTreeLeaf{
					MainnetExitRoot: mainnetExitRoot,
					RollupExitRoot:  rollupExitRoot,
					GlobalExitRoot:  expectedGER,
				}
				mockL1Info.On("GetLatestInfoUntilBlock", mock.Anything, tt.bridge.BlockNum).Return(mockL1InfoLeaf, nil)
			}

			// Test
			result := sandboxOracle.simulateGERCalculation(tt.bridge)

			// Assertions
			if tt.expectedUsesFallback {
				// When using fallback, result should be bridge hash (with MockFinalization=true)
				assert.Equal(t, tt.bridge.Hash(), result)
			} else {
				// When using proper calculation, result should be the calculated GER
				assert.Equal(t, expectedGER, result)
			}

			mockL1Info.AssertExpectations(t)
		})
	}
}

func TestSandboxAggOracle_processLatestGERSandbox(t *testing.T) {
	tests := []struct {
		name         string
		bridges      []bridgesync.Bridge
		isInjected   bool
		autoSettle   bool
		delay        time.Duration
		expectInject bool
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
			mockL1Info := &MockL1InfoTreer{}

			baseOracle := &AggOracle{
				logger:      logger,
				chainSender: mockSender,
				l1Info:      mockL1Info,
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

			// Setup bridge data mocks
			// With MockFinalization=true, GetLastProcessedBlock is called twice:
			// 1. In calculateGERFromBridgeEvents
			// 2. In getLatestExitRootFromBridge
			mockL1BridgeSync.On("GetLastProcessedBlock", mock.Anything).Return(uint64(100), nil).Twice()
			mockL1BridgeSync.On("GetBridges", mock.Anything, uint64(0), mock.AnythingOfType("uint64")).Return(tt.bridges, nil)

			// Setup L1InfoTree and GER injection mocks
			if len(tt.bridges) > 0 {
				// Mock L1InfoTree to return proper mainnet and rollup exit roots
				mainnetExitRoot := common.HexToHash("0xa6f1c7537095290a4d4c0fa300186bf138a863b98a2d2257b33af94134b02278")
				rollupExitRoot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
				expectedGER := crypto.Keccak256Hash(mainnetExitRoot.Bytes(), rollupExitRoot.Bytes())

				latestBridge := tt.bridges[len(tt.bridges)-1]
				mockL1InfoLeaf := &l1infotreesync.L1InfoTreeLeaf{
					MainnetExitRoot: mainnetExitRoot,
					RollupExitRoot:  rollupExitRoot,
					GlobalExitRoot:  expectedGER,
				}
				mockL1Info.On("GetLatestInfoUntilBlock", mock.Anything, latestBridge.BlockNum).Return(mockL1InfoLeaf, nil)

				// Mock GER injection check and injection
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
			if len(tt.bridges) > 0 {
				mockL1Info.AssertExpectations(t)
			}
		})
	}
}
