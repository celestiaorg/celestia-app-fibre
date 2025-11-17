package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	"github.com/spf13/cobra"
)

func localCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "Manage local multi-validator networks",
		Long:  "Commands for initializing, configuring, and running local multi-validator networks on a single machine.",
	}

	cmd.AddCommand(
		localInitCmd(),
		localAddCmd(),
		localGenesisCmd(),
		localStartCmd(),
		localStopCmd(),
		localLogsCmd(),
	)

	return cmd
}

func localInitCmd() *cobra.Command {
	var (
		rootDir    string
		chainID    string
		binaryPath string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a local network configuration",
		Long:  "Creates a local network configuration with default port allocation strategy.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate that binary path is provided
			if binaryPath == "" {
				return fmt.Errorf("--binary-path is required")
			}

			// Convert to absolute path if relative
			absBinaryPath, err := filepath.Abs(binaryPath)
			if err != nil {
				return fmt.Errorf("failed to get absolute path for binary: %w", err)
			}

			// Verify binary exists
			if _, err := os.Stat(absBinaryPath); err != nil {
				return fmt.Errorf("binary not found at %s: %w", absBinaryPath, err)
			}

			// Create the root directory if it doesn't exist
			if err := os.MkdirAll(rootDir, 0o755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}

			// Create the local config
			config := DefaultLocalConfig(chainID, absBinaryPath)

			// Save the config
			if err := config.Save(rootDir); err != nil {
				return fmt.Errorf("failed to save local config: %w", err)
			}

			fmt.Printf("Initialized local network configuration:\n")
			fmt.Printf("  Chain ID: %s\n", config.ChainID)
			fmt.Printf("  Binary: %s\n", config.BinaryPath)
			fmt.Printf("  Config file: %s\n", filepath.Join(rootDir, "local_config.json"))
			fmt.Printf("\nNext steps:\n")
			fmt.Printf("  1. Add validators with: talis local add --moniker <name>\n")
			fmt.Printf("  2. Generate genesis with: talis local genesis\n")
			fmt.Printf("  3. Start the network with: talis local start\n")

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory for local network")
	cmd.Flags().StringVarP(&chainID, "chain-id", "c", "local-testnet", "chain ID for the local network")
	cmd.Flags().StringVarP(&binaryPath, "binary-path", "b", "", "path to celestia-appd binary (required)")
	cmd.MarkFlagRequired("binary-path")

	return cmd
}

func localAddCmd() *cobra.Command {
	var (
		rootDir string
		moniker string
		homeDir string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a validator to the local network",
		Long:  "Adds a validator to the local network configuration. The genesis command will create all necessary files.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load the local config
			config, err := LoadLocalConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load local config: %w (run 'talis local init' first)", err)
			}

			// Check if validator already exists
			if config.GetValidator(moniker) != nil {
				return fmt.Errorf("validator %s already exists", moniker)
			}

			// If homeDir not specified, use default
			if homeDir == "" {
				homeDir = filepath.Join(rootDir, "nodes", moniker)
			}

			// Add validator to config (this allocates ports)
			validator := config.AddValidator(moniker, homeDir)

			// Save the updated config
			if err := config.Save(rootDir); err != nil {
				return fmt.Errorf("failed to save local config: %w", err)
			}

			fmt.Printf("✅ Added validator %s to configuration\n", moniker)
			fmt.Printf("  Home: %s\n", homeDir)
			fmt.Printf("  Ports:\n")
			fmt.Printf("    P2P:  %d\n", validator.Ports.P2P)
			fmt.Printf("    RPC:  %d\n", validator.Ports.RPC)
			fmt.Printf("    gRPC: %d\n", validator.Ports.GRPC)
			fmt.Printf("    DRPC: %d\n", validator.Ports.DRPC)
			fmt.Printf("\nRun 'talis local genesis' to generate the network genesis and validator files.\n")

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory for local network")
	cmd.Flags().StringVarP(&moniker, "moniker", "m", "", "moniker for the validator (required)")
	cmd.Flags().StringVar(&homeDir, "home", "", "home directory for the validator (default: <root>/nodes/<moniker>)")
	_ = cmd.MarkFlagRequired("moniker")

	return cmd
}

func localGenesisCmd() *cobra.Command {
	var (
		rootDir     string
		stakeAmount int64
		squareSize  int
	)

	cmd := &cobra.Command{
		Use:   "genesis",
		Short: "Generate genesis for the local network",
		Long:  "Creates genesis using programmatic approach (same as remote talis networks).",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load the local config
			config, err := LoadLocalConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load local config: %w", err)
			}

			if len(config.Validators) == 0 {
				return fmt.Errorf("no validators found (run 'talis local add' first)")
			}

			fmt.Printf("Generating genesis for %d validators using programmatic approach...\n\n", len(config.Validators))

			// Create a Network using the same approach as normal talis
			network, err := NewNetwork(config.ChainID, squareSize)
			if err != nil {
				return fmt.Errorf("failed to create network: %w", err)
			}

			// Define payload directory
			payloadDir := filepath.Join(rootDir, "nodes")

			// Create the payload directory if it doesn't exist
			if err := os.MkdirAll(payloadDir, 0o755); err != nil {
				return fmt.Errorf("failed to create payload directory: %w", err)
			}

			// Add validators to the genesis
			fmt.Println("Adding validators to genesis...")
			for i := range config.Validators {
				validator := &config.Validators[i]
				fmt.Printf("  Adding %s...\n", validator.Moniker)

				// Use localhost with the validator's P2P port
				ip := "127.0.0.1"

				err = network.AddValidator(
					validator.Moniker,
					ip,
					payloadDir, // This will create keyring at nodes/val0/keyring-test
					"local",    // region
					stakeAmount,
				)
				if err != nil {
					return fmt.Errorf("failed to add validator %s: %w", validator.Moniker, err)
				}
			}

			// Initialize nodes - this creates genesis.json and all validator files
			fmt.Println("\nInitializing validator nodes...")
			if err := network.InitNodes(payloadDir); err != nil {
				return fmt.Errorf("failed to initialize nodes: %w", err)
			}

			// Move node keys and validator keys to correct directories
			fmt.Println("\nMoving keys to correct directories...")
			for i := range config.Validators {
				validator := &config.Validators[i]
				configDir := filepath.Join(validator.HomeDir, "config")
				dataDir := filepath.Join(validator.HomeDir, "data")

				if err := os.MkdirAll(configDir, 0o755); err != nil {
					return fmt.Errorf("failed to create config directory for %s: %w", validator.Moniker, err)
				}
				if err := os.MkdirAll(dataDir, 0o755); err != nil {
					return fmt.Errorf("failed to create data directory for %s: %w", validator.Moniker, err)
				}

				// Move node_key.json to config/
				srcNodeKey := filepath.Join(validator.HomeDir, "node_key.json")
				dstNodeKey := filepath.Join(configDir, "node_key.json")
				if err := os.Rename(srcNodeKey, dstNodeKey); err != nil {
					return fmt.Errorf("failed to move node_key.json for %s: %w", validator.Moniker, err)
				}

				// Move priv_validator_key.json to config/
				srcPrivKey := filepath.Join(validator.HomeDir, "priv_validator_key.json")
				dstPrivKey := filepath.Join(configDir, "priv_validator_key.json")
				if err := os.Rename(srcPrivKey, dstPrivKey); err != nil {
					return fmt.Errorf("failed to move priv_validator_key.json for %s: %w", validator.Moniker, err)
				}

				// Move priv_validator_state.json to data/
				srcPrivState := filepath.Join(validator.HomeDir, "priv_validator_state.json")
				dstPrivState := filepath.Join(dataDir, "priv_validator_state.json")
				if err := os.Rename(srcPrivState, dstPrivState); err != nil {
					return fmt.Errorf("failed to move priv_validator_state.json for %s: %w", validator.Moniker, err)
				}
			}

			// Copy the genesis file to each validator's config directory
			fmt.Println("\nCopying genesis to validator config directories...")
			sharedGenesisPath := filepath.Join(payloadDir, "genesis.json")
			genesisData, err := os.ReadFile(sharedGenesisPath)
			if err != nil {
				return fmt.Errorf("failed to read genesis file: %w", err)
			}

			for i := range config.Validators {
				validator := &config.Validators[i]
				validatorGenesisPath := filepath.Join(validator.HomeDir, "config", "genesis.json")
				if err := os.WriteFile(validatorGenesisPath, genesisData, 0o644); err != nil {
					return fmt.Errorf("failed to write genesis for %s: %w", validator.Moniker, err)
				}
			}

			// Now update the config files with the correct ports
			fmt.Println("\nConfiguring ports for each validator...")
			localNetwork := NewLocalNetwork(config, "", rootDir)
			for i := range config.Validators {
				validator := &config.Validators[i]
				fmt.Printf("  Configuring ports for %s...\n", validator.Moniker)
				if err := localNetwork.ConfigureValidatorPorts(validator); err != nil {
					return fmt.Errorf("failed to configure ports for %s: %w", validator.Moniker, err)
				}
			}

			// Configure persistent peers
			fmt.Println("\nConfiguring persistent peers...")
			for i := range config.Validators {
				validator := &config.Validators[i]

				// Get node IDs for all other validators
				var peers []string
				for j := range config.Validators {
					if i == j {
						continue // Skip self
					}

					other := &config.Validators[j]
					nodeIDOutput, err := localNetwork.GetNodeID(other)
					if err != nil {
						return err
					}
					nodeID := strings.TrimSpace(nodeIDOutput)

					// Format: <node_id>@localhost:<p2p_port>
					peer := fmt.Sprintf("%s@localhost:%d", nodeID, other.Ports.P2P)
					peers = append(peers, peer)
				}

				fmt.Printf("  Configuring %s with %d peers...\n", validator.Moniker, len(peers))
				if err := localNetwork.ConfigurePersistentPeers(validator, peers); err != nil {
					return err
				}
			}

			// Generate validator_hosts.json for fibre-load
			fmt.Println("\nGenerating validator_hosts.json for fibre-load...")
			validatorHostsPath := filepath.Join(rootDir, "validator_hosts.json")
			if err := saveLocalValidatorHostMapping(config, validatorHostsPath); err != nil {
				return fmt.Errorf("failed to save validator host mapping: %w", err)
			}
			fmt.Printf("  Saved to: %s\n", validatorHostsPath)

			fmt.Printf("\n✅ Genesis configuration completed successfully!\n")
			fmt.Printf("\nNetwork details:\n")
			fmt.Printf("  Chain ID: %s\n", config.ChainID)
			fmt.Printf("  Validators: %d\n", len(config.Validators))
			fmt.Printf("  Funded accounts created: 'txsim' key in each validator's keyring (balance: 9999999999999999 utia)\n")
			fmt.Printf("\nNext steps:\n")
			fmt.Printf("  1. Start the network: talis local start\n")
			fmt.Printf("\n  2. Run fibre-load:\n")
			absPayloadDir, _ := filepath.Abs(payloadDir)
			fmt.Printf("     fibre-load --grpc-endpoint localhost:%d --validator-hosts %s \\\n", config.Validators[0].Ports.CometGRPC, validatorHostsPath)
			fmt.Printf("       --chain-id %s --keyring-dir %s/%s\n", config.ChainID, absPayloadDir, config.Validators[0].Moniker)

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory for local network")
	cmd.Flags().Int64VarP(&stakeAmount, "stake", "s", 5000000000, "stake amount for each validator in utia")
	cmd.Flags().IntVar(&squareSize, "square-size", appconsts.SquareSizeUpperBound, "square size for the network")

	return cmd
}

func localStartCmd() *cobra.Command {
	var (
		rootDir string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local network",
		Long:  "Starts all validators in separate tmux sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load the local config
			config, err := LoadLocalConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load local config: %w", err)
			}

			if len(config.Validators) == 0 {
				return fmt.Errorf("no validators found")
			}

			// Check if tmux is installed
			if err := checkTmuxInstalled(); err != nil {
				return err
			}

			// Verify binary from config exists
			if _, err := os.Stat(config.BinaryPath); err != nil {
				return fmt.Errorf("binary not found at %s (from config): %w", config.BinaryPath, err)
			}

			fmt.Printf("Starting local network with %d validators...\n", len(config.Validators))
			fmt.Printf("Using binary: %s\n\n", config.BinaryPath)

			// Start each validator in its own tmux session
			for _, validator := range config.Validators {
				sessionName := fmt.Sprintf("celestia-%s", validator.Moniker)

				// Check if session already exists
				checkCmd := exec.Command("tmux", "has-session", "-t", sessionName)
				if checkCmd.Run() == nil {
					fmt.Printf("⚠️  Tmux session '%s' already exists, skipping...\n", sessionName)
					continue
				}

				// Get absolute path to home directory
				// Join with rootDir first in case validator.HomeDir is relative
				homeDir := validator.HomeDir
				if !filepath.IsAbs(homeDir) {
					homeDir = filepath.Join(rootDir, homeDir)
				}
				absHomeDir, err := filepath.Abs(homeDir)
				if err != nil {
					return fmt.Errorf("failed to get absolute path for %s: %w", validator.Moniker, err)
				}

				// Create new tmux session and start validator
				startCmd := fmt.Sprintf("%s start --home %s", config.BinaryPath, absHomeDir)
				tmuxCmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName, startCmd)

				if err := tmuxCmd.Run(); err != nil {
					return fmt.Errorf("failed to start validator %s in tmux: %w", validator.Moniker, err)
				}

				fmt.Printf("✅ Started %s in tmux session '%s'\n", validator.Moniker, sessionName)
				fmt.Printf("   P2P: %d | RPC: %d | gRPC: %d | DRPC: %d\n",
					validator.Ports.P2P,
					validator.Ports.RPC,
					validator.Ports.GRPC,
					validator.Ports.DRPC,
				)
			}

			fmt.Printf("\n🎉 Local network started successfully!\n")
			fmt.Printf("\nUseful commands:\n")
			fmt.Printf("  List all sessions:    tmux ls\n")
			fmt.Printf("  Attach to validator:  tmux attach -t celestia-<moniker>\n")
			fmt.Printf("  Detach from session:  Ctrl+B, then D\n")
			fmt.Printf("  Stop all validators:  talis local stop -d %s\n", rootDir)
			fmt.Printf("\nRPC endpoints:\n")
			for _, validator := range config.Validators {
				fmt.Printf("  %s: http://localhost:%d\n", validator.Moniker, validator.Ports.RPC)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory for local network")

	return cmd
}

func localStopCmd() *cobra.Command {
	var (
		rootDir string
	)

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the local network",
		Long:  "Stops all validator tmux sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load the local config
			config, err := LoadLocalConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load local config: %w", err)
			}

			if len(config.Validators) == 0 {
				return fmt.Errorf("no validators found")
			}

			fmt.Printf("Stopping local network...\n\n")

			// Kill each validator's tmux session
			stoppedCount := 0
			for _, validator := range config.Validators {
				sessionName := fmt.Sprintf("celestia-%s", validator.Moniker)

				// Check if session exists
				checkCmd := exec.Command("tmux", "has-session", "-t", sessionName)
				if checkCmd.Run() != nil {
					fmt.Printf("⚠️  Session '%s' not found, skipping...\n", sessionName)
					continue
				}

				// Kill the session
				killCmd := exec.Command("tmux", "kill-session", "-t", sessionName)
				if err := killCmd.Run(); err != nil {
					fmt.Printf("❌ Failed to stop %s: %v\n", validator.Moniker, err)
					continue
				}

				fmt.Printf("✅ Stopped %s (session: %s)\n", validator.Moniker, sessionName)
				stoppedCount++
			}

			if stoppedCount > 0 {
				fmt.Printf("\n🛑 Stopped %d validator(s)\n", stoppedCount)
			} else {
				fmt.Printf("\nNo validators were running\n")
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory for local network")

	return cmd
}

func localLogsCmd() *cobra.Command {
	var (
		rootDir string
	)

	cmd := &cobra.Command{
		Use:   "logs <moniker>",
		Short: "Attach to a validator's tmux session",
		Long:  "Attaches to the tmux session of the specified validator to view logs.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			moniker := args[0]

			// Load the local config
			config, err := LoadLocalConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load local config: %w", err)
			}

			// Check if validator exists
			validator := config.GetValidator(moniker)
			if validator == nil {
				return fmt.Errorf("validator '%s' not found", moniker)
			}

			sessionName := fmt.Sprintf("celestia-%s", moniker)

			// Check if session exists
			checkCmd := exec.Command("tmux", "has-session", "-t", sessionName)
			if checkCmd.Run() != nil {
				return fmt.Errorf("validator '%s' is not running (session '%s' not found)", moniker, sessionName)
			}

			fmt.Printf("Attaching to %s...\n", moniker)
			fmt.Printf("Press Ctrl+B, then D to detach from the session\n\n")

			// Attach to the session
			// We need to replace the current process with tmux attach
			attachCmd := exec.Command("tmux", "attach", "-t", sessionName)
			attachCmd.Stdin = os.Stdin
			attachCmd.Stdout = os.Stdout
			attachCmd.Stderr = os.Stderr

			return attachCmd.Run()
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory for local network")

	return cmd
}

// checkTmuxInstalled checks if tmux is installed
func checkTmuxInstalled() error {
	cmd := exec.Command("tmux", "-V")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux is not installed. Please install tmux first:\n  macOS: brew install tmux\n  Ubuntu/Debian: sudo apt-get install tmux\n  CentOS/RHEL: sudo yum install tmux")
	}
	return nil
}

// saveLocalValidatorHostMapping creates validator_hosts.json for fibre-load
func saveLocalValidatorHostMapping(config *LocalConfig, filename string) error {
	hostMapping := make(map[string]string)

	for _, val := range config.Validators {
		// Read the validator's priv_validator_key.json to get consensus address
		privKeyPath := filepath.Join(val.HomeDir, "config", "priv_validator_key.json")
		privKeyData, err := os.ReadFile(privKeyPath)
		if err != nil {
			return fmt.Errorf("failed to read priv_validator_key.json for %s: %w", val.Moniker, err)
		}

		var privKey struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(privKeyData, &privKey); err != nil {
			return fmt.Errorf("failed to parse priv_validator_key.json for %s: %w", val.Moniker, err)
		}

		// Map consensus address to localhost:DRPC_PORT
		consensusAddr := strings.ToUpper(privKey.Address)
		host := fmt.Sprintf("localhost:%d", val.Ports.DRPC)
		hostMapping[consensusAddr] = host
	}

	// Write to file
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(hostMapping); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	return nil
}
