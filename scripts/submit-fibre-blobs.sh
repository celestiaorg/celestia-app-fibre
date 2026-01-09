#!/bin/sh

# This script submits a single Fibre blob to the single node testnet.
# Prerequisite: Run ./scripts/single-node-fibre.sh first.
#
# The script uses `go run` to execute the submit-fibre-blob tool, so Go must be installed.
# It will submit a single random blob with a random namespace.

set -o errexit # Stop script execution if an error is encountered
set -o nounset # Stop script execution if an undefined variable is used

# Constants
CHAIN_ID="test"
KEY_NAME="validator"
PFB_KEY_NAME="pfb-sender"  # Second account for PFB transactions
KEYRING_BACKEND="test"
GRPC_ADDR="localhost:9090"
DEPOSIT_AMOUNT="1000000utia"
FEES="5000utia"
FUND_AMOUNT="10000000utia"  # Amount to fund the PFB account

VERSION=$(celestia-appd version 2>&1)
APP_HOME="${HOME}/.celestia-app"
GENESIS_FILE="${APP_HOME}/config/genesis.json"

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# Get the project root (assuming script is in scripts/ directory)
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "celestia-app version: ${VERSION}"
echo "celestia-app home: ${APP_HOME}"
echo "celestia-app genesis file: ${GENESIS_FILE}"
echo "gRPC address: ${GRPC_ADDR}"
echo ""

# Check if celestia-appd binary exists
if ! command -v celestia-appd >/dev/null 2>&1; then
    echo "Error: celestia-appd binary not found in PATH"
    echo "Please install it using 'make install' or ensure it's in your PATH"
    exit 1
fi

# Check if Go is installed
if ! command -v go >/dev/null 2>&1; then
    echo "Error: Go is not installed or not in PATH"
    echo "Please install Go to run the submit-fibre-blob tool"
    exit 1
fi

# Check if the tool directory exists
if [ ! -d "${PROJECT_ROOT}/tools/submit-fibre-blob" ]; then
    echo "Error: submit-fibre-blob tool directory not found at ${PROJECT_ROOT}/tools/submit-fibre-blob"
    exit 1
fi

# Check if main.go exists
if [ ! -f "${PROJECT_ROOT}/tools/submit-fibre-blob/main.go" ]; then
    echo "Error: main.go not found in ${PROJECT_ROOT}/tools/submit-fibre-blob"
    exit 1
fi

# Check if node is running by checking if we can connect to gRPC
if ! timeout 2 nc -z $(echo "${GRPC_ADDR}" | cut -d: -f1) $(echo "${GRPC_ADDR}" | cut -d: -f2) 2>/dev/null; then
    echo "Warning: Cannot connect to gRPC server at ${GRPC_ADDR}"
    echo "Make sure the node is running (start with ./scripts/single-node-fibre.sh)"
fi

# Check if validator key exists
if ! celestia-appd keys show "${KEY_NAME}" --keyring-backend="${KEYRING_BACKEND}" --home "${APP_HOME}" >/dev/null 2>&1; then
    echo "Error: Key '${KEY_NAME}' not found in keyring"
    echo "Make sure you've run ./scripts/single-node-fibre.sh first"
    exit 1
fi

# Create second account for PFB if it doesn't exist
if ! celestia-appd keys show "${PFB_KEY_NAME}" --keyring-backend="${KEYRING_BACKEND}" --home "${APP_HOME}" >/dev/null 2>&1; then
    echo "Creating second account '${PFB_KEY_NAME}' for PFB transactions..."
    celestia-appd keys add "${PFB_KEY_NAME}" \
        --keyring-backend="${KEYRING_BACKEND}" \
        --home "${APP_HOME}" \
        > /dev/null 2>&1
    echo "✓ Account '${PFB_KEY_NAME}' created"
else
    echo "Account '${PFB_KEY_NAME}' already exists"
fi

# Get PFB account address
PFB_ADDRESS=$(celestia-appd keys show "${PFB_KEY_NAME}" -a --keyring-backend="${KEYRING_BACKEND}" --home "${APP_HOME}")

# Check if PFB account has balance, if not fund it
# Query balance - if it fails or returns empty/zero, we need to fund it
PFB_BALANCE_JSON=$(celestia-appd query bank balances "${PFB_ADDRESS}" --home "${APP_HOME}" --chain-id "${CHAIN_ID}" --output json 2>/dev/null || echo "")
if [ -z "${PFB_BALANCE_JSON}" ] || echo "${PFB_BALANCE_JSON}" | grep -q '"amount":"0"' || ! echo "${PFB_BALANCE_JSON}" | grep -q '"amount"'; then
    echo "Funding PFB account '${PFB_KEY_NAME}' (${PFB_ADDRESS}) with ${FUND_AMOUNT}..."
    FUND_TX_OUTPUT=$(celestia-appd tx bank send "${KEY_NAME}" "${PFB_ADDRESS}" "${FUND_AMOUNT}" \
        --chain-id "${CHAIN_ID}" \
        --from "${KEY_NAME}" \
        --keyring-backend="${KEYRING_BACKEND}" \
        --home "${APP_HOME}" \
        --fees "${FEES}" \
        --yes 2>&1)

    # Extract transaction hash from output
    FUND_TXHASH=$(echo "${FUND_TX_OUTPUT}" | grep -o 'txhash: [A-F0-9]*' | cut -d' ' -f2 || echo "")

    if [ -n "${FUND_TXHASH}" ]; then
        echo "✓ Funding transaction submitted (hash: ${FUND_TXHASH})"
        echo "  Waiting for transaction to be included in a block..."
        # Wait for transaction to be included (poll up to 10 times with 1 second delay)
        MAX_WAIT=10
        WAIT_COUNT=0
        while [ $WAIT_COUNT -lt $MAX_WAIT ]; do
            sleep 1
            TX_RESULT=$(celestia-appd query tx "${FUND_TXHASH}" --home "${APP_HOME}" --chain-id "${CHAIN_ID}" 2>/dev/null || echo "")
            if [ -n "${TX_RESULT}" ] && echo "${TX_RESULT}" | grep -q "code: 0"; then
                echo "✓ Funding transaction confirmed"
                break
            fi
            WAIT_COUNT=$((WAIT_COUNT + 1))
        done
        if [ $WAIT_COUNT -eq $MAX_WAIT ]; then
            echo "⚠ Warning: Funding transaction not confirmed yet, but continuing..."
        fi
    else
        echo "⚠ Warning: Could not extract transaction hash, waiting 3 seconds..."
        sleep 3
    fi
else
    echo "✓ PFB account '${PFB_KEY_NAME}' already has balance"
fi

# Deposit to escrow account (creates account if it doesn't exist)
echo "Depositing to escrow account..."
ESCROW_TX_OUTPUT=$(celestia-appd tx fibre deposit-to-escrow "${DEPOSIT_AMOUNT}" \
    --from "${KEY_NAME}" \
    --keyring-backend="${KEYRING_BACKEND}" \
    --home "${APP_HOME}" \
    --chain-id "${CHAIN_ID}" \
    --fees "${FEES}" \
    --yes 2>&1)

