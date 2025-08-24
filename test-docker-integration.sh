#!/bin/bash
# Docker integration test for config migration

set -e

echo "=== Docker Config Migration Integration Test ==="

# Clean up function
cleanup() {
    echo "Cleaning up..."
    docker-compose -f docker-compose.test.yml down -v 2>/dev/null || true
    rm -rf test-config
}

trap cleanup EXIT

# Create test directory and config
mkdir -p test-config
cat > test-config/config.json << EOF
{
  "port": 8080,
  "github_token": "test_github_token_should_be_preserved",
  "copilot_token": "test_copilot_token_should_be_preserved",
  "expires_at": 9999999999,
  "refresh_in": 1500,
  "allowed_models": ["gpt-4"],
  "default_model": "gpt-4",
  "timeouts": {
    "http_client": 200,
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
EOF

echo "Created test config with custom settings and mock tokens"

# Create test docker-compose
cat > docker-compose.test.yml << EOF
services:
  github-copilot-svcs-test:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: github-copilot-svcs-test
    volumes:
      - ./test-config:/home/appuser/.local/share/github-copilot-svcs
    environment:
      - PORT=8080
    command: ["config"]
    networks:
      - test-network

networks:
  test-network:
    driver: bridge
EOF

echo "Building test container..."
docker-compose -f docker-compose.test.yml build --quiet

echo "Testing config migration in container..."
output=$(docker-compose -f docker-compose.test.yml run --rm github-copilot-svcs-test 2>&1)

echo "Container output:"
echo "$output"

# Verify that migration preserved tokens and updated models
if echo "$output" | grep -q "test_github_token_should_be_preserved"; then
    echo "❌ ERROR: Tokens should not be displayed in config output for security"
elif echo "$output" | grep -q "Has GitHub token: true"; then
    echo "✅ PASS: GitHub token preserved"
else
    echo "❌ FAIL: GitHub token not preserved"
    exit 1
fi

if echo "$output" | grep -q "Has Copilot token: true"; then
    echo "✅ PASS: Copilot token preserved"
else
    echo "❌ FAIL: Copilot token not preserved"
    exit 1
fi

if echo "$output" | grep -q "Default model: gpt-5-mini"; then
    echo "✅ PASS: Default model updated to gpt-5-mini"
else
    echo "❌ FAIL: Default model not updated"
    exit 1
fi

if echo "$output" | grep -q "Port: 8080"; then
    echo "✅ PASS: Custom port preserved"
else
    echo "❌ FAIL: Custom port not preserved"
    exit 1
fi

echo ""
echo "=== All Docker integration tests PASSED! ==="
