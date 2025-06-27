# AggKit Sandbox Mode

## Overview

AggKit Sandbox Mode enables local development and testing with simplified bridge functionality between local Anvil nodes, bypassing the complexity of AggLayer integration while maintaining full bridge API compatibility.

## Features

- **Local Development Environment**: Works with 2 local Anvil nodes (L1 mainnet + L2 sovereign chain)
- **Simplified Architecture**: Bypasses AggLayer integration for faster development cycles
- **Full Bridge API Compatibility**: Maintains the same APIs as production AggKit
- **Automatic Settlement**: Bridges are settled immediately without waiting for AggLayer
- **Component Selection**: Automatically skips unnecessary components (AggSender, AggchainProofGen)

## Configuration

### Basic Setup

1. Create a sandbox configuration file (e.g., `sandbox.toml`):

```toml
# Enable sandbox mode
[Sandbox]
Enabled = true
AutoSettle = true
SettlementDelay = "5s"
MockFinalization = true
InstantClaims = true

 [Sandbox.L1Node]
  URL = "http://localhost:8545"
  ChainID = 31337

 [Sandbox.L2Node]
  URL = "http://localhost:8546"
  ChainID = 31338

# Override URLs for sandbox
L1URL = "http://localhost:8545"
L2URL = "http://localhost:8546"

# Sample bridge contract address (replace with actual)
polygonBridgeAddr = "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe"

# Network configuration
NetworkID = 31337
```

### Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `Sandbox.Enabled` | Enable/disable sandbox mode | `false` |
| `Sandbox.AutoSettle` | Automatically settle bridge operations | `true` |
| `Sandbox.SettlementDelay` | Delay before settlement | `"5s"` |
| `Sandbox.MockFinalization` | Skip complex finality validation | `true` |
| `Sandbox.InstantClaims` | Make bridge claims immediately ready | `true` |
| `Sandbox.L1Node.URL` | L1 node RPC endpoint | Required |
| `Sandbox.L1Node.ChainID` | L1 chain identifier | Required |
| `Sandbox.L2Node.URL` | L2 node RPC endpoint | Required |
| `Sandbox.L2Node.ChainID` | L2 chain identifier | Required |

## Usage

### Starting Sandbox Mode

1. **Start Local Anvil Nodes**:

   ```bash
   # Terminal 1: L1 (mainnet)
   anvil --chain-id 31337 --port 8545
   
   # Terminal 2: L2 (sovereign chain)  
   anvil --chain-id 31338 --port 8546
   ```

2. **Deploy Bridge Contracts**: Deploy the necessary bridge contracts to both networks.

3. **Start AggKit in Sandbox Mode**:

   ```bash
   aggkit run --cfg sandbox.toml --components bridge,aggoracle
   ```

### Component Behavior in Sandbox Mode

| Component | Behavior |
|-----------|----------|
| **Bridge** | ✅ Fully functional with instant settlement |
| **AggOracle** | ✅ Modified for direct GER calculation |
| **AggSender** | ❌ Automatically skipped |
| **AggchainProofGen** | ❌ Automatically skipped |

## Development Workflow

1. **Setup Environment**: Start local Anvil nodes
2. **Deploy Contracts**: Deploy bridge contracts to both L1 and L2
3. **Update Configuration**: Set contract addresses in `sandbox.toml`
4. **Start AggKit**: Launch with sandbox configuration
5. **Test Bridge Operations**: Use bridge APIs for testing

## API Compatibility

All bridge service APIs remain the same:

- `bridge_getProof`
- `bridge_l1InfoTreeIndexForBridge`
- `bridge_injectedInfoAfterIndex`
- `bridge_getTokenMappings`

Response payloads include sandbox mode indicators:

```json
{
  "result": {
    // existing fields...
    "sandbox_mode": true,
    "instant_settlement": true,
    "mock_finalization": true
  }
}
```

## Validation

The sandbox configuration is automatically validated on startup:

- ✅ L1 and L2 node URLs must be provided
- ✅ L1 and L2 chain IDs must be non-zero and different
- ✅ Settlement delay must be non-negative
- ✅ Required contract addresses must be provided

## Troubleshooting

### Common Issues

1. **Configuration Validation Errors**:
   - Ensure L1 and L2 have different chain IDs
   - Verify node URLs are accessible
   - Check contract addresses are valid

2. **Connection Issues**:
   - Verify Anvil nodes are running on specified ports
   - Check firewall/network connectivity

3. **Contract Deployment**:
   - Ensure bridge contracts are deployed to both networks
   - Verify contract addresses in configuration

### Logs

Sandbox mode provides clear logging:

```
INFO Sandbox mode enabled - AggLayer integration disabled
INFO Skipping AggSender in sandbox mode
INFO Skipping AggchainProofGen in sandbox mode
```

## Implementation Status

### Phase 1: Configuration Framework ✅

- [x] Sandbox configuration structures
- [x] Configuration validation
- [x] Component skipping logic
- [x] Sample configuration file
- [x] Unit tests

### Phase 2: AggOracle Sandbox Mode (Next)

- [ ] Direct GER calculation
- [ ] Immediate settlement simulation
- [ ] Mock finalization

### Phase 3: Bridge Service Enhancement (Next)

- [ ] Sandbox mode API responses
- [ ] Instant claim readiness
- [ ] Enhanced metadata

## Testing

Run the sandbox configuration tests:

```bash
go test -v ./config -run TestSandboxConfig
```

All tests should pass, validating:

- Configuration loading
- Validation logic
- Component detection
- Error handling
