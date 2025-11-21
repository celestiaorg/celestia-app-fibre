#!/bin/sh

# Test script to verify Fibre provider registration
# This script tests the registration logic without running the full node

set -o errexit
set -o nounset

APP_HOME="${HOME}/.celestia-app"
FIBRE_HOST="localhost:9090"
CHAIN_ID="test"
KEY_NAME="validator"
KEYRING_BACKEND="test"
FEES="500utia"

echo "=== Testing Fibre Provider Registration ==="
echo ""

# Check if node is running
if ! timeout 2 celestia-appd query block 1 --node "tcp://localhost:26657" --home "${APP_HOME}" > /dev/null 2>&1; then
  echo "✗ Error: Node is not running"
  echo "  Please start the node first using: ./scripts/single-node-fibre.sh"
  exit 1
fi

echo "✓ Node is running"
echo ""

# Check current registration status
echo "Checking current Fibre provider registration status..."
providers_output=$(celestia-appd query valaddr providers --node "tcp://localhost:26657" --home "${APP_HOME}" --output json 2>&1)
query_result=$?

if [ $query_result -eq 0 ]; then
  if echo "$providers_output" | grep -q "${FIBRE_HOST}"; then
    echo "✓ Fibre provider is already registered"
    echo ""
    echo "Registered providers:"
    echo "$providers_output" | python3 -m json.tool 2>/dev/null || echo "$providers_output"
    exit 0
  else
    echo "⚠ Fibre provider is NOT registered"
    echo ""
  fi
else
  echo "⚠ Could not query providers (exit code: $query_result)"
  echo "  Output: $providers_output"
  echo ""
fi

# Try to register
echo "Attempting to register Fibre provider..."
echo "  Host: ${FIBRE_HOST}"
echo "  Key: ${KEY_NAME}"
echo ""

tx_output=$(celestia-appd tx valaddr set-host "${FIBRE_HOST}" \
  --from "${KEY_NAME}" \
  --keyring-backend="${KEYRING_BACKEND}" \
  --home "${APP_HOME}" \
  --chain-id "${CHAIN_ID}" \
  --fees "${FEES}" \
  --node "tcp://localhost:26657" \
  --yes 2>&1)

tx_result=$?

if [ $tx_result -eq 0 ]; then
  echo "✓ Transaction submitted successfully"
  echo ""

  # Extract txhash
  txhash=$(echo "$tx_output" | grep -oE 'txhash: [A-F0-9]{64}' | cut -d' ' -f2 || echo "")
  if [ -n "$txhash" ]; then
    echo "  Transaction hash: ${txhash}"
  fi

  echo ""
  echo "Waiting for transaction to be included in a block..."
  sleep 5

  # Verify registration
  echo ""
  echo "Verifying registration..."
  providers_output=$(celestia-appd query valaddr providers --node "tcp://localhost:26657" --home "${APP_HOME}" --output json 2>&1)

  if echo "$providers_output" | grep -q "${FIBRE_HOST}"; then
    echo "✓ Registration verified successfully!"
    echo ""
    echo "Registered providers:"
    echo "$providers_output" | python3 -m json.tool 2>/dev/null || echo "$providers_output"
    exit 0
  else
    echo "⚠ Registration not yet visible (may need more time)"
    echo "  Providers output:"
    echo "$providers_output" | head -20
    exit 1
  fi
else
  echo "✗ Failed to register (exit code: $tx_result)"
  echo ""
  echo "Transaction output:"
  echo "$tx_output" | head -20
  exit 1
fi
