package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/p2p"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"

	"github.com/celestiaorg/celestia-app/v6/app"
)

// LocalNetwork wraps the local network configuration and provides
// helper methods for initializing and managing local validators
type LocalNetwork struct {
	Config     *LocalConfig
	BinaryPath string
	RootDir    string
}

// NewLocalNetwork creates a new LocalNetwork instance
func NewLocalNetwork(config *LocalConfig, binaryPath, rootDir string) *LocalNetwork {
	return &LocalNetwork{
		Config:     config,
		BinaryPath: binaryPath,
		RootDir:    rootDir,
	}
}

// ConfigureValidatorPorts is kept for configuring ports after programmatic genesis creation

// ConfigureValidatorPorts updates the config files with the allocated ports
func (ln *LocalNetwork) ConfigureValidatorPorts(validator *LocalValidator) error {
	// Ensure config directory exists
	configDir := filepath.Join(validator.HomeDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Update config.toml
	configPath := filepath.Join(configDir, "config.toml")
	cmtConfig := app.DefaultConsensusConfig()
	cmtConfig.SetRoot(validator.HomeDir)

	// Set P2P port
	cmtConfig.P2P.ListenAddress = fmt.Sprintf("tcp://0.0.0.0:%d", validator.Ports.P2P)

	// Set RPC port
	cmtConfig.RPC.ListenAddress = fmt.Sprintf("tcp://0.0.0.0:%d", validator.Ports.RPC)

	// Set CometBFT gRPC port
	cmtConfig.RPC.GRPCListenAddress = fmt.Sprintf("tcp://0.0.0.0:%d", validator.Ports.CometGRPC)

	// Write config.toml
	cmtcfg.WriteConfigFile(configPath, cmtConfig)

	// Update app.toml
	appConfigPath := filepath.Join(validator.HomeDir, "config", "app.toml")
	appConfig := app.DefaultAppConfig()

	// Set gRPC port
	appConfig.GRPC.Enable = true
	appConfig.GRPC.Address = fmt.Sprintf("0.0.0.0:%d", validator.Ports.GRPC)

	// Set DRPC port
	appConfig.DRPC.Enable = true
	appConfig.DRPC.Address = fmt.Sprintf("0.0.0.0:%d", validator.Ports.DRPC)

	// Set Fibre transport from config
	appConfig.DRPC.FibreTransport = ln.Config.FibreTransport

	// Set Multiplex transport from config
	appConfig.DRPC.MultiplexTransport = ln.Config.MultiplexTransport

	// Set the custom config template
	serverconfig.SetConfigTemplate(app.CustomConfigTemplate)

	// Write app.toml
	serverconfig.WriteConfigFile(appConfigPath, appConfig)

	fmt.Printf("Configured ports for %s: P2P=%d, RPC=%d, gRPC=%d, DRPC=%d\n",
		validator.Moniker,
		validator.Ports.P2P,
		validator.Ports.RPC,
		validator.Ports.GRPC,
		validator.Ports.DRPC,
	)

	return nil
}

// Removed CLI-based functions: CreateKey, AddGenesisAccount, CreateGenTx, GetValidatorAddress
// These are now handled by the programmatic genesis approach

// GetNodeID returns the node ID for a validator by reading the node_key.json file
func (ln *LocalNetwork) GetNodeID(validator *LocalValidator) (string, error) {
	nodeKeyPath := filepath.Join(validator.HomeDir, "config", "node_key.json")

	// Load the node key using CometBFT's function
	nodeKey, err := p2p.LoadNodeKey(nodeKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to load node_key.json for %s: %w", validator.Moniker, err)
	}

	return string(nodeKey.ID()), nil
}

// Removed CLI-based genesis functions: CollectGenTxs, CopyGenesisFile, FixGenesisFormat
// The programmatic approach creates genesis correctly from the start

// ConfigurePersistentPeers sets up persistent_peers in config.toml
func (ln *LocalNetwork) ConfigurePersistentPeers(validator *LocalValidator, peers []string) error {
	configPath := filepath.Join(validator.HomeDir, "config", "config.toml")

	// Load existing config
	cmtConfig := app.DefaultConsensusConfig()
	cmtConfig.SetRoot(validator.HomeDir)

	// Read the existing config to preserve other settings
	if data, err := os.ReadFile(configPath); err == nil {
		// Parse and update just the persistent_peers field
		content := string(data)

		// Simple string replacement for persistent_peers
		// This preserves all other config settings
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "persistent_peers = ") {
				lines[i] = fmt.Sprintf("persistent_peers = \"%s\"", strings.Join(peers, ","))
				break
			}
		}

		updated := strings.Join(lines, "\n")
		if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
			return fmt.Errorf("failed to write config for %s: %w", validator.Moniker, err)
		}
	} else {
		return fmt.Errorf("failed to read config for %s: %w", validator.Moniker, err)
	}

	return nil
}
