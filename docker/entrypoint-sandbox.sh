#!/bin/bash
set -e

# AggKit Sandbox Mode Entrypoint Script
# This script generates configuration from environment variables and starts AggKit in sandbox mode

echo "=== AggKit Sandbox Mode ==="
echo "Starting AggKit in sandbox mode..."

# Default values - use /tmp for config to avoid permission issues
AGGKIT_CONFIG_PATH=${AGGKIT_CONFIG_PATH:-/tmp/aggkit-sandbox.toml}
AGGKIT_SANDBOX_ENABLED=${AGGKIT_SANDBOX_ENABLED:-"true"}
AGGKIT_LOG_LEVEL=${AGGKIT_LOG_LEVEL:-info}
AGGKIT_COMPONENTS=${AGGKIT_COMPONENTS:-"bridge,aggoracle,claim-sponsor"}
AGGKIT_CLAIMSPONSOR_ENABLED=${AGGKIT_CLAIMSPONSOR_ENABLED:-"true"}

# Network configuration
AGGKIT_L1_CHAIN_ID=${AGGKIT_L1_CHAIN_ID:-${CHAIN_ID_MAINNET:-"1"}}
AGGKIT_L2_CHAIN_ID=${AGGKIT_L2_CHAIN_ID:-${CHAIN_ID_AGGLAYER_1:-"1101"}}
AGGKIT_L1_URL=${AGGKIT_L1_URL:-"http://anvil-l1:8545"}
AGGKIT_L2_URL=${AGGKIT_L2_URL:-"http://anvil-l2:8545"}

# Service configuration
AGGKIT_REST_HOST=${AGGKIT_REST_HOST:-"0.0.0.0"}
AGGKIT_REST_PORT=${AGGKIT_REST_PORT:-"5577"}
AGGKIT_RPC_HOST=${AGGKIT_RPC_HOST:-0.0.0.0}
AGGKIT_RPC_PORT=${AGGKIT_RPC_PORT:-8555}
AGGKIT_TELEMETRY_PORT=${AGGKIT_TELEMETRY_PORT:-8080}

# Database configuration
AGGKIT_DATABASE_DRIVER=${AGGKIT_DATABASE_DRIVER:-"sqlite"}
AGGKIT_DATABASE_NAME=${AGGKIT_DATABASE_NAME:-"/app/data/aggkit_sandbox.db"}

# Sandbox specific configuration
AGGKIT_SANDBOX_AUTO_SETTLE=${AGGKIT_SANDBOX_AUTO_SETTLE:-"true"}
AGGKIT_SANDBOX_SETTLEMENT_DELAY=${AGGKIT_SANDBOX_SETTLEMENT_DELAY:-"5s"}
AGGKIT_SANDBOX_MOCK_FINALIZATION=${AGGKIT_SANDBOX_MOCK_FINALIZATION:-"true"}
AGGKIT_SANDBOX_INSTANT_CLAIMS=${AGGKIT_SANDBOX_INSTANT_CLAIMS:-"true"}

# Contract addresses (can be overridden via env vars)
POLYGON_ZKEVM_L1=${POLYGON_ZKEVM_L1:-"0xDc64a140Aa3E981100a9becA4E685f962f0cF6C9"}
POLYGON_ZKEVM_BRIDGE_L1=${POLYGON_ZKEVM_BRIDGE_L1:-"0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0"}
POLYGON_ZKEVM_BRIDGE_L2=${POLYGON_ZKEVM_BRIDGE_L2:-"0x5FbDB2315678afecb367f032d93F642f64180aa3"}
POLYGON_ROLLUP_MANAGER_L1=${POLYGON_ROLLUP_MANAGER_L1:-"0x0165878A594ca255338adfa4d48449f69242Eb8F"}
POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L1=${POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L1:-"0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9"}
# L2 Global Exit Root Manager contract - different from the bridge contract
# This should be the address of GlobalExitRootManagerL2SovereignChain contract, not the bridge
POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2=${POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2:-"0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"}
AGGKIT_EVM_SENDER=${AGGKIT_EVM_SENDER:-0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266}

