# AggKit Sandbox Docker Setup

This directory contains Docker configurations for running AggKit in sandbox mode, designed exclusively for local development and testing.

## Quick Start

1. **Build and run the complete sandbox environment**:

   ```bash
   docker-compose -f docker-compose.sandbox.yml up --build
   ```

2. **Run simple setup**:

   ```bash
   docker-compose -f docker-compose.simple.yml up --build
   ```

3. **Access the services**:
   - Bridge API: <http://localhost:5577>
   - L1 Node: <http://localhost:8545>
   - L2 Node: <http://localhost:8546>
   - Metrics: <http://localhost:8080>

## Architecture

```text
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Anvil L1      │    │   AggKit        │    │   Anvil L2      │
│  (Chain 31337)  │◄──►│   Sandbox       │◄──►│  (Chain 31338)  │
│   Port 8545     │    │  Bridge+Oracle  │    │   Port 8546     │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                              │
                              ▼
                       ┌─────────────────┐
                       │   Bridge API    │
                       │   Port 5577     │
                       └─────────────────┘
```

## Configuration Options

### Environment Variables

All configuration is handled via environment variables. See `env.sandbox.template` for a complete list.

#### Core Sandbox Settings

- `AGGKIT_SANDBOX_ENABLED`: Must be `true` for this image
- `AGGKIT_SANDBOX_AUTO_SETTLE`: Enable automatic settlement
- `AGGKIT_SANDBOX_INSTANT_CLAIMS`: Make claims instantly ready
- `AGGKIT_SANDBOX_MOCK_FINALIZATION`: Skip complex finality validation

#### Network Configuration

- `AGGKIT_L1_URL`: L1 node endpoint (default: `http://anvil-l1:8545`)
- `AGGKIT_L2_URL`: L2 node endpoint (default: `http://anvil-l2:8545`)
- `AGGKIT_L1_CHAIN_ID`: L1 chain ID (default: `31337`)
- `AGGKIT_L2_CHAIN_ID`: L2 chain ID (default: `31338`)

#### Service Configuration

- `AGGKIT_REST_PORT`: Bridge API port (default: `5577`)
- `AGGKIT_COMPONENTS`: Components to run (default: `bridge,aggoracle`)
- `AGGKIT_LOG_LEVEL`: Logging level (`debug`, `info`, `warn`, `error`)

### Custom Configuration File

You can also mount a custom TOML configuration file:

```yaml
volumes:
  - ./config/custom.toml:/app/config/custom.toml:ro
```

When a custom config is mounted at `/app/config/custom.toml`, it will be used instead of generating one from environment variables.

## Docker Compose Files

### 1. Complete Setup (`docker-compose.sandbox.yml`)

**Features:**

- Full monitoring and health checks
- Persistent data volumes
- Contract deployment service
- Comprehensive logging
- Network isolation

**Usage:**

```bash
docker-compose -f docker-compose.sandbox.yml up --build
```

### 2. Simple Setup (`docker-compose.simple.yml`)

**Features:**

- Minimal configuration
- Quick startup
- Essential services only

**Usage:**

```bash
docker-compose -f docker-compose.simple.yml up --build
```

## Advanced Usage

### Using External Nodes

To connect to external Ethereum nodes instead of local Anvil:

```yaml
environment:
  AGGKIT_L1_URL: "https://mainnet.infura.io/v3/YOUR_KEY"
  AGGKIT_L2_URL: "https://polygon-rpc.com"
  AGGKIT_L1_CHAIN_ID: "1"
  AGGKIT_L2_CHAIN_ID: "137"
```

### Custom Database

To use PostgreSQL instead of SQLite:

```yaml
environment:
  AGGKIT_DATABASE_DRIVER: "postgres"
  AGGKIT_DATABASE_NAME: "aggkit"
  AGGKIT_DATABASE_HOST: "postgres"
  AGGKIT_DATABASE_PORT: "5432"
  AGGKIT_DATABASE_USER: "aggkit"
  AGGKIT_DATABASE_PASSWORD: "password"
```

### Development Mode

For development with hot reloading:

```yaml
volumes:
  - .:/app/src:ro
  - /app/src/aggkit-sandbox  # Exclude binary
environment:
  AGGKIT_LOG_LEVEL: "debug"
  # Add development-specific settings
```

## Environment-Specific Configurations

### Development

```bash
cp docker/env.sandbox.template .env.dev
# Edit .env.dev with development settings
docker-compose --env-file .env.dev -f docker-compose.sandbox.yml up
```

