package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LocalConfig stores configuration for a local multi-validator network
type LocalConfig struct {
	// ChainID is the chain ID for the local network
	ChainID string `json:"chain_id"`

	// BinaryPath is the path to the celestia-appd binary to use
	BinaryPath string `json:"binary_path"`

	// Validators is a list of validators in the local network
	Validators []LocalValidator `json:"validators"`

	// BasePortP2P is the starting port for P2P (default 26656)
	BasePortP2P int `json:"base_port_p2p"`

	// BasePortRPC is the starting port for RPC (default 26657)
	BasePortRPC int `json:"base_port_rpc"`

	// BasePortGRPC is the starting port for gRPC (default 9090)
	BasePortGRPC int `json:"base_port_grpc"`

	// BasePortDRPC is the starting port for DRPC (default 26658)
	BasePortDRPC int `json:"base_port_drpc"`

	// FibreTransport specifies which transport to use for Fibre service ("grpc" or "drpc")
	FibreTransport string `json:"fibre_transport"`

	// MultiplexTransport specifies which multiplexing transport to use for DRPC ("yamux" or "quic")
	// Only applies when FibreTransport is "drpc"
	MultiplexTransport string `json:"multiplex_transport"`
}

// LocalValidator represents a validator in the local network
type LocalValidator struct {
	// Moniker is the validator's name
	Moniker string `json:"moniker"`

	// HomeDir is the validator's home directory
	HomeDir string `json:"home_dir"`

	// ValidatorIndex is the index of this validator (0-based)
	ValidatorIndex int `json:"validator_index"`

	// Ports contains all port assignments for this validator
	Ports LocalValidatorPorts `json:"ports"`
}

// LocalValidatorPorts contains all port assignments for a validator
type LocalValidatorPorts struct {
	P2P  int `json:"p2p"`
	RPC  int `json:"rpc"`
	GRPC int `json:"grpc"`
	DRPC int `json:"drpc"`

	// CometBFT gRPC (internal)
	CometGRPC int `json:"comet_grpc"`
}

// DefaultLocalConfig returns a LocalConfig with default port values
func DefaultLocalConfig(chainID, binaryPath string) *LocalConfig {
	return &LocalConfig{
		ChainID:            chainID,
		BinaryPath:         binaryPath,
		Validators:         []LocalValidator{},
		BasePortP2P:        26656,
		BasePortRPC:        26657,
		BasePortGRPC:       9090,
		BasePortDRPC:       26658,
		FibreTransport:     "drpc",  // Default to DRPC
		MultiplexTransport: "yamux", // Default to yamux for backward compatibility
	}
}

// AllocatePorts assigns unique ports to a validator based on its index
// Port allocation strategy:
// - Validator 0: uses base ports (26656, 26657, 9090, 26658)
// - Validator 1: uses base + 100 (26756, 26757, 9190, 26758)
// - Validator 2: uses base + 200 (26856, 26857, 9290, 26858)
// - etc.
func (lc *LocalConfig) AllocatePorts(index int) LocalValidatorPorts {
	offset := index * 100
	return LocalValidatorPorts{
		P2P:       lc.BasePortP2P + offset,
		RPC:       lc.BasePortRPC + offset,
		GRPC:      lc.BasePortGRPC + offset,
		DRPC:      lc.BasePortDRPC + offset,
		CometGRPC: 9098 + offset, // Default CometBFT gRPC port
	}
}

// AddValidator adds a new validator to the local config
func (lc *LocalConfig) AddValidator(moniker, homeDir string) *LocalValidator {
	index := len(lc.Validators)
	validator := LocalValidator{
		Moniker:        moniker,
		HomeDir:        homeDir,
		ValidatorIndex: index,
		Ports:          lc.AllocatePorts(index),
	}
	lc.Validators = append(lc.Validators, validator)
	return &lc.Validators[index]
}

// GetValidator returns a validator by moniker, or nil if not found
func (lc *LocalConfig) GetValidator(moniker string) *LocalValidator {
	for i := range lc.Validators {
		if lc.Validators[i].Moniker == moniker {
			return &lc.Validators[i]
		}
	}
	return nil
}

// Save writes the LocalConfig to a JSON file
func (lc *LocalConfig) Save(rootDir string) error {
	configPath := filepath.Join(rootDir, "local_config.json")
	data, err := json.MarshalIndent(lc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal local config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write local config: %w", err)
	}

	return nil
}

// LoadLocalConfig reads the LocalConfig from a JSON file
func LoadLocalConfig(rootDir string) (*LocalConfig, error) {
	configPath := filepath.Join(rootDir, "local_config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local config: %w", err)
	}

	var config LocalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal local config: %w", err)
	}

	return &config, nil
}
