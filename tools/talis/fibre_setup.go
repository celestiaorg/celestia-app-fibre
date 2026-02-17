package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const SetupFibreSessionName = "setup-fibre"

func setupFibreCmd() *cobra.Command {
	var (
		rootDir      string
		SSHKeyPath   string
		escrowAmount string
		fibrePort    int
		fees         string
	)

	cmd := &cobra.Command{
		Use:   "setup-fibre",
		Short: "Register fibre host addresses and fund escrow accounts on remote validators",
		Long:  "SSHes into each validator and runs two transactions: register the fibre host address and fund the escrow account.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if len(cfg.Validators) == 0 {
				return fmt.Errorf("no validators found in config")
			}

			resolvedSSHKeyPath := resolveValue(SSHKeyPath, EnvVarSSHKeyPath, strings.ReplaceAll(cfg.SSHPubKeyPath, ".pub", ""))

			for _, val := range cfg.Validators {
				script := fmt.Sprintf(
					"celestia-appd tx valaddr set-host %s:%d "+
						"--from validator --keyring-backend=test --home .celestia-app "+
						"--chain-id %s --fees %s --yes && "+
						"sleep 5 && "+
						"celestia-appd tx fibre deposit-to-escrow %s "+
						"--from validator --keyring-backend=test --home .celestia-app "+
						"--chain-id %s --fees %s --yes",
					val.PublicIP, fibrePort,
					cfg.ChainID, fees,
					escrowAmount,
					cfg.ChainID, fees,
				)

				fmt.Printf("Running setup-fibre on %s (%s)\n", val.Name, val.PublicIP)
				if err := runScriptInTMux([]Instance{val}, resolvedSSHKeyPath, script, SetupFibreSessionName, time.Minute*5); err != nil {
					return fmt.Errorf("failed to run setup-fibre on %s: %w", val.Name, err)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory in which to initialize")
	cmd.Flags().StringVarP(&SSHKeyPath, "ssh-key-path", "k", "", "path to the user's SSH key")
	cmd.Flags().StringVar(&escrowAmount, "escrow-amount", "4999999999999999utia", "amount to deposit into escrow")
	cmd.Flags().IntVar(&fibrePort, "fibre-port", 9091, "fibre gRPC port on validators")
	cmd.Flags().StringVar(&fees, "fees", "5000utia", "transaction fees")

	return cmd
}