# Default private key for sandbox mode (from anvil default accounts)
AGGORACLE_PRIVATE_KEY=${AGGORACLE_PRIVATE_KEY:-"0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"}

# Function to generate configuration file
generate_config() {
    echo "Generating configuration file: $AGGKIT_CONFIG_PATH"
    
    # Create private key file for AggOracle (keystore format)
    echo "Creating AggOracle keystore file..."
    # Use the pre-generated keystore for anvil's first account (0xac0974...)
    # This keystore has no password for simplicity in sandbox mode
    cat > /tmp/aggoracle.key << 'KEYSTORE_EOF'
{"address":"f39fd6e51aad88f6f4ce6ab8827279cfffb92266","crypto":{"cipher":"aes-128-ctr","ciphertext":"d005030a7684f3adad2447cbb27f63039eec2224c451eaa445de0d90502b9f3d","cipherparams":{"iv":"dc07a54bc7e388efa89c34d42f2ebdb4"},"kdf":"scrypt","kdfparams":{"dklen":32,"n":262144,"p":1,"r":8,"salt":"cf2ec55ecae11171de575112cfb16963570533a9c46fb774473ceb11519eb24a"},"mac":"3eb180d405a5da6e462b2adc00091c14856c91d574bf27348714506357d6e177"},"id":"035454db-6b6d-477f-8a79-ce24c10b185f","version":3}
KEYSTORE_EOF
    chmod 600 /tmp/aggoracle.key
    
    cat > "$AGGKIT_CONFIG_PATH" << EOF
# AggKit Sandbox Configuration - Generated from Environment Variables
# Generated at: $(date)

[Log]
Level = "$AGGKIT_LOG_LEVEL"
Outputs = ["stdout"]

[Telemetry]
PrometheusAddr = "0.0.0.0:$AGGKIT_TELEMETRY_PORT"

[RPC]
Host = "$AGGKIT_RPC_HOST"
Port = $AGGKIT_RPC_PORT
ReadTimeout = "15s"
WriteTimeout = "15s"
MaxRequestsPerIPAndSecond = 1000

# Primary Network Configuration (L2)
[Common]
NetworkID = 1

[Common.L1RPC]
URL = "$AGGKIT_L1_URL"
Mode = "basic"

[Common.L2RPC]
URL = "$AGGKIT_L2_URL"
Mode = "basic"

[L1NetworkConfig]
URL = "$AGGKIT_L1_URL"
L1ChainID = $AGGKIT_L1_CHAIN_ID
POLTokenAddr = "0x0000000000000000000000000000000000000000"
RollupAddr = "$POLYGON_ZKEVM_L1"
RollupManagerAddr = "$POLYGON_ROLLUP_MANAGER_L1"
GlobalExitRootManagerAddr = "$POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L1"

[EthTxManager]
FrequencyToMonitorTxs = "10s"
WaitTxToBeMined = "30s"
GetReceiptMaxTime = "60s"
GetReceiptWaitInterval = "5s"
PrivateKeys = [
  {Path = "", Password = ""},
]

[L2GasPriceSuggester]
Type = "follower"
UpdatePeriod = "10s"
Factor = 0.5
DefaultGasPrice = 1000000000
MaxGasPrice = 0

[REST]
Port = $AGGKIT_REST_PORT
Host = "$AGGKIT_REST_HOST"
ReadTimeout = "30s"
WriteTimeout = "30s"
MaxRequestsPerIPAndSecond = 1000

[ClaimSponsor]
DBPath = "/app/data/claimsponsor.sqlite"
Enabled = $AGGKIT_CLAIMSPONSOR_ENABLED
SenderAddr = "$AGGKIT_EVM_SENDER"
BridgeAddrL2 = "$POLYGON_ZKEVM_BRIDGE_L2"
MaxGas = 3000000
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitTxToBeMinedPeriod = "3s"
WaitOnEmptyQueue = "3s"
GasOffset = 0

[ClaimSponsor.EthTxManager]
FrequencyToMonitorTxs = "1s"
WaitTxToBeMined = "2s"
GetReceiptMaxTime = "250ms"
GetReceiptWaitInterval = "1s"
ForcedGas = 0
GasPriceMarginFactor = 1
MaxGasPriceLimit = 0
StoragePath = "/app/data/ethtxmanager-claimsponsor.sqlite"
ReadPendingL1Txs = false
SafeStatusL1NumberOfBlocks = 5
FinalizedStatusL1NumberOfBlocks = 10

[ClaimSponsor.EthTxManager.PrivateKeys]
Method = "local"
Path = "/tmp/aggoracle.key"
Password = "testonly"

[ClaimSponsor.EthTxManager.Etherman]
URL = "$AGGKIT_L2_URL"
MultiGasProvider = false
# L1ChainID = 0 indicates it will be set at runtime
# This field should be populated with L2ChainID 
L1ChainID = $AGGKIT_L2_CHAIN_ID
HTTPHeaders = []

[Database]
Driver = "$AGGKIT_DATABASE_DRIVER"
Name = "$AGGKIT_DATABASE_NAME"
Host = ""
Port = ""
User = ""
Password = ""
MaxConns = 0

# Network Configurations for Bridge Service
[Networks]

# L1 Network Configuration (Mainnet)
[Networks.L1]
NetworkID = 0
Name = "L1 Mainnet"
URL = "$AGGKIT_L1_URL"
ChainID = $AGGKIT_L1_CHAIN_ID

# L2 Network Configuration
[Networks.L2] 
NetworkID = 1
Name = "L2 AggLayer"
URL = "$AGGKIT_L2_URL"
ChainID = $AGGKIT_L2_CHAIN_ID

# Sandbox Configuration
[Sandbox]
Enabled = $AGGKIT_SANDBOX_ENABLED
AutoSettle = $AGGKIT_SANDBOX_AUTO_SETTLE
SettlementDelay = "$AGGKIT_SANDBOX_SETTLEMENT_DELAY"
MockFinalization = $AGGKIT_SANDBOX_MOCK_FINALIZATION
InstantClaims = $AGGKIT_SANDBOX_INSTANT_CLAIMS

[Sandbox.L1Node]
URL = "$AGGKIT_L1_URL"
ChainID = $AGGKIT_L1_CHAIN_ID
Name = "Docker L1 Node"

[Sandbox.L2Node]
URL = "$AGGKIT_L2_URL"
ChainID = $AGGKIT_L2_CHAIN_ID
Name = "Docker L2 Node"

# AggOracle Configuration
[AggOracle]
SandboxMode = true
TargetChainType = "EVM"
URLRPCL1 = "$AGGKIT_L1_URL"
BlockFinality = "LatestBlock"
WaitPeriodNextGER = "10s"

[AggOracle.EVMSender]
GlobalExitRootL2 = "$POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2"
GasOffset = 0
WaitPeriodMonitorTx = "10s"

[AggOracle.EVMSender.EthTxManager]
FrequencyToMonitorTxs = "10s"
WaitTxToBeMined = "30s"
GetReceiptMaxTime = "60s"
GetReceiptWaitInterval = "5s"
ForcedGas = 0
GasPriceMarginFactor = 1
MaxGasPriceLimit = 0
StoragePath = "/app/data/ethtxmanager-aggoracle.sqlite"
ReadPendingL1Txs = false
SafeStatusL1NumberOfBlocks = 0
FinalizedStatusL1NumberOfBlocks = 0

[[AggOracle.EVMSender.EthTxManager.PrivateKeys]]
Method = "local"
Path = "/tmp/aggoracle.key"
Password = "testonly"

[AggOracle.EVMSender.EthTxManager.Etherman]
URL = "$AGGKIT_L2_URL"
MultiGasProvider = false
L1ChainID = $AGGKIT_L2_CHAIN_ID
HTTPHeaders = []

# AggSender (will be skipped in sandbox mode)
[AggSender]
StoragePath = "/app/data/aggsender"
BlockFinality = "LatestBlock"
PrivateKey = {Path = "", Password = ""}

[AggSender.AgglayerClient]
URL = "http://localhost:8080"

# L1InfoTreeSync configuration  
[L1InfoTreeSync]
DBPath = "/app/data/L1InfoTreeSync.sqlite"
GlobalExitRootAddr = "$POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L1"
RollupManagerAddr = "$POLYGON_ROLLUP_MANAGER_L1"
SyncBlockChunkSize = 100
BlockFinality = "LatestBlock"
URLRPCL1 = "$AGGKIT_L1_URL"
WaitForNewBlocksPeriod = "100ms"
InitialBlock = 0
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
RequireStorageContentCompatibility = false

# LastGERSync configuration
[LastGERSync]
DBPath = "/app/data/lastgersync.sqlite"
BlockFinality = "LatestBlock"
InitialBlockNum = 0
GlobalExitRootL2Addr = "$POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2"
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "1s"
DownloadBufferSize = 100
RequireStorageContentCompatibility = false
SyncMode = "FEP"

# ReorgDetector configurations
[ReorgDetectorL1]
DBPath = "/app/data/reorgdetectorl1.sqlite"
FinalizedBlock = "LatestBlock"

[ReorgDetectorL2]
DBPath = "/app/data/reorgdetectorl2.sqlite"
FinalizedBlock = "LatestBlock"

# Bridge sync configurations
[BridgeL1Sync]
DBPath = "/app/data/bridgel1sync.sqlite"
BlockFinality = "LatestBlock"
InitialBlockNum = 0
BridgeAddr = "$POLYGON_ZKEVM_BRIDGE_L1"
SyncBlockChunkSize = 100
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "3s"
RequireStorageContentCompatibility = false

[BridgeL2Sync]
DBPath = "/app/data/bridgel2sync.sqlite"
BlockFinality = "LatestBlock"
InitialBlockNum = 0
BridgeAddr = "$POLYGON_ZKEVM_BRIDGE_L2"
SyncBlockChunkSize = 100
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "3s"
RequireStorageContentCompatibility = false

# Bridge Service Network Support
[BridgeService]
SupportedNetworks = [0, 1]

[BridgeService.Networks.0]
Name = "L1 Mainnet"
ChainID = $AGGKIT_L1_CHAIN_ID
BridgeAddr = "$POLYGON_ZKEVM_BRIDGE_L1"
URL = "$AGGKIT_L1_URL"

[BridgeService.Networks.1]
Name = "L2 AggLayer"
ChainID = $AGGKIT_L2_CHAIN_ID
BridgeAddr = "$POLYGON_ZKEVM_BRIDGE_L2"
URL = "$AGGKIT_L2_URL"
EOF

    echo "Configuration generated successfully!"
}

