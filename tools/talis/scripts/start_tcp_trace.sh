#!/bin/bash
# Starts a tshark-based TCP capture and writes pcaps into the default traces directory.

SCRIPT_SOURCED=0
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
  SCRIPT_SOURCED=1
fi

TRACE_DIR="${TRACE_DIR:-/root/.celestia-app/data/traces}"
LOG_DIR="${LOG_DIR:-/root/logs}"
LOG_FILE="${LOG_FILE:-}"
PID_FILE="${TRACE_DIR}/tcp-trace.pid"
TRACE_PREFIX="${TRACE_PREFIX:-tcp-trace}"
TRACE_FILTER="${TRACE_FILTER:-tcp}"
TRACE_INTERFACE="${TRACE_INTERFACE:-}"
TRACE_RING_DURATION="${TRACE_RING_DURATION:-300}"
TRACE_RING_FILES="${TRACE_RING_FILES:-6}"
TRACE_RING_FILESIZE_MB="${TRACE_RING_FILESIZE_MB:-0}"
TRACE_SNAPLEN="${TRACE_SNAPLEN:-0}"
TRACE_FORCE_RESTART="${TRACE_FORCE_RESTART:-0}"
TRACE_PATH=""

log() {
  local ts
  ts="$(date --iso-8601=seconds)"
  echo "[$ts] $*"
}

ensure_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    echo "This script must be run as root." >&2
    exit 1
  fi
}

ensure_tshark() {
  if ! command -v tshark >/dev/null 2>&1; then
    log "Installing tshark..."
    export DEBIAN_FRONTEND=noninteractive
    export DEBCONF_NONINTERACTIVE_SEEN=true
    apt-get update -y -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold"
    apt-get install -y -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold" tshark
  fi
}

detect_interface() {
  if [[ -n "${TRACE_INTERFACE}" ]]; then
    return
  fi
  local detected
  detected="$(ip -o route show to default 2>/dev/null | awk '{print $5; exit}')"
  if [[ -n "${detected}" ]]; then
    TRACE_INTERFACE="${detected}"
  else
    TRACE_INTERFACE="any"
  fi
}

source_chain_vars() {
  local vars_file="/root/payload/vars.sh"
  if [[ -f "${vars_file}" ]]; then
    # shellcheck disable=SC1090
    source "${vars_file}"
  fi
}

ensure_dirs() {
  mkdir -p "${TRACE_DIR}"
  if mkdir -p "${LOG_DIR}" 2>/dev/null; then
    LOG_FILE="${LOG_DIR}/tcp-trace.log"
    return
  fi

  if [[ -e "${LOG_DIR}" && ! -d "${LOG_DIR}" ]]; then
    log "LOG_DIR ${LOG_DIR} is a file; logging directly to it."
    LOG_FILE="${LOG_DIR}"
    return
  fi

  log "Unable to prepare LOG_DIR ${LOG_DIR}; defaulting logs to ${TRACE_DIR}/tcp-trace.log."
  LOG_FILE="${TRACE_DIR}/tcp-trace.log"
}

handle_existing_capture() {
  if [[ -f "${PID_FILE}" ]]; then
    local existing_pid
    existing_pid="$(cat "${PID_FILE}")"
    if [[ -n "${existing_pid}" && -d "/proc/${existing_pid}" ]]; then
      if [[ "${TRACE_FORCE_RESTART}" == "1" ]]; then
        log "Stopping existing tshark capture (PID ${existing_pid})."
        kill "${existing_pid}" || true
        sleep 1
      else
        log "An existing tshark capture (PID ${existing_pid}) is running. Set TRACE_FORCE_RESTART=1 to replace it."
        exit 0
      fi
    fi
    rm -f "${PID_FILE}"
  fi
}

build_trace_path() {
  local timestamp hostname chain_id
  timestamp="$(date +%Y%m%d-%H%M%S)"
  hostname="$(hostname)"
  chain_id="${CHAIN_ID:-unknown-chain}"
  TRACE_PATH="${TRACE_DIR}/${chain_id}_${hostname}_${TRACE_PREFIX}_${timestamp}.pcapng"
}

start_capture() {
  local cmd=(tshark -i "${TRACE_INTERFACE}" -f "${TRACE_FILTER}" -w "${TRACE_PATH}")

  if [[ "${TRACE_RING_DURATION}" != "0" ]]; then
    cmd+=(-b "duration:${TRACE_RING_DURATION}")
  fi
  if [[ "${TRACE_RING_FILES}" != "0" ]]; then
    cmd+=(-b "files:${TRACE_RING_FILES}")
  fi
  if [[ "${TRACE_RING_FILESIZE_MB}" != "0" ]]; then
    cmd+=(-b "filesize:${TRACE_RING_FILESIZE_MB}")
  fi
  if [[ "${TRACE_SNAPLEN}" != "0" ]]; then
    cmd+=(-s "${TRACE_SNAPLEN}")
  fi
  if [[ -n "${TRACE_EXTRA_ARGS:-}" ]]; then
    # shellcheck disable=SC2206
    local -a extra_args=(${TRACE_EXTRA_ARGS})
    cmd+=("${extra_args[@]}")
  fi

  log "Starting tshark capture:"
  log "  interface: ${TRACE_INTERFACE}"
  log "  filter: ${TRACE_FILTER}"
  log "  output: ${TRACE_PATH}"
  log "  ring duration (s): ${TRACE_RING_DURATION}"
  log "  ring files: ${TRACE_RING_FILES}"
  log "  ring filesize (MB): ${TRACE_RING_FILESIZE_MB}"
  log "  snaplen: ${TRACE_SNAPLEN}"

  echo $$ > "${PID_FILE}"

  local tshark_pid=0
  cleanup() {
    rm -f "${PID_FILE}"
  }
  forward_signal() {
    if [[ "${tshark_pid}" -ne 0 ]]; then
      kill -TERM "${tshark_pid}" || true
    fi
  }
  trap cleanup EXIT
  trap forward_signal INT TERM

  "${cmd[@]}" >> "${LOG_FILE}" 2>&1 &
  tshark_pid=$!
  wait "${tshark_pid}"
}

main() {
  ensure_root
  source_chain_vars
  ensure_tshark
  detect_interface
  ensure_dirs
  handle_existing_capture
  build_trace_path
  start_capture
}

run_script() {
  set -euo pipefail
  main "$@"
}

if [[ "${SCRIPT_SOURCED}" -eq 1 ]]; then
  (
    run_script "$@"
  )
  exit_code=$?
  return $exit_code 2>/dev/null || exit $exit_code
else
  run_script "$@"
fi
