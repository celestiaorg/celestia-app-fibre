package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	chainID         = "test"
	keyName         = "validator"
	keyringBackend  = "test"
	fees            = "500utia"
	initialBalance  = "1000000000000000utia"
	validatorStake  = "5000000000utia"
	commissionRate  = "0.05"
	commissionMax   = "1.0"
	commissionChange = "1.0"
)

type Config struct {
	AppHome    string
	BinaryPath string
}

var (
	appHomeFlag string
	binaryPath  string
)

var rootCmd = &cobra.Command{
	Use:   "local-network",
	Short: "Start a single-node testnet",
	Long:  "Starts a single-node testnet using the provided celestia-appd binary.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return run()
	},
}

func init() {
	rootCmd.Flags().StringVarP(&binaryPath, "binary", "b", "", "path to celestia-appd binary (required)")
	rootCmd.Flags().StringVarP(&appHomeFlag, "home", "d", "", "home directory for the node (default: ~/.celestia-app)")
	rootCmd.MarkFlagRequired("binary")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Determine app home directory
	appHome := getAppHome(appHomeFlag)

	cfg := &Config{
		AppHome:    appHome,
		BinaryPath: binaryPath,
	}

	fmt.Printf("celestia-app home: %s\n", cfg.AppHome)
	fmt.Printf("celestia-app binary: %s\n", cfg.BinaryPath)
	fmt.Println()

	// Verify binary exists and is executable
	if err := verifyBinary(cfg.BinaryPath); err != nil {
		return fmt.Errorf("failed to verify binary: %w", err)
	}

	// Check version
	version, err := getVersion(cfg.BinaryPath)
	if err != nil {
		return fmt.Errorf("failed to get version: %w", err)
	}
	fmt.Printf("celestia-app version: %s\n\n", version)

	genesisFile := filepath.Join(cfg.AppHome, "config", "genesis.json")

	// Check if genesis already exists
	if _, err := os.Stat(genesisFile); err == nil {
		fmt.Printf("Do you want to delete existing %s and start a new local testnet? [y/n]: ", cfg.AppHome)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		if response == "y" || response == "Y" {
			if err := deleteCelestiaAppHome(cfg.AppHome); err != nil {
				return fmt.Errorf("failed to delete app home: %w", err)
			}
			if err := createGenesis(cfg); err != nil {
				return fmt.Errorf("failed to create genesis: %w", err)
			}
		}
	} else {
		if err := createGenesis(cfg); err != nil {
			return fmt.Errorf("failed to create genesis: %w", err)
		}
	}

	// Start the node
	if err := startCelestiaApp(cfg); err != nil {
		return fmt.Errorf("failed to start celestia-app: %w", err)
	}

	return nil
}

func getAppHome(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ".celestia-app"
	}
	return filepath.Join(home, ".celestia-app")
}

func verifyBinary(binaryPath string) error {
	// Check if file exists
	info, err := os.Stat(binaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("binary not found at %s", binaryPath)
		}
		return fmt.Errorf("failed to stat binary: %w", err)
	}

	// Check if it's a regular file
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", binaryPath)
	}

	// Check if it's executable
	if info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("%s is not executable", binaryPath)
	}

	return nil
}

