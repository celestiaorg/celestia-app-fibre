# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

celestia-app is the blockchain application for the Celestia consensus network. It is built on forks of Cosmos SDK (`celestiaorg/cosmos-sdk`) and CometBFT (`celestiaorg/celestia-core`). The current major version is v6 (module path: `github.com/celestiaorg/celestia-app/v6`).

## Build and Development Commands

```sh
# Build binary (multiplexer mode, downloads embedded v3/v4/v5 binaries)
make build

# Build without downloading embedded binaries
DOWNLOAD=false make build

# Build standalone (no multiplexer)
make build-standalone

# Install binary
make install

# Run all tests (30m timeout)
make test

# Run tests in short mode (1m timeout)
make test-short

# Run a single test
go test -run TestName -timeout 30m ./path/to/package/...

# Run a single test in a specific package
go test -run TestName -timeout 30m ./x/blob/...

# Lint (requires golangci-lint, markdownlint, hadolint, yamllint)
make lint

# Auto-fix lint issues
make lint-fix

# Run golangci-lint only
golangci-lint run

# Regenerate Protobuf files (requires Docker)
make proto-gen

# Update go.mod files
make mod

# Docker e2e tests (requires test name)
make test-docker-e2e test=TestE2ESimple
```

## Multi-Module Workspace

This repo contains multiple Go modules. Copy `go.work.example` to `go.work` and run `go work sync` for local development. The secondary module is in `test/docker-e2e/` with its own `go.mod`.

## Architecture

### Application Layer (`app/`)

- `app.go` - Main `App` struct extending `baseapp.BaseApp`. Contains all module keepers, store keys, and wiring.
- `modules.go` - Module manager setup and module ordering (begin/end blockers, init genesis order).
- `prepare_proposal.go` / `process_proposal.go` - ABCI++ block proposal logic (square construction and validation).
- `ante/` - Ante handler chain (fee deduction, signature verification, tx size gas, blob-specific validation).
- `upgrades.go` - On-chain upgrade handlers for version transitions.

### Custom Cosmos SDK Modules (`x/`)

- `x/blob` - Pay-for-blob transaction type. Core Celestia module enabling data availability.
- `x/signal` - Coordinates network upgrades by tracking validator version signals. Ensures all validators upgrade before the chain transitions to a new app version.
- `x/mint` - Custom inflation/minting module.
- `x/minfee` - Global minimum gas price enforcement.

### Multiplexer (`multiplexer/`)

An upgrade system that enables syncing from genesis and seamless chain upgrades without switching binaries. It embeds older version binaries (v3, v4, v5) and manages the lifecycle of standalone processes via gRPC ABCI connections. The multiplexer sits between CometBFT and the application, watching for `AppVersion` changes to trigger automatic upgrades.

### Public Packages (`pkg/`)

- `pkg/appconsts` - Network constants (share sizes, square sizes, versioned parameters).
- `pkg/user` - Transaction signing and broadcasting client (`TxClient`).
- `pkg/proof` - Share and namespace Merkle proof generation/verification.
- `pkg/inclusion` - Blob inclusion proof logic.
- `pkg/wrapper` - Erasure-coded data square wrapper using NMT (Namespace Merkle Tree).
- `pkg/da` - Data availability header utilities.

### Testing (`test/`)

- `test/txsim/` - Transaction simulator sequences (blob, send, stake, upgrade) used in e2e tests.
- `test/docker-e2e/` - Docker-based end-to-end tests (separate Go module). Includes upgrade, IBC, state sync, and block sync tests.
- `test/util/` - Test utilities including testnode setup for integration tests.
- `app/test/` - Integration tests for the application (uses testnode).

### Tools (`tools/`)

Various operational tools: `chainbuilder`, `blocktime`, `throughput`, `blockscan`, `talis` (deployment), etc.

## Conventions

- Follow [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) for PR titles: `fix:`, `feat:`, `refactor:`, `chore:`, etc. Use `!` for breaking changes (e.g., `feat!:`).
- Use `testify/assert` and `testify/require` for test assertions. Prefer table-driven tests.
- Go 1.24.6 is required.
- golangci-lint 2.1.2 is the expected linter version.

## Key Dependencies

| Dependency | Branch |
|---|---|
| `celestiaorg/cosmos-sdk` | `release/v0.51.x-celestia` |
| `celestiaorg/celestia-core` | `main` |
| `cosmos/ibc-go` | v8 |
