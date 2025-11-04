//go:build !multiplexer

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// enhanceStartCommandHelp adds Fibre server documentation to the start command's help text
func enhanceStartCommandHelp(rootCmd *cobra.Command) error {
	// Find start command
	startCmd, _, err := rootCmd.Find([]string{"start"})
	if err != nil {
		return fmt.Errorf("failed to find start command: %w", err)
	}

	// Get existing Long description or use Short as base
	existingLong := startCmd.Long
	if existingLong == "" {
		existingLong = startCmd.Short
	}

	// Add Fibre server documentation section
	fibreDocs := `

Fibre Server Configuration:
  The Fibre server is automatically started for validator nodes to handle data availability
  requests. The server only starts if:
  - The node is configured as a validator (has priv_validator_key.json)
  - The gRPC server is enabled
  - The --fibre.enable flag is true (default)

  For validator nodes, if Fibre server initialization fails, the node will fail to start
  to ensure validators are properly configured. Non-validator nodes can start successfully
  even if Fibre server initialization fails (errors are logged but don't prevent startup).

  Examples:
    # Start with default Fibre configuration (BadgerDB store)
    celestia-appd start

    # Start with in-memory store (for testing, data is lost on restart)
    celestia-appd start --fibre.store-type memory

    # Start with custom store path
    celestia-appd start --fibre.store-path /custom/path/to/fibre-store

    # Start with custom block time
    celestia-appd start --fibre.block-time 3s

    # Disable Fibre server (even for validator nodes)
    celestia-appd start --fibre.enable false

    # Combine multiple flags
    celestia-appd start --fibre.store-type badger --fibre.store-path /var/lib/celestia-app/fibre-store --fibre.block-time 6s`

	// Append Fibre docs to existing Long description
	startCmd.Long = strings.TrimSpace(existingLong) + fibreDocs

	// Also enhance the example if it exists
	if startCmd.Example != "" {
		startCmd.Example += "\n\n  # Example: Start with Fibre server enabled (default)\n  celestia-appd start"
	}

	return nil
}
