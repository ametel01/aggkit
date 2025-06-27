# AggKit Sandbox Bridge Service Enhancement

## Overview

Phase 3 of the AggKit Sandbox implementation enhances the bridge service with sandbox-specific functionality, providing developers with rich metadata and instant claim processing for local development.

## Features

### 1. Sandbox Mode API Responses

All bridge service API responses now include sandbox metadata when running in sandbox mode:

- **Bridge Responses**: Include sandbox configuration and development metadata
- **Claim Responses**: Show instant claim readiness status
- **Claim Proof Responses**: Indicate simplified proof generation
- **Sync Status**: Show sandbox-specific synchronization information

### 2. Instant Claim Readiness

When `InstantClaims` is enabled in sandbox mode:

- Claims are immediately ready for processing
- No waiting period for claim finalization
- Simplified proof generation for faster development cycles
- Enhanced metadata showing claim processing time (0s)

### 3. Enhanced Metadata

Comprehensive development metadata including:

- **Bridge Mode**: Indicates sandbox operation
- **Chain Information**: L1 and L2 chain IDs
- **Settlement Information**: Auto-settlement status and delays
- **Finalization Status**: Mock finalization indicators
- **Development Context**: Additional debugging information

## API Response Examples

### Bridge Response with Sandbox Metadata

```json
{
  "bridges": [
    {
      "block_num": 1234,
      "deposit_count": 10,
      "amount": "1000000000000000000",
      "sandbox_metadata": {
        "sandbox_mode": true,
        "auto_settle": true,
        "instant_claims": true,
        "mock_finalization": true,
        "settlement_delay": "5s",
        "generated_at": 1684500000,
        "dev_metadata": {
          "bridge_mode": "sandbox",
          "l1_chain_id": 31337,
          "l2_chain_id": 31338,
          "claim_ready_instantly": true,
          "finalization_bypassed": true
        }
      }
    }
  ],
  "count": 1,
  "sandbox_metadata": {
    "sandbox_mode": true,
    "auto_settle": true,
    "instant_claims": true,
    "dev_metadata": {
      "total_bridges": 1,
      "page_info": {
        "page_number": 1,
        "page_size": 20
      }
    }
  }
}
```

### Claim Proof Response with Sandbox Metadata

```json
{
  "proof_local_exit_root": ["0x1", "0x2"],
  "proof_rollup_exit_root": ["0x3", "0x4"],
  "l1_info_tree_leaf": {
    "block_num": 1234,
    "l1_info_tree_index": 42,
    "sandbox_metadata": {
      "sandbox_mode": true,
      "mock_finalization": true,
      "dev_metadata": {
        "ger_calculation_method": "direct_bridge_events"
      }
    }
  },
  "sandbox_metadata": {
    "sandbox_mode": true,
    "instant_claims": true,
    "dev_metadata": {
      "claim_verification": "instant_sandbox_mode",
      "proof_method": "simplified_local_calculation"
    }
  }
}
```

### Claims Response with Instant Readiness

```json
{
  "claims": [
    {
      "global_index": "1000000000000000000",
      "origin_network": 0,
      "destination_network": 1
    }
  ],
  "count": 1,
  "sandbox_metadata": {
    "sandbox_mode": true,
    "instant_claims": true,
    "dev_metadata": {
      "total_claims": 1,
      "claims_instantly_ready": true,
      "claim_processing_time": "0s"
    }
  }
}
```

### Sync Status with Sandbox Information

```json
{
  "l1_info": {
    "contract_deposit_count": 10,
    "bridge_deposit_count": 10,
    "is_synced": true
  },
  "l2_info": {
    "contract_deposit_count": 5,
    "bridge_deposit_count": 5,
    "is_synced": true
  },
  "sandbox_metadata": {
    "sandbox_mode": true,
    "auto_settle": true,
    "dev_metadata": {
      "sync_mode": "sandbox_instant",
      "settlement_mode": "automatic"
    }
  }
}
```

## Configuration

Bridge service sandbox mode is automatically enabled when the global sandbox configuration is active:

```toml
# Global sandbox configuration
[Sandbox]
Enabled = true
AutoSettle = true
SettlementDelay = "2s"
MockFinalization = true
InstantClaims = true

[Sandbox.L1Node]
URL = "http://localhost:8545"
ChainID = 31337

[Sandbox.L2Node]
URL = "http://localhost:8546"
ChainID = 31338
```

