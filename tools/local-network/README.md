# Local Network

A Go-based tool for running a single-node testnet.

## Overview

This tool recreates the functionality of `scripts/single-node.sh` but as a Go program that uses the top-level repository's `go.mod`. It sets up a local single-node testnet using a provided `celestia-appd` binary.

## Building

From the repository root:

```bash
# Build the tool
go build -o build/local-network ./tools/local-network
```

## Usage

### Run with default settings

```bash
# Build celestia-appd first
make build

# Run the tool (long form)
go run ./tools/local-network --binary ./build/celestia-appd

# Or using shorthand flags
go run ./tools/local-network -b ./build/celestia-appd

# Or using the built binary
./build/local-network -b ./build/celestia-appd
```

This will:
- Use the specified `celestia-appd` binary
- Create a testnet in `~/.celestia-app` (default home directory)
- Start the node with all services enabled

### Run with custom home directory

```bash
# Long form
go run ./tools/local-network --binary ./build/celestia-appd --home /custom/path/home

# Shorthand
go run ./tools/local-network -b ./build/celestia-appd -d /custom/path/home
```

### Get help

```bash
go run ./tools/local-network --help
# or
go run ./tools/local-network -h
```

## Features

- **Binary path required**: Accepts path to pre-built `celestia-appd` binary via `--binary` / `-b` flag
- **Interactive cleanup**: Prompts before deleting existing data
- **Full configuration**: Sets up genesis, validators, and all necessary config files
- **Binary validation**: Verifies the binary exists and is executable
- **Cobra CLI**: Uses spf13/cobra for robust command-line parsing

## Flags

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--binary` | `-b` | (required) | Path to celestia-appd binary |
| `--home` | `-d` | `~/.celestia-app` | Home directory for the node |
| `--help` | `-h` | - | Display help information |

## Configuration

The tool sets up a testnet with:

- **Chain ID**: `test`
- **Validator key**: `validator` (stored in test keyring backend)
- **Initial balance**: 1,000,000,000,000,000 utia
- **Validator stake**: 5,000,000,000 utia
- **Voting period**: 30 seconds (reduced from default 1 week)
- **Log level**: `*:error,p2p:info,state:info` (reduced noise)

### Enabled Services

- **RPC**: `tcp://0.0.0.0:26657`
- **API**: `tcp://localhost:1317`
- **gRPC**: `localhost:9090`
- **gRPC-Web**: enabled
- **Tracing**: Pull endpoint at `http://127.0.0.1:26661`

### Modified Defaults

The tool modifies the default configuration to:
- Enable transaction indexing (kv indexer)
- Persist ABCI responses
- Enable local tracing with pull address `:26661`
- Reduce log verbosity
- Set delayed precommit timeout to 1s

## Comparison with scripts/single-node.sh

| Feature | Shell Script | Go Tool |
|---------|--------------|---------|
| Language | Bash | Go |
| Binary source | Pre-installed in PATH | Provided via `-binary` flag |
| Dependencies | celestia-appd in PATH | None (uses top-level go.mod) |
| Portability | Unix-like systems | Cross-platform |
| Argument parsing | Positional only | Flags |
| Error handling | Basic | Structured |
| Binary validation | Simple command check | Existence + executable check |

## Implementation Details

- Uses the top-level `go.mod` (no separate module needed)
- Accepts binary path via `-binary` flag (required)
- Validates binary exists and is executable before starting
- All file modifications use string replacement (similar to sed in shell script)
- Runs celestia-appd commands using `os/exec` package