# Function to wait for dependencies
wait_for_dependency() {
    local host=$1
    local port=$2
    local service=$3
    
    echo "Waiting for $service at $host:$port..."
    
    for i in {1..30}; do
        if curl -s "$host:$port" >/dev/null 2>&1; then
            echo "$service is ready!"
            return 0
        fi
        echo "Waiting for $service... ($i/30)"
        sleep 2
    done
    
    echo "Warning: $service at $host:$port is not responding after 60 seconds"
    return 1
}

# Function to validate configuration
validate_config() {
    echo "Validating configuration..."
    
    if [[ "$AGGKIT_L1_CHAIN_ID" == "$AGGKIT_L2_CHAIN_ID" ]]; then
        echo "Error: L1 and L2 chain IDs must be different"
        exit 1
    fi
    
    if [[ "$AGGKIT_SANDBOX_ENABLED" != "true" ]]; then
        echo "Error: This image only supports sandbox mode (AGGKIT_SANDBOX_ENABLED=true)"
        exit 1
    fi
    
    if [[ "$POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2" == "0x0000000000000000000000000000000000000000" ]]; then
        echo "Warning: POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2 is not set. AggOracle will fail to start."
        echo "Please deploy a GlobalExitRootManagerL2SovereignChain contract and set this environment variable."
    fi
    
    echo "Configuration validation passed!"
}