## Development Benefits

### Faster Testing Cycles

- **Instant Claims**: No waiting for claim finalization
- **Simplified Proofs**: Faster proof generation for development
- **Rich Metadata**: Comprehensive debugging information

### Enhanced Debugging

- **Development Context**: Clear indicators of sandbox mode
- **Chain Information**: Easy identification of L1/L2 networks
- **Processing Status**: Real-time information about bridge operations

### API Compatibility

- **Non-Breaking Changes**: All existing APIs work unchanged
- **Optional Metadata**: Sandbox metadata only appears in sandbox mode
- **Backward Compatible**: Production APIs unaffected

## Implementation Details

### Bridge Service Enhancement

The `BridgeService` struct includes:

```go
type BridgeService struct {
    // ... existing fields ...
    sandboxConfig *config.SandboxConfig
}
```

### Sandbox Metadata Structure

```go
type SandboxMetadata struct {
    SandboxMode      bool                   `json:"sandbox_mode"`
    AutoSettle       bool                   `json:"auto_settle"`
    InstantClaims    bool                   `json:"instant_claims"`
    MockFinalization bool                   `json:"mock_finalization"`
    SettlementDelay  string                 `json:"settlement_delay"`
    GeneratedAt      int64                  `json:"generated_at"`
    DevMetadata      map[string]interface{} `json:"dev_metadata,omitempty"`
}
```

### Helper Methods

The bridge service provides several helper methods:

- `isSandboxMode()`: Check if running in sandbox mode
- `createSandboxMetadata()`: Generate sandbox metadata
- `enhanceBridgeResponseWithSandbox()`: Add metadata to bridge responses
- `enhanceClaimProofWithSandbox()`: Add metadata to claim proofs
- `isClaimInstantlyReady()`: Check instant claim readiness

## Testing

Comprehensive test suite covering:

- Sandbox mode detection
- Metadata generation
- Response enhancement
- Instant claim functionality
- API compatibility

Run tests:

```bash
# Test sandbox bridge functionality
go test -v ./bridgeservice -run TestBridgeService_Sandbox

# Test all bridge functionality
go test -v ./bridgeservice
```

## Usage Examples

### Development Workflow

1. **Start Sandbox Environment**:
   ```bash
   # Terminal 1: L1 node
   anvil --chain-id 31337 --port 8545
   
   # Terminal 2: L2 node  
   anvil --chain-id 31338 --port 8546
   
   # Terminal 3: AggKit with bridge
   aggkit run --cfg config-sandbox.toml --components bridge
   ```

2. **Test Bridge Operations**:
   ```bash
   # Get bridges with sandbox metadata
   curl "http://localhost:5577/bridge/v1/bridges?network_id=1"
   
   # Get claims with instant readiness info
   curl "http://localhost:5577/bridge/v1/claims?network_id=1"
   
   # Get claim proof with simplified generation
   curl "http://localhost:5577/bridge/v1/claim-proof?network_id=1&leaf_index=1&deposit_count=1"
   ```

3. **Monitor Sync Status**:
   ```bash
   # Check sandbox sync status
   curl "http://localhost:5577/bridge/v1/sync-status"
   ```

## Troubleshooting

### Common Issues

1. **Missing Sandbox Metadata**:
   - Verify `Sandbox.Enabled = true` in configuration
   - Check bridge service logs for sandbox mode activation

2. **Claims Not Instantly Ready**:
   - Ensure `Sandbox.InstantClaims = true`
   - Verify sandbox configuration is passed to bridge service

3. **Incorrect Chain IDs in Metadata**:
   - Verify L1Node and L2Node ChainID values
   - Check that chain IDs are different for L1 and L2

### Debug Information

Enable debug logging to see sandbox metadata generation:

```toml
[Log]
Level = "debug"
```

Look for log messages:
- `"Bridge service starting in sandbox mode"`
- `"Enhanced X bridge responses with sandbox metadata"`
- `"Enhanced X claim responses with sandbox metadata"`

## Next Steps

Phase 3 completes the core sandbox functionality. Future enhancements could include:

- **Performance Metrics**: Timing comparisons between sandbox and production
- **Mock Contract Interactions**: Simplified contract calls for testing
- **Development Tools**: Additional debugging utilities
- **Integration Testing**: End-to-end test scenarios 