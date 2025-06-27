#!/bin/bash
set -e

# AggKit Sandbox Mode Entrypoint Script
# This script generates configuration from environment variables and starts AggKit in sandbox mode

echo "=== AggKit Sandbox Mode ==="
echo "Starting AggKit in sandbox mode..."

# Default values
AGGKIT_CONFIG_PATH=${AGGKIT_CONFIG_PATH:-/app/config/aggkit-sandbox.toml}
AGGKIT_SANDBOX_ENABLED=${AGGKIT_SANDBOX_ENABLED:-true}
AGGKIT_LOG_LEVEL=${AGGKIT_LOG_LEVEL:-info}
AGGKIT_COMPONENTS=${AGGKIT_COMPONENTS:-bridge,aggoracle}

# Network configuration
AGGKIT_NETWORK_ID=${AGGKIT_NETWORK_ID:-1338}
AGGKIT_L1_CHAIN_ID=${AGGKIT_L1_CHAIN_ID:-31337}
AGGKIT_L2_CHAIN_ID=${AGGKIT_L2_CHAIN_ID:-31338}
AGGKIT_L1_URL=${AGGKIT_L1_URL:-http://anvil-l1:8545}
AGGKIT_L2_URL=${AGGKIT_L2_URL:-http://anvil-l2:8545}

# Service configuration
AGGKIT_REST_HOST=${AGGKIT_REST_HOST:-0.0.0.0}
AGGKIT_REST_PORT=${AGGKIT_REST_PORT:-5577}
AGGKIT_RPC_HOST=${AGGKIT_RPC_HOST:-0.0.0.0}
AGGKIT_RPC_PORT=${AGGKIT_RPC_PORT:-8555}
AGGKIT_TELEMETRY_PORT=${AGGKIT_TELEMETRY_PORT:-8080}

# Database configuration
AGGKIT_DATABASE_DRIVER=${AGGKIT_DATABASE_DRIVER:-sqlite3}
AGGKIT_DATABASE_NAME=${AGGKIT_DATABASE_NAME:-/app/data/aggkit_sandbox.db}

# Sandbox specific configuration
AGGKIT_SANDBOX_AUTO_SETTLE=${AGGKIT_SANDBOX_AUTO_SETTLE:-true}
AGGKIT_SANDBOX_SETTLEMENT_DELAY=${AGGKIT_SANDBOX_SETTLEMENT_DELAY:-5s}
AGGKIT_SANDBOX_MOCK_FINALIZATION=${AGGKIT_SANDBOX_MOCK_FINALIZATION:-true}
AGGKIT_SANDBOX_INSTANT_CLAIMS=${AGGKIT_SANDBOX_INSTANT_CLAIMS:-true}

# Contract addresses (can be overridden via env vars)
AGGKIT_POLYGON_BRIDGE_ADDRESS=${AGGKIT_POLYGON_BRIDGE_ADDRESS:-0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe}
AGGKIT_EVM_SENDER=${AGGKIT_EVM_SENDER:-0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266}

# Function to generate configuration file
generate_config() {
    echo "Generating configuration file: $AGGKIT_CONFIG_PATH"
    
    mkdir -p "$(dirname "$AGGKIT_CONFIG_PATH")"
    
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

[Common]
NetworkID = $AGGKIT_NETWORK_ID
IsValidiumMode = false

[Etherman]
URL = "$AGGKIT_L1_URL"
ForkID = 9
MaxTxPerBatch = 300
PolygonBridgeAddress = "$AGGKIT_POLYGON_BRIDGE_ADDRESS"
L1ChainID = $AGGKIT_L1_CHAIN_ID

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

[Database]
Driver = "$AGGKIT_DATABASE_DRIVER"
Name = "$AGGKIT_DATABASE_NAME"
Host = ""
Port = ""
User = ""
Password = ""
MaxConns = 0

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
TargetChainID = $AGGKIT_L2_CHAIN_ID
L1URL = "$AGGKIT_L1_URL"
L2URL = "$AGGKIT_L2_URL"
EVMSender = "$AGGKIT_EVM_SENDER"
EVMPrivateKey = {Path = "", Password = ""}
GasOffset = 0
WaitPeriodNextGER = "10s"
WaitPeriodMonitorTx = "10s"
WaitPeriodFinalizedRootSignal = "10s"

# AggSender (will be skipped in sandbox mode)
[AggSender]
StoragePath = "/app/data/aggsender"
AggLayerURL = "http://localhost:8080"
BlockGetInterval = "10s"
CheckSettledInterval = "10s"
MaxWaitForL1InfoRootSignal = "30s"
PrivateKey = {Path = "", Password = ""}

# Sync configurations
[BridgeSync]
ChainID = $AGGKIT_L1_CHAIN_ID
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "100ms"
InitialBlock = 0
SyncChunkSize = 100
SyncInterval = "10s"

[L1InfoTreeSync]
ChainID = $AGGKIT_L1_CHAIN_ID
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "100ms"
InitialBlock = 0
SyncChunkSize = 100
SyncInterval = "10s"

[LastGERSync]
ChainID = $AGGKIT_L1_CHAIN_ID
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "100ms"
InitialBlock = 0
SyncChunkSize = 100
SyncInterval = "10s"
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
    echo ""
    
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
    
    # Create data directory
    mkdir -p /app/data
    
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