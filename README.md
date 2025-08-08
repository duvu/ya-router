# GitHub Copilot Service Proxy

This project provides a reverse proxy for GitHub Copilot, exposing OpenAI-compatible endpoints for use with tools and clients that expect the OpenAI API. It follows the authentication and token management approach used by [OpenCode](https://github.com/sst/opencode).

## Features

  - Proactive token refresh (refreshes at 20% of token lifetime, minimum 5 minutes)
  - Exponential backoff retry logic for failed token refreshes
  - Automatic fallback to full re-authentication when needed
  - Detailed token status monitoring
  - Automatic retry with exponential backoff for chat completions (3 attempts)
  - Network error recovery and rate limiting handling
  - 30-second request timeout protection

## Downloads

Pre-built binaries are available for each release on the Releases page of this repository.

- **macOS**: AMD64 (Intel), ARM64 (Apple Silicon)
- **Windows**: AMD64, ARM64
### Automated Releases

Releases are automatically created when code is merged to the `main` branch:
- Version numbers follow semantic versioning (starting from v0.0.1)
- Cross-platform binaries are built and attached to each release
- Release notes include download links for all supported platforms

## Performance & Production Features

This service includes enterprise-grade performance optimizations:

### 🚀 HTTP Server Optimizations
- **Connection Pooling**: Shared HTTP client with configurable connection limits (100 max idle, 20 per host)
- **Configurable Timeouts**: Fully customizable timeout settings via `config.json` for all server operations
- **Streaming Support**: Read (30s), Write (300s), and Idle (120s) timeouts optimized for AI chat streaming
- **Long Response Handling**: HTTP client and proxy context timeouts support up to 300s (5 minutes) for extended AI conversations
- **Request Limits**: 5MB request body size limit to prevent memory exhaustion
- **Advanced Transport**: Configurable dial timeout (10s), TLS handshake timeout (10s), keep-alive (30s)

### 🔄 Reliability & Concurrency
- **Circuit Breaker**: Automatic failure detection and recovery (5 failure threshold, 30s timeout)
- **Context Propagation**: Request contexts with 25s timeout and proper cancellation
- **Request Coalescing**: Deduplicates identical concurrent requests to models endpoint
- **Exponential Backoff**: Enhanced retry logic with circuit breaker integration
- **Worker Pool**: Concurrent request processing with dedicated worker goroutines (CPU*2 workers)

### 💾 Resource Management
- **Buffer Pooling**: sync.Pool for request/response buffer reuse to reduce GC pressure
- **Memory Optimization**: Streaming support with 32KB buffers for large responses
- **Graceful Shutdown**: Proper resource cleanup and coordinated shutdown with worker pool termination
- **Shared Clients**: Centralized HTTP client eliminates resource duplication
- **Worker Pool Management**: Automatic worker lifecycle management with graceful termination

### 📊 Monitoring & Observability
- **Profiling Endpoints**: `/debug/pprof/*` for memory, CPU, and goroutine analysis
- **Enhanced Logging**: Circuit breaker state, request coalescing, worker pool metrics, and performance data
- **Health Monitoring**: Detailed `/health` endpoint for load balancer integration
- **Production Metrics**: Built-in support for operational monitoring and worker pool status

## Quickstart with Makefile

If you have `make` installed, you can build, run, and test the project easily:

```bash
make build      # Build the binary
make run        # Start the proxy server
make auth       # Authenticate with GitHub Copilot
make models     # List available models
make config     # Show current configuration
make clean      # Remove the binary
```

## Installation & Usage

### 1. Build the Application
```bash
make build
# or manually:
go build -o github-copilot-svcs
```

### 2. Optional: Configure Timeouts
```bash
# Copy example config and customize timeout values
cp config.example.json ~/.local/share/github-copilot-svcs/config.json
# Edit the timeouts section as needed
```

### 3. First Time Setup & Authentication
```bash
make auth
# or manually:
./github-copilot-svcs auth
```

### 4. Start the Proxy Server
```bash
make run
# or manually:
./github-copilot-svcs run
```

## Docker Usage

Run with Docker to persist configuration across container restarts:

```bash
# Local development with docker-compose
docker-compose up -d

# Or manually with docker run
docker run -d \
  --name github-copilot-svcs \
  -p 7071:7071 \
  -v ./config:/home/appuser/.local/share/github-copilot-svcs \
  docker.x51.vn/dev/github-copilot-svcs:latest
```

**Configuration persistence:**
- Local development: `./config` directory is mounted to container config path
- Production deployment: Host folder `./github-copilot-svcs` is mounted to persist authentication tokens

**First-time authentication in container:**
```bash
# Authenticate inside the container (one-time setup)
docker exec -it github-copilot-svcs /app/github-copilot-svcs auth
```

**Health check:**
```bash
curl http://localhost:7071/health
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `run` / `start` | Start the proxy server |
| `auth`   | Authenticate with GitHub Copilot using device flow |
| `status` | Show detailed authentication and token status |
| `config` | Display current configuration details |
| `models` | List all available AI models |
| `refresh`| Manually force token refresh |
| `version`| Show version information |
| `help`   | Show usage information |

### Enhanced Status Monitoring

The `status` command now provides detailed token information:

```bash
./github-copilot-svcs status
```

Example output:
```
Configuration file: ~/.local/share/github-copilot-svcs/config.json
Port: 7071
Authentication: ✓ Authenticated
Token expires: in 29m 53s (1793 seconds)
Status: ✅ Token is healthy
Has GitHub token: true
Refresh interval: 1500 seconds
```

Status indicators:
- ✅ **Token is healthy**: Token has plenty of time remaining
- ⚠️ **Token will be refreshed soon**: Token is approaching refresh threshold
- ❌ **Token needs refresh**: Token has expired or will expire very soon

## API Endpoints

Once running, the proxy exposes these OpenAI-compatible endpoints:

### Chat Completions
```bash
POST http://localhost:7071/v1/chat/completions
Content-Type: application/json

{
  "model": "gpt-4",
  "messages": [
    {"role": "user", "content": "Hello, world!"}
  ],
  "max_tokens": 100
}
```

### Available Models
```bash
GET http://localhost:7071/v1/models
```

### Health Check
```bash
GET http://localhost:7071/health
```

### Profiling Endpoints (Production Monitoring)
```bash
GET http://localhost:7071/debug/pprof/          # Overview of available profiles
GET http://localhost:7071/debug/pprof/heap      # Memory heap profile
GET http://localhost:7071/debug/pprof/goroutine # Goroutine profile
GET http://localhost:7071/debug/pprof/profile   # CPU profile (30s sampling)
GET http://localhost:7071/debug/pprof/trace     # Execution trace
```

## Reliability & Error Handling

### Automatic Token Management

The proxy implements proactive token management to minimize authentication interruptions:

- **Proactive Refresh**: Tokens are refreshed when 20% of their lifetime remains (typically 5-6 minutes before expiration for 25-minute tokens)
- **Retry Logic**: Failed token refreshes are retried up to 3 times with exponential backoff (2s, 8s, 18s delays)
- **Fallback Authentication**: If token refresh fails completely, the system falls back to full device flow re-authentication
- **Background Monitoring**: Token status is continuously monitored during API requests

### Request Retry Logic

Chat completion requests are automatically retried to handle transient failures:

- **Automatic Retries**: Up to 3 attempts for failed requests
- **Smart Retry Logic**: Only retries on network errors, server errors (5xx), rate limiting (429), and timeouts (408)
- **Exponential Backoff**: Retry delays of 1s, 4s, 9s to avoid overwhelming the API
- **Timeout Protection**: 30-second timeout per request attempt

### Error Recovery

```bash
# Manual token refresh if needed
./github-copilot-svcs refresh

# Check current token status
./github-copilot-svcs status

# Re-authenticate if all else fails
./github-copilot-svcs auth
```

## Configuration

The configuration is stored in `~/.local/share/github-copilot-svcs/config.json`:

```json
{
  "port": 7071,
  "github_token": "gho_...",
  "copilot_token": "ghu_...",
  "expires_at": 1720000000,
  "refresh_in": 1500,
  "timeouts": {
    "http_client": 300,
    "server_read": 30,
    "server_write": 300,
    "server_idle": 120,
    "proxy_context": 300,
    "circuit_breaker": 30,
    "keep_alive": 30,
    "tls_handshake": 10,
    "dial_timeout": 10,
    "idle_conn_timeout": 90
  }
}
```

### Configuration Fields

- `port`: Server port (default: 7071)
- `github_token`: GitHub OAuth token for Copilot access
- `copilot_token`: GitHub Copilot API token
- `expires_at`: Unix timestamp when the Copilot token expires
- `refresh_in`: Seconds until token should be refreshed (typically 1500 = 25 minutes)

### Timeout Configuration

All timeout values are specified in seconds and have sensible defaults:

| Field | Default | Description |
|-------|---------|-------------|
| `http_client` | 300 | HTTP client timeout for outbound requests to GitHub Copilot API |
| `server_read` | 30 | Server timeout for reading incoming requests |
| `server_write` | 300 | Server timeout for writing responses (increased for streaming) |
| `server_idle` | 120 | Server timeout for idle connections |
| `proxy_context` | 300 | Request context timeout for proxy operations |
| `circuit_breaker` | 30 | Circuit breaker recovery timeout when API is failing |
| `keep_alive` | 30 | TCP keep-alive timeout for HTTP connections |
| `tls_handshake` | 10 | TLS handshake timeout |
| `dial_timeout` | 10 | Connection dial timeout |
| `idle_conn_timeout` | 90 | Idle connection timeout in connection pool |

**Streaming Support**: The service is optimized for long-running streaming chat completions with timeouts up to 300 seconds (5 minutes) to support extended AI conversations.

**Custom Configuration**: You can copy `config.example.json` as a starting point and modify timeout values based on your environment:

```bash
cp config.example.json ~/.local/share/github-copilot-svcs/config.json
# Edit the timeouts section as needed
```

## Authentication Flow

The authentication follows GitHub Copilot's OAuth device flow:

1. **Device Authorization**: Generates a device code and user code
2. **User Authorization**: User visits GitHub and enters the user code
3. **Token Exchange**: Polls for GitHub OAuth token
4. **Copilot Token**: Exchanges GitHub token for Copilot API token
5. **Automatic Refresh**: Refreshes Copilot token as needed

## Model Mapping

The proxy automatically maps common model names to GitHub Copilot models:

| Input Model | GitHub Copilot Model | Provider |
|-------------|---------------------|----------|
| `gpt-4o`, `gpt-4.1` | As specified | OpenAI |
| `o3`, `o3-mini`, `o4-mini` | As specified | OpenAI |
| `claude-3.5-sonnet`, `claude-3.7-sonnet`, `claude-3.7-sonnet-thought` | As specified | Anthropic |
| `claude-opus-4`, `claude-sonnet-4` | As specified | Anthropic |
| `gemini-2.5-pro`, `gemini-2.0-flash-001` | As specified | Google |

**Supported Model Categories:**
- **OpenAI GPT Models**: GPT-4o, GPT-4.1, O3/O4 reasoning models
- **Anthropic Claude Models**: Claude 3.5/3.7 Sonnet variants, Claude Opus/Sonnet 4
- **Google Gemini Models**: Gemini 2.0/2.5 Pro and Flash models

## Security

- Tokens are stored securely in the user's home directory with restricted permissions (0700)
- All communication with GitHub Copilot uses HTTPS
- No sensitive data is logged
- Automatic token refresh prevents long-lived token exposure

## Troubleshooting

### Authentication Issues
```bash
# Re-authenticate
./github-copilot-svcs auth

# Check current status
./github-copilot-svcs status
```

### Connection Issues
```bash
# Check if service is running
curl http://localhost:7071/health

# View logs (if running in foreground)
./github-copilot-svcs run
```

### Port Conflicts
```bash
# Use a different port
# Edit ~/.local/share/github-copilot-svcs/config.json
# Or delete config file and restart to select new port
```

## Integration Examples

### Using with curl
```bash
curl -X POST http://localhost:7071/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Write a hello world in Python"}],
    "max_tokens": 100
  }'
```

### Using with OpenAI Python Client
```python
import openai

# Point OpenAI client to the proxy
client = openai.OpenAI(
    base_url="http://localhost:7071/v1",
    api_key="dummy"  # Not used, but required by client
)

response = client.chat.completions.create(
    model="gpt-4",
    messages=[{"role": "user", "content": "Hello, world!"}]
)
print(response.choices[0].message.content)
```

### Using with LangChain
```python
from langchain.llms import OpenAI

llm = OpenAI(
    openai_api_base="http://localhost:7071/v1",
    openai_api_key="dummy"  # Not used
)
response = llm("Write a hello world in Python")
print(response)
```

## Development

### Project Structure
```
github-copilot-svcs/
├── main.go        # Main application and CLI
├── auth.go        # GitHub Copilot authentication
├── proxy.go       # Reverse proxy implementation
├── server.go      # Server utilities and graceful shutdown
├── transform.go   # Request/response transformation
├── cli.go         # CLI command handling
├── go.mod         # Go module definition
└── README.md      # This documentation
```

### Building from Source
```bash
git clone <repository>
cd github-copilot-svcs
make build
# or manually:
go mod tidy
go build -o github-copilot-svcs
```

### Running Tests
```bash
make test
# or manually:
go test ./...
```

## License

Apache License 2.0 - see LICENSE file for details.

This is free software: you are free to change and redistribute it under the terms of the Apache 2.0 license.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test thoroughly
5. Submit a pull request

## Support

For issues and questions:
1. Check the troubleshooting section
2. Review the logs for error messages
3. Open an issue with detailed information about your setup and the problem