func getVersion(binaryPath string) (string, error) {
	cmd := exec.Command(binaryPath, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func deleteCelestiaAppHome(appHome string) error {
	fmt.Printf("Deleting %s...\n", appHome)
	return os.RemoveAll(appHome)
}

func createGenesis(cfg *Config) error {
	fmt.Println("Initializing validator and node config files...")
	if err := runCommand(cfg.BinaryPath, "init", chainID,
		"--chain-id", chainID,
		"--home", cfg.AppHome); err != nil {
		return fmt.Errorf("init failed: %w", err)
	}

	fmt.Println("Adding a new key to the keyring...")
	if err := runCommand(cfg.BinaryPath, "keys", "add", keyName,
		"--keyring-backend", keyringBackend,
		"--home", cfg.AppHome); err != nil {
		return fmt.Errorf("keys add failed: %w", err)
	}

	// Get the address
	fmt.Println("Adding genesis account...")
	addressCmd := exec.Command(cfg.BinaryPath, "keys", "show", keyName, "-a",
		"--keyring-backend", keyringBackend,
		"--home", cfg.AppHome)
	addressOutput, err := addressCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get address: %w", err)
	}
	address := strings.TrimSpace(string(addressOutput))

	if err := runCommand(cfg.BinaryPath, "genesis", "add-genesis-account", address, initialBalance,
		"--home", cfg.AppHome); err != nil {
		return fmt.Errorf("add-genesis-account failed: %w", err)
	}

	fmt.Println("Creating a genesis tx...")
	if err := runCommand(cfg.BinaryPath, "genesis", "gentx", keyName, validatorStake,
		"--fees", fees,
		"--keyring-backend", keyringBackend,
		"--chain-id", chainID,
		"--home", cfg.AppHome,
		"--commission-rate", commissionRate,
		"--commission-max-rate", commissionMax,
		"--commission-max-change-rate", commissionChange); err != nil {
		return fmt.Errorf("gentx failed: %w", err)
	}

	fmt.Println("Collecting genesis txs...")
	if err := runCommand(cfg.BinaryPath, "genesis", "collect-gentxs",
		"--home", cfg.AppHome); err != nil {
		return fmt.Errorf("collect-gentxs failed: %w", err)
	}

	// Modify config files
	if err := modifyConfigs(cfg.AppHome); err != nil {
		return fmt.Errorf("failed to modify configs: %w", err)
	}

	fmt.Println("Tracing is set up with the ability to pull traced data from the node on the address http://127.0.0.1:26661")
	return nil
}

func modifyConfigs(appHome string) error {
	configPath := filepath.Join(appHome, "config", "config.toml")
	genesisPath := filepath.Join(appHome, "config", "genesis.json")

	// Read config.toml
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config.toml: %w", err)
	}
	config := string(configData)

	// Override the default RPC server listening address
	config = strings.Replace(config, `"tcp://127.0.0.1:26657"`, `"tcp://0.0.0.0:26657"`, -1)

	// Enable transaction indexing
	config = strings.Replace(config, `"null"`, `"kv"`, -1)

	// Persist ABCI responses
	config = strings.Replace(config, "discard_abci_responses = true", "discard_abci_responses = false", -1)

	// Override the log level to reduce noisy logs
	config = strings.Replace(config, `log_level = "info"`, `log_level = "*:error,p2p:info,state:info"`, -1)

	// Set trace_type
	config = replaceConfigValue(config, "trace_type", `"local"`)

	// Set trace_pull_address
	config = replaceConfigValue(config, "trace_pull_address", `":26661"`)

	// Set trace_push_batch_size
	config = replaceConfigValue(config, "trace_push_batch_size", "1000")

	// Write back config.toml
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write config.toml: %w", err)
	}

	// Read genesis.json
	genesisData, err := os.ReadFile(genesisPath)
	if err != nil {
		return fmt.Errorf("failed to read genesis.json: %w", err)
	}
	genesis := string(genesisData)

	// Override the VotingPeriod from 1 week to 30 seconds
	genesis = strings.Replace(genesis, `"604800s"`, `"30s"`, -1)

	// Write back genesis.json
	if err := os.WriteFile(genesisPath, []byte(genesis), 0644); err != nil {
		return fmt.Errorf("failed to write genesis.json: %w", err)
	}

	return nil
}

func replaceConfigValue(config, key, value string) string {
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+" =") || strings.HasPrefix(trimmed, key+"=") {
			lines[i] = key + " = " + value
		}
	}
	return strings.Join(lines, "\n")
}

func startCelestiaApp(cfg *Config) error {
	fmt.Println("Starting celestia-app...")
	cmd := exec.Command(cfg.BinaryPath, "start",
		"--home", cfg.AppHome,
		"--api.enable",
		"--grpc.enable",
		"--grpc-web.enable",
		"--delayed-precommit-timeout", "1s")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