# Check if deposit was successful
if echo "${ESCROW_TX_OUTPUT}" | grep -q "code: 0"; then
    ESCROW_TXHASH=$(echo "${ESCROW_TX_OUTPUT}" | grep -o 'txhash: [A-F0-9]*' | cut -d' ' -f2 || echo "")
    if [ -n "${ESCROW_TXHASH}" ]; then
        echo "✓ Escrow deposit submitted (hash: ${ESCROW_TXHASH})"
        echo "  Waiting for transaction to be included..."
        # Wait for transaction to be included
        MAX_WAIT=10
        WAIT_COUNT=0
        while [ $WAIT_COUNT -lt $MAX_WAIT ]; do
            sleep 1
            TX_RESULT=$(celestia-appd query tx "${ESCROW_TXHASH}" --home "${APP_HOME}" --chain-id "${CHAIN_ID}" 2>/dev/null || echo "")
            if [ -n "${TX_RESULT}" ] && echo "${TX_RESULT}" | grep -q "code: 0"; then
                echo "✓ Escrow deposit confirmed"
                break
            fi
            WAIT_COUNT=$((WAIT_COUNT + 1))
        done
        if [ $WAIT_COUNT -eq $MAX_WAIT ]; then
            echo "⚠ Warning: Escrow deposit not confirmed yet, but continuing..."
        fi
    else
        echo "⚠ Warning: Could not extract transaction hash, waiting 3 seconds..."
        sleep 3
    fi
elif echo "${ESCROW_TX_OUTPUT}" | grep -q "account sequence mismatch"; then
    echo "⚠ Sequence mismatch detected, waiting a bit longer and retrying..."
    sleep 5
    # Retry the deposit
    ESCROW_TX_OUTPUT=$(celestia-appd tx fibre deposit-to-escrow "${DEPOSIT_AMOUNT}" \
        --from "${KEY_NAME}" \
        --keyring-backend="${KEYRING_BACKEND}" \
        --home "${APP_HOME}" \
        --chain-id "${CHAIN_ID}" \
        --fees "${FEES}" \
        --yes 2>&1)
    if echo "${ESCROW_TX_OUTPUT}" | grep -q "code: 0"; then
        echo "✓ Escrow deposit successful on retry"
        sleep 3
    else
        echo "✗ Escrow deposit failed even after retry:"
        echo "${ESCROW_TX_OUTPUT}"
        exit 1
    fi
else
    echo "✗ Escrow deposit failed:"
    echo "${ESCROW_TX_OUTPUT}"
    exit 1
fi

# Submit both PFB and Fibre blob in parallel from different accounts
# This avoids sequence number conflicts and maximizes chance they're in same block
echo "Submitting PFB and Fibre blob in parallel from different accounts..."

# Submit PFB from second account in background (sync mode - goes to mempool)
(celestia-appd tx blob pay-for-blob 0x00010203040506070809 0x48656c6c6f2c20576f726c6421 \
    --chain-id "${CHAIN_ID}" \
    --from "${PFB_KEY_NAME}" \
    --keyring-backend="${KEYRING_BACKEND}" \
    --home "${APP_HOME}" \
    --fees 21000utia \
    --broadcast-mode sync \
    --yes > /tmp/pfb_output.log 2>&1) &
PFB_PID=$!

# Submit Fibre blob from validator account immediately (also goes to mempool)
# Since they're from different accounts, they can be submitted in parallel
echo "Submitting Fibre blob from '${KEY_NAME}' account..."
if (cd "${PROJECT_ROOT}" && go run ./tools/submit-fibre-blob \
    --chain-id "${CHAIN_ID}" \
    --key-name "${KEY_NAME}" \
    --keyring-backend="${KEYRING_BACKEND}" \
    --home "${APP_HOME}" \
    --grpc-addr "${GRPC_ADDR}"); then
    # Wait for PFB to complete
    wait $PFB_PID 2>/dev/null
    PFB_EXIT=$?
    if [ $PFB_EXIT -eq 0 ]; then
        echo "✓ Both transactions submitted successfully"
        echo "  PFB from '${PFB_KEY_NAME}' account"
        echo "  Fibre blob from '${KEY_NAME}' account"
        echo "  They should be included in the same block"
        exit 0
    else
        echo "⚠ Fibre blob submitted, but PFB failed (check /tmp/pfb_output.log)"
        cat /tmp/pfb_output.log 2>/dev/null || true
        exit 1
    fi
else
    # Kill PFB if Fibre blob submission failed
    kill $PFB_PID 2>/dev/null || true
    echo "✗ Fibre blob submission failed"
    exit 1
fi
