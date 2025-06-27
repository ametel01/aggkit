# AggKit Sandbox AggOracle

## Overview

The Sandbox AggOracle is a specialized implementation of the AggOracle component that enables local development and testing of bridge functionality without requiring AggLayer integration. It directly calculates Global Exit Roots (GER) from bridge events, bypassing the complex distributed consensus mechanisms of production AggLayer.

## Features

- **Direct GER Calculation**: Calculates GER directly from L1 bridge events, bypassing AggLayer
- **Immediate Settlement**: Supports immediate or delayed GER injection for testing scenarios
- **Mock Finalization**: Skips complex block finality validation for faster development cycles
- **Configurable Delays**: Optional settlement delays to simulate real-world timing

## Configuration

### AggOracle Sandbox Mode Configuration

Add the following to your configuration file to enable sandbox mode for AggOracle:

```toml
[Sandbox]
Enabled = true
AutoSettle = true
SettlementDelay = "5s"
MockFinalization = true
InstantClaims = true

[AggOracle]
TargetChainType = "EVM"
URLRPCL1 = "http://localhost:8545"
BlockFinality = "LatestBlock"  # Use LatestBlock for faster development
WaitPeriodNextGER = "5s"       # Faster polling for sandbox
SandboxMode = true             # Enable AggOracle-specific sandbox features

 [AggOracle.EVMSender]
  GlobalExitRootL2 = "0x..." # Your deployed GER contract address
  GasOffset = 80000
  WaitPeriodMonitorTx = "1s"
  
  [AggOracle.EVMSender.EthTxManager]
   FrequencyToMonitorTxs = "1s"
   WaitTxToBeMined = "2s"
   GetReceiptMaxTime = "250ms"
   GetReceiptWaitInterval = "1s"
   PrivateKeys = [
    {Path = "/path/to/sequencer.keystore", Password = "password"}
   ]
   ForcedGas = 0
   GasPriceMarginFactor = 1
   MaxGasPriceLimit = 0
   PendingTransactionCheckInterval = "1s"
   
   [AggOracle.EVMSender.EthTxManager.Etherman]
    URL = "http://localhost:8546"  # L2 node URL
    MultiGasProvider = false
    L1ChainID = 31338              # L2 Chain ID
```

## How It Works

### 1. GER Calculation Process

In sandbox mode, the AggOracle:

1. **Monitors L1 Bridge Events**: Watches for bridge transactions on the L1 node
2. **Calculates Simplified GER**: Uses bridge event hashes as simplified GER values
3. **Immediate Injection**: Injects the calculated GER into L2 without AggLayer coordination
4. **Optional Delays**: Applies configurable delays to simulate real-world settlement timing

### 2. Mock Finalization

When `MockFinalization` is enabled:

- Uses latest block data instead of waiting for finalized blocks
- Bypasses complex finality validation
- Enables immediate access to recent bridge events

### 3. Settlement Behavior

The sandbox AggOracle supports different settlement modes:

- **AutoSettle = true**: Automatically settles all bridge operations
- **SettlementDelay**: Adds realistic delays to settlement process
- **InstantClaims**: Makes bridge claims immediately claimable

## Development Workflow

### 1. Local Setup

```bash
# Start local L1 node (Anvil)
anvil --port 8545 --chain-id 31337

# Start local L2 node (Anvil) 
anvil --port 8546 --chain-id 31338

# Deploy bridge contracts to both networks
# ... deployment scripts ...

# Start AggKit with sandbox configuration
./aggkit run --cfg sandbox-config.toml --components bridge,aggoracle
```

### 2. Bridge Testing

With sandbox mode enabled, you can:

1. **Perform L1→L2 Bridges**: Deposit tokens on L1, GER automatically calculated and injected on L2
2. **Perform L2→L1 Bridges**: Bridge tokens from L2, simplified settlement without AggLayer
3. **Test Claim Flows**: Claims become immediately available with `InstantClaims = true`

### 3. Development Benefits

- **No AggLayer Dependency**: Develop bridge functionality without complex AggLayer setup
- **Fast Iteration**: Immediate feedback without waiting for consensus
- **Isolated Testing**: Test bridge logic in isolation from production infrastructure
- **Configurable Timing**: Adjust delays to simulate different network conditions

## API Compatibility

The Sandbox AggOracle maintains full API compatibility with the standard AggOracle:

- All existing bridge service endpoints work unchanged
- RPC methods return consistent responses
- Event flows remain the same from a client perspective

## Limitations

### Development Only

The Sandbox AggOracle is designed for development and testing only. It should **NOT** be used in production environments because:

- Simplified security model
- No distributed consensus
- Immediate finality assumptions
- Local-only bridge validation

### Simplified GER Calculation

The GER calculation in sandbox mode is simplified and does not match production behavior:

- Uses bridge event hashes instead of complex merkle trees
- Bypasses L1 info tree synchronization
- No rollup exit root validation

## Troubleshooting

### Common Issues

1. **GER Not Injecting**:
   - Check L1 bridge events are being generated
   - Verify L2 GER contract address is correct
   - Ensure sufficient gas for GER injection transactions

2. **Settlement Delays**:
   - Adjust `SettlementDelay` for faster/slower settlement
   - Set `AutoSettle = true` for immediate settlement
   - Check `WaitPeriodNextGER` for polling frequency

3. **Bridge Events Missing**:
   - Verify bridge contract addresses in configuration
   - Check L1 node is producing blocks
   - Ensure bridge synchronizers are running

### Debug Logging

Enable debug logging to troubleshoot sandbox behavior:

```toml
[Log]
Environment = "development"
Level = "debug"
Outputs = ["stderr"]
```

This will show detailed logs of:

- Bridge event processing
- GER calculation steps  
- Settlement timing
- Error conditions

## Migration to Production

When moving from sandbox to production:

1. **Disable Sandbox Mode**: Set `Sandbox.Enabled = false`
2. **Configure AggLayer**: Add proper AggLayer connection settings
3. **Update Block Finality**: Change to `FinalizedBlock` for production safety
4. **Adjust Timing**: Increase polling periods for production efficiency
5. **Security Review**: Ensure proper key management and security practices