# Main execution
main() {
    echo "Environment Variables:"
    echo "  AGGKIT_SANDBOX_ENABLED: $AGGKIT_SANDBOX_ENABLED"
    echo "  AGGKIT_L1_URL: $AGGKIT_L1_URL"
    echo "  AGGKIT_L2_URL: $AGGKIT_L2_URL"
    echo "  AGGKIT_L1_CHAIN_ID: $AGGKIT_L1_CHAIN_ID"
    echo "  AGGKIT_L2_CHAIN_ID: $AGGKIT_L2_CHAIN_ID"
    echo "  AGGKIT_COMPONENTS: $AGGKIT_COMPONENTS"
    echo "  AGGKIT_REST_PORT: $AGGKIT_REST_PORT"
    echo "  AGGKIT_DATABASE_NAME: $AGGKIT_DATABASE_NAME"
    echo "  POLYGON_ZKEVM_BRIDGE_L1: $POLYGON_ZKEVM_BRIDGE_L1"
    echo "  POLYGON_ZKEVM_BRIDGE_L2: $POLYGON_ZKEVM_BRIDGE_L2"
    echo "  POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2: $POLYGON_ZKEVM_GLOBAL_EXIT_ROOT_L2"
    echo ""
    
    # Debug information
    echo "Debug: Current user: $(whoami)"
    echo "Debug: Config path: $AGGKIT_CONFIG_PATH"
    
    # Validate configuration
    validate_config
    
    # Check if custom config is provided
    if [[ -f "/app/config/custom.toml" ]]; then
        echo "Using custom configuration file: /app/config/custom.toml"
        AGGKIT_CONFIG_PATH="/app/config/custom.toml"
    else
        # Generate configuration from environment variables
        generate_config
    fi
    
    # Wait for L1 node if specified
    if [[ "$AGGKIT_WAIT_FOR_L1" == "true" ]]; then
        L1_HOST=$(echo "$AGGKIT_L1_URL" | sed 's|http://||' | sed 's|:.*||')
        L1_PORT=$(echo "$AGGKIT_L1_URL" | sed 's|.*:||' | sed 's|/.*||')
        wait_for_dependency "$L1_HOST" "$L1_PORT" "L1 Node"
    fi
    
    # Wait for L2 node if specified
    if [[ "$AGGKIT_WAIT_FOR_L2" == "true" ]]; then
        L2_HOST=$(echo "$AGGKIT_L2_URL" | sed 's|http://||' | sed 's|:.*||')
        L2_PORT=$(echo "$AGGKIT_L2_URL" | sed 's|.*:||' | sed 's|/.*||')
        wait_for_dependency "$L2_HOST" "$L2_PORT" "L2 Node"
    fi
    
    # Create data directory if needed
    mkdir -p /app/data /app/logs
    
    echo "Starting AggKit Sandbox..."
    echo "Configuration file: $AGGKIT_CONFIG_PATH"
    echo "Components: $AGGKIT_COMPONENTS"
    echo ""
    
    # Execute AggKit with the generated configuration
    exec /app/aggkit-sandbox "$@" \
        --cfg="$AGGKIT_CONFIG_PATH" \
        --components="$AGGKIT_COMPONENTS"
}

# Run main function
main "$@" 