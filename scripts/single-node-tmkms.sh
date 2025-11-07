#!/bin/sh

# This script starts a single node testnet with tmkms (Tendermint Key Management System).
# It sets up a validator that uses tmkms for signing instead of local key storage.
#
# Prerequisites:
#   - celestia-appd binary installed (run 'make install')
#   - tmkms installed with softsign feature:
#     cargo install tmkms --features=softsign
#     OR build from source:
#     git clone https://github.com/iqlusioninc/tmkms.git && cd tmkms
#     cargo build --release --features=softsign
#
# Usage:
#   ./scripts/single-node-tmkms.sh [home_directory]
#
# To verify the Fibre server starts successfully:
#   1. Check the validator logs for "Fibre server" messages
#   2. The gRPC server should be enabled (--grpc.enable flag)
#   3. Look for successful connection between validator and tmkms
#
# NOTE: Currently, this script configures tmkms and sets priv_validator_laddr,
# but the validator may still use a file-based validator due to application code
# that always loads LoadOrGenFilePV. The Fibre server should still start correctly
# as it only needs the validator's public key, not the signing mechanism.
# To fully use tmkms for signing, the application code may need modification.

set -o errexit # Stop script execution if an error is encountered
set -o nounset # Stop script execution if an undefined variable is used

# Constants
CHAIN_ID="test"
KEY_NAME="validator"
KEYRING_BACKEND="test"
FEES="500utia"
APP_HOME="${HOME}/.celestia-app"
KMS_HOME="${APP_HOME}/tmkms"
PRIV_VALIDATOR_LADDR="tcp://127.0.0.1:26658"

VERSION=$(celestia-appd version 2>&1)
GENESIS_FILE="${APP_HOME}/config/genesis.json"
TMKMS_CONFIG="${KMS_HOME}/tmkms.toml"
PRIV_VALIDATOR_KEY_FILE="${APP_HOME}/config/priv_validator_key.json"

echo "celestia-app version: ${VERSION}"
echo "celestia-app home: ${APP_HOME}"
echo "tmkms home: ${KMS_HOME}"
echo "celestia-app genesis file: ${GENESIS_FILE}"
echo ""

setupTmkms() {
    echo "Setting up tmkms..."

    # Create tmkms directory structure
    mkdir -p "${KMS_HOME}/secrets"
    mkdir -p "${KMS_HOME}/state"

    # Initialize tmkms if not already initialized
    if [ ! -f "${TMKMS_CONFIG}" ]; then
        echo "Initializing tmkms configuration..."
        tmkms init "${KMS_HOME}" > /dev/null 2>&1 || true

        # Check if tmkms init created the identity key (it should be in secrets/)
        KMS_IDENTITY_KEY="${KMS_HOME}/secrets/kms-identity.key"
        if [ ! -f "${KMS_IDENTITY_KEY}" ]; then
            echo "Generating KMS identity key..."
            # Generate a random key for KMS identity (32 bytes hex)
            openssl rand -hex 32 > "${KMS_IDENTITY_KEY}" 2>/dev/null || {
                # Fallback if openssl is not available
                head -c 32 /dev/urandom | xxd -p -c 32 > "${KMS_IDENTITY_KEY}" 2>/dev/null || {
                    echo "Warning: Could not generate KMS identity key. You may need to generate it manually."
                    echo "  Run: openssl rand -hex 32 > ${KMS_IDENTITY_KEY}"
                }
            }
            chmod 600 "${KMS_IDENTITY_KEY}" 2>/dev/null || true
        fi

        # Configure tmkms.toml for softsign backend and validator connection
        # Overwrite the config file with our custom configuration
        cat > "${TMKMS_CONFIG}" <<EOF
# Tendermint KMS Configuration File

[[chain]]
id = "${CHAIN_ID}"
key_format = { type = "bech32", account_key_prefix = "celestia", consensus_key_prefix = "celestiavalcons" }
state_file = "${KMS_HOME}/state/${CHAIN_ID}-consensus.json"

[[providers.softsign]]
chain_ids = ["${CHAIN_ID}"]
key_type = "consensus"
path = "${KMS_HOME}/secrets/consensus.key"

[[validator]]
chain_id = "${CHAIN_ID}"
addr = "${PRIV_VALIDATOR_LADDR}"
secret_key = "${KMS_IDENTITY_KEY}"
protocol_version = "v0.38"
reconnect = true
EOF

        echo "tmkms configuration created at ${TMKMS_CONFIG}"
    else
        echo "tmkms configuration already exists at ${TMKMS_CONFIG}"
        # Ensure KMS identity key exists (in secrets/ directory)
        KMS_IDENTITY_KEY="${KMS_HOME}/secrets/kms-identity.key"
        if [ ! -f "${KMS_IDENTITY_KEY}" ]; then
            echo "Warning: KMS identity key not found. Generating..."
            openssl rand -hex 32 > "${KMS_IDENTITY_KEY}" 2>/dev/null || {
                head -c 32 /dev/urandom | xxd -p -c 32 > "${KMS_IDENTITY_KEY}" 2>/dev/null || true
            }
            chmod 600 "${KMS_IDENTITY_KEY}" 2>/dev/null || true
        fi
    fi

    # Import validator key into tmkms if it exists and key file doesn't exist yet
    if [ -f "${PRIV_VALIDATOR_KEY_FILE}" ] && [ ! -f "${KMS_HOME}/secrets/consensus.key" ]; then
        echo "Importing validator key into tmkms..."
        # Use tmkms softsign import command - it expects JSON format and converts to base64 text
        # The command takes input and output as positional arguments
        IMPORT_OUTPUT=$(tmkms softsign import -f json "${PRIV_VALIDATOR_KEY_FILE}" "${KMS_HOME}/secrets/consensus.key" 2>&1)
        IMPORT_EXIT=$?
        if [ $IMPORT_EXIT -eq 0 ]; then
            echo "Successfully imported key using tmkms softsign import"
        else
            # If tmkms import fails, show the error and try manual import
            echo "tmkms softsign import failed with exit code $IMPORT_EXIT"
            echo "Import error: $IMPORT_OUTPUT"
            echo "Attempting manual import..."
            importKeyManually
        fi
        chmod 600 "${KMS_HOME}/secrets/consensus.key" 2>/dev/null || true

        # Verify the key file was created and has content
        if [ ! -f "${KMS_HOME}/secrets/consensus.key" ] || [ ! -s "${KMS_HOME}/secrets/consensus.key" ]; then
            echo "Error: Failed to create consensus key file at ${KMS_HOME}/secrets/consensus.key"
            exit 1
        fi
    elif [ ! -f "${PRIV_VALIDATOR_KEY_FILE}" ]; then
        echo "Warning: Validator key file not found at ${PRIV_VALIDATOR_KEY_FILE}"
        echo "tmkms will need the key to be imported manually after the validator is created."
    else
        echo "tmkms key already exists at ${KMS_HOME}/secrets/consensus.key"
    fi
}

importKeyManually() {
    # Extract the private key from priv_validator_key.json
    # tmkms expects the key file to be base64-encoded text, not binary
    # So we keep it as base64 text (don't decode it)
    if command -v jq > /dev/null 2>&1; then
        PRIV_KEY_B64=$(jq -r '.priv_key.value' "${PRIV_VALIDATOR_KEY_FILE}" 2>/dev/null)
        if [ -n "${PRIV_KEY_B64}" ] && [ "${PRIV_KEY_B64}" != "null" ]; then
            # Write as base64 text (tmkms expects this format)
            echo "${PRIV_KEY_B64}" > "${KMS_HOME}/secrets/consensus.key" && {
                echo "Successfully imported key using jq (base64 text format)"
                return 0
            }
        fi
    fi

    # Fallback: try to extract using grep/sed
    PRIV_KEY_B64=$(grep -o '"value":"[^"]*"' "${PRIV_VALIDATOR_KEY_FILE}" | head -1 | sed 's/"value":"\([^"]*\)"/\1/')
    if [ -n "${PRIV_KEY_B64}" ]; then
        # Write as base64 text (tmkms expects this format)
        echo "${PRIV_KEY_B64}" > "${KMS_HOME}/secrets/consensus.key" && {
            echo "Successfully imported key using grep/sed (base64 text format)"
            return 0
        }
    fi

    echo "Warning: Could not automatically import key."
    echo "Please import manually using:"
    echo "  tmkms softsign import ${PRIV_VALIDATOR_KEY_FILE} ${KMS_HOME}/secrets/consensus.key"
}

