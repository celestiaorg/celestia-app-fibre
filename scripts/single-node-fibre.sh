#!/bin/sh

# This script starts a single node testnet on the latest app version with Fibre DA enabled.

set -o errexit # Stop script execution if an error is encountered
set -o nounset # Stop script execution if an undefined variable is used

# Constants
CHAIN_ID="test"
KEY_NAME="validator"
KEYRING_BACKEND="test"
FEES="5000utia"
FIBRE_HOST="localhost:9090"  # Fibre DA server runs on the same gRPC server

VERSION=$(celestia-appd version 2>&1)
APP_HOME="${HOME}/.celestia-app"
GENESIS_FILE="${APP_HOME}/config/genesis.json"
CELESTIA_APP_PID=""

# Cleanup function to kill background celestia-appd process
cleanup() {
  if [ -n "${CELESTIA_APP_PID}" ]; then
    echo ""
    echo "Stopping celestia-appd (PID: ${CELESTIA_APP_PID})..."
    kill "${CELESTIA_APP_PID}" 2>/dev/null || true
    wait "${CELESTIA_APP_PID}" 2>/dev/null || true
    echo "celestia-appd stopped."
  fi
  exit 0
}

# Set up signal handlers to cleanup on script exit
trap cleanup INT TERM EXIT

echo "celestia-app version: ${VERSION}"
echo "celestia-app home: ${APP_HOME}"
echo "celestia-app genesis file: ${GENESIS_FILE}"
echo ""

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

    # Enable transaction indexing
    sed -i.bak 's#"null"#"kv"#g' "${APP_HOME}"/config/config.toml

    # Persist ABCI responses
    sed -i.bak 's#discard_abci_responses = true#discard_abci_responses = false#g' "${APP_HOME}"/config/config.toml

    # Override the log level to reduce noisy logs
    # sed -i.bak 's#log_level = "info"#log_level = "info"#g' "${APP_HOME}"/config/config.toml
}

deleteCelestiaAppHome() {
    echo "Deleting $APP_HOME..."
    rm -r "$APP_HOME"
}

registerFibreProviderInfo() {
  sleep 10
  echo "Registering Fibre provider info..."
  celestia-appd tx valaddr set-host "${FIBRE_HOST}" \
      --from "${KEY_NAME}" \
      --keyring-backend="${KEYRING_BACKEND}" \
      --home "${APP_HOME}" \
      --chain-id "${CHAIN_ID}" \
      --fees "${FEES}" \
      --yes

  sleep 7
  echo "Querying Fibre provider info..."
  celestia-appd query valaddr providers --home "${APP_HOME}" --output json
}

startCelestiaApp() {
  echo "Starting celestia-app in background..."
  celestia-appd start \
    --home "${APP_HOME}" \
    --api.enable \
    --grpc.enable \
    --grpc-web.enable \
    --delayed-precommit-timeout 6s &

  CELESTIA_APP_PID=$!
  echo "celestia-appd started with PID: ${CELESTIA_APP_PID}"
}

deleteCelestiaAppHome
createGenesis
startCelestiaApp
registerFibreProviderInfo

# Keep script running and wait for celestia-appd process
# This allows logs to continue streaming and CTRL+C will trigger cleanup
wait "${CELESTIA_APP_PID}"