### Testing

```bash
cp docker/env.sandbox.template .env.test
# Edit .env.test with test settings
docker-compose --env-file .env.test -f docker-compose.sandbox.yml up
```

### Staging

```bash
cp docker/env.sandbox.template .env.staging
# Edit .env.staging with staging settings
docker-compose --env-file .env.staging -f docker-compose.sandbox.yml up
```

## API Testing

Once running, you can test the bridge APIs:

```bash
# Health check
curl http://localhost:5577/

# Get bridges (with sandbox metadata)
curl "http://localhost:5577/bridge/v1/bridges?network_id=1338"

# Get sync status (with sandbox metadata)
curl "http://localhost:5577/bridge/v1/sync-status"

# Get claim proof (with instant processing)
curl "http://localhost:5577/bridge/v1/claim-proof?network_id=1&leaf_index=1&deposit_count=1"
```

## Monitoring and Debugging

### Logs

```bash
# View all logs
docker-compose -f docker-compose.sandbox.yml logs -f

# View specific service logs
docker-compose -f docker-compose.sandbox.yml logs -f aggkit-sandbox

# View with timestamps
docker-compose -f docker-compose.sandbox.yml logs -f -t
```

### Metrics

Access Prometheus metrics at: <http://localhost:8080/metrics>

### Health Checks

```bash
# Check service health
docker-compose -f docker-compose.sandbox.yml ps

# Manual health check
curl http://localhost:5577/
```

## Troubleshooting

### Common Issues

1. **Port Conflicts**

   ```bash
   # Check what's using the ports
   netstat -tulpn | grep -E '(5577|8545|8546)'
   
   # Change ports in docker-compose.yml
   ports:
     - "15577:5577"  # Use different host port
   ```

2. **Node Connection Issues**

   ```bash
   # Check if nodes are accessible
   curl http://localhost:8545
   curl http://localhost:8546
   
   # Check container networking
   docker network inspect aggkit-sandbox_aggkit-sandbox
   ```

3. **Database Issues**

   ```bash
   # Remove persistent data
   docker volume rm aggkit-sandbox_aggkit-data
   
   # Check database file
   docker exec -it aggkit-sandbox ls -la /app/data/
   ```

4. **Configuration Validation**

   ```bash
   # Check generated configuration
   docker exec -it aggkit-sandbox cat /app/config/aggkit-sandbox.toml
   
   # Validate environment variables
   docker exec -it aggkit-sandbox env | grep AGGKIT
   ```

### Debug Mode

Run with debug logging and keep containers running:

```yaml
environment:
  AGGKIT_LOG_LEVEL: "debug"
command: ["sh", "-c", "sleep infinity"]  # Keep container running
```

Then exec into the container:

```bash
docker exec -it aggkit-sandbox /app/entrypoint.sh run
```

## Building Custom Images

### Build Arguments

```bash
docker build -f Dockerfile.sandbox \
  --build-arg GO_VERSION=1.21 \
  --build-arg ALPINE_VERSION=3.18 \
  -t aggkit-sandbox:custom .
```

### Multi-platform Build

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -f Dockerfile.sandbox \
  -t aggkit-sandbox:latest .
```

## Security Considerations

⚠️ **This image is designed for development only**

- Uses default Anvil accounts with known private keys
- Runs with relaxed security settings
- Not suitable for production use
- Database files are stored in containers

## Performance Tuning

### Resource Limits

```yaml
deploy:
  resources:
    limits:
      memory: 1G
      cpus: '0.5'
    reservations:
      memory: 512M
      cpus: '0.25'
```

### Anvil Configuration

```yaml
command: |
  anvil 
  --host 0.0.0.0 
  --port 8545 
  --chain-id 31337
  --accounts 20          # More accounts
  --balance 100000       # More balance
  --gas-limit 50000000   # Higher gas limit
  --block-time 1         # Faster blocks
```

## Support

For issues and questions:

1. Check the logs: `docker-compose logs aggkit-sandbox`
2. Verify configuration: `docker exec aggkit-sandbox env | grep AGGKIT`
3. Test node connectivity: `curl http://localhost:8545`
4. Review the generated config: `docker exec aggkit-sandbox cat /app/config/aggkit-sandbox.toml`

## Examples

See the `examples/` directory for:

- Bridge interaction scripts
- Contract deployment examples
- API testing scenarios
- Integration test suites