createGenesis() {
    echo "Initializing validator and node config files..."
    celestia-appd init ${CHAIN_ID} \
      --chain-id ${CHAIN_ID} \
      --home "${APP_HOME}" \
      > /dev/null 2>&1 # Hide output to reduce terminal noise

    echo "Adding a new key to the keyring..."
    celestia-appd keys add ${KEY_NAME} \
      --keyring-backend=${KEYRING_BACKEND} \
      --home "${APP_HOME}" \
      > /dev/null 2>&1 # Hide output to reduce terminal noise

    echo "Adding genesis account..."
    celestia-appd genesis add-genesis-account \
      "$(celestia-appd keys show ${KEY_NAME} -a --keyring-backend=${KEYRING_BACKEND} --home "${APP_HOME}")" \
      "1000000000000000utia" \
      --home "${APP_HOME}"

    echo "Creating a genesis tx..."
    celestia-appd genesis gentx ${KEY_NAME} 5000000000utia \
      --fees ${FEES} \
      --keyring-backend=${KEYRING_BACKEND} \
      --chain-id ${CHAIN_ID} \
      --home "${APP_HOME}" \
      --commission-rate=0.05 \
      --commission-max-rate=1.0 \
      --commission-max-change-rate=1.0 \
      > /dev/null 2>&1 # Hide output to reduce terminal noise

    echo "Collecting genesis txs..."
    celestia-appd genesis collect-gentxs \
      --home "${APP_HOME}" \
        > /dev/null 2>&1 # Hide output to reduce terminal noise

    # Override the default RPC server listening address
    sed -i.bak 's#"tcp://127.0.0.1:26657"#"tcp://0.0.0.0:26657"#g' "${APP_HOME}"/config/config.toml

    # Configure validator to use remote signer (tmkms)
    # echo "Configuring validator to use tmkms remote signer..."
    sed -i.bak -e "s#^priv_validator_laddr = \"\"#priv_validator_laddr = \"${PRIV_VALIDATOR_LADDR}\"#g" "${APP_HOME}/config/config.toml"

    # Enable transaction indexing
    sed -i.bak 's#"null"#"kv"#g' "${APP_HOME}"/config/config.toml

    # Persist ABCI responses
    sed -i.bak 's#discard_abci_responses = true#discard_abci_responses = false#g' "${APP_HOME}"/config/config.toml

    # Override the log level to reduce noisy logs
    sed -i.bak 's#log_level = "info"#log_level = "*:error,p2p:info,state:info"#g' "${APP_HOME}"/config/config.toml

    # Override the VotingPeriod from 1 week to 30 seconds
    sed -i.bak 's#"604800s"#"30s"#g' "${APP_HOME}"/config/genesis.json

    trace_type="local"
    sed -i.bak -e "s/^trace_type *=.*/trace_type = \"$trace_type\"/" ${APP_HOME}/config/config.toml

    trace_pull_address=":26661"
    sed -i.bak -e "s/^trace_pull_address *=.*/trace_pull_address = \"$trace_pull_address\"/" ${APP_HOME}/config/config.toml

    trace_push_batch_size=1000
    sed -i.bak -e "s/^trace_push_batch_size *=.*/trace_push_batch_size = \"$trace_push_batch_size\"/" ${APP_HOME}/config/config.toml

    echo "Tracing is set up with the ability to pull traced data from the node on the address http://127.0.0.1${trace_pull_address}"
    echo "Validator is configured to use tmkms at ${PRIV_VALIDATOR_LADDR}"
}

deleteCelestiaAppHome() {
    echo "Deleting $APP_HOME..."
    rm -rf "$APP_HOME"
}

startTmkms() {
    echo "Starting tmkms..."
    # Start tmkms in the background
    # Note: tmkms will connect to the validator, so the validator must be running first
    tmkms start -c "${TMKMS_CONFIG}" > "${KMS_HOME}/tmkms.log" 2>&1 &
    TMKMS_PID=$!
    echo "tmkms started with PID: ${TMKMS_PID}"
    echo "tmkms logs: ${KMS_HOME}/tmkms.log"
    echo "Note: tmkms will attempt to connect to the validator. Connection errors are normal until the validator starts."
}

startCelestiaApp() {
    echo "Starting celestia-app with tmkms..."
    echo "Note: The Fibre server should start automatically when gRPC is enabled."
    echo "Look for 'Fibre server' messages in the logs to verify it started successfully."
    echo ""
    echo "The validator will listen on ${PRIV_VALIDATOR_LADDR} for tmkms connections."
    echo ""
    # Start the validator - it will listen on priv_validator_laddr
    celestia-appd start \
      --home "${APP_HOME}" \
      --api.enable \
      --grpc.enable \
      --grpc-web.enable \
      --delayed-precommit-timeout 1s &
    VALIDATOR_PID=$!
    echo "Validator started with PID: ${VALIDATOR_PID}"
    echo "Waiting for validator to initialize remote signer listener..."

    # Wait longer for the validator to fully initialize and start listening on priv_validator_laddr
    # The remote signer listener may take a few seconds to start
    sleep 5
    startTmkms

    # Wait for the validator process (this will block until it exits)
    wait ${VALIDATOR_PID}
}

# Cleanup function
cleanup() {
    echo ""
    echo "Cleaning up..."
    if [ -n "${TMKMS_PID:-}" ]; then
        echo "Stopping tmkms (PID: ${TMKMS_PID})..."
        kill ${TMKMS_PID} 2>/dev/null || true
        # Wait a moment for it to exit
        sleep 1
        # Force kill if still running
        kill -9 ${TMKMS_PID} 2>/dev/null || true
    fi
    if [ -n "${VALIDATOR_PID:-}" ]; then
        echo "Stopping validator (PID: ${VALIDATOR_PID})..."
        # Send SIGTERM first for graceful shutdown
        kill ${VALIDATOR_PID} 2>/dev/null || true
        # Wait a moment for graceful shutdown
        sleep 2
        # Force kill if still running
        kill -9 ${VALIDATOR_PID} 2>/dev/null || true
    fi
    exit 0
}

# Set up signal handlers BEFORE starting any processes
trap cleanup INT TERM

# Main execution
deleteCelestiaAppHome
createGenesis
setupTmkms
startCelestiaApp
