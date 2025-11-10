package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	FibreLoadSessionName = "load"
)

// startFibreLoadCmd creates a cobra command for starting fibre-load on remote instances.
func startFibreLoadCmd() *cobra.Command {
	var (
		instances   int
		interval    float64
		payloadSize int
		namespace   string
		rootDir     string
		cfgPath     string
		SSHKeyPath  string
	)

	cmd := &cobra.Command{
		Use:   "fibre-load",
		Short: "Starts the fibre-load command on remote validators",
		Long:  "Connects to remote validators and starts the fibre-load command in a detached tmux session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if len(cfg.Validators) == 0 {
				return fmt.Errorf("no validators found in config")
			}

			resolvedSSHKeyPath := resolveValue(SSHKeyPath, EnvVarSSHKeyPath, strings.ReplaceAll(cfg.SSHPubKeyPath, ".pub", ""))

			fibreLoadScript := fmt.Sprintf("./payload/build/fibre-load -e localhost:9091 -v ./payload/validator_hosts.json -c %s -i %f -s %d -n %s", cfg.ChainID, interval, payloadSize, namespace)

			// only spin up fibre-load on the number of instances that were specified.
			insts := []Instance{}
			for i, val := range cfg.Validators {
				if i >= instances || i >= len(cfg.Validators) {
					break
				}
				insts = append(insts, val)
			}

			fmt.Println(insts, "\n", fibreLoadScript)

			return runScriptInTMux(insts, resolvedSSHKeyPath, fibreLoadScript, FibreLoadSessionName, time.Minute*5)
		},
	}

	// Define flags for the command
	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory in which to initialize")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.json", "name of the config")
	cmd.Flags().StringVarP(&SSHKeyPath, "ssh-key-path", "k", "", "path to the user's SSH key (overrides environment variable and default)")
	cmd.Flags().IntVarP(&instances, "instances", "i", 1, "the number of instances of fibre-load, each ran on its own validator")
	cmd.Flags().Float64VarP(&interval, "interval", "t", 1.0, "interval between transactions in seconds")
	cmd.Flags().IntVarP(&payloadSize, "payload-size", "p", 1048576, "size of payload data in bytes (default 1MB)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "fibre", "namespace for blob submission")
	_ = cmd.MarkFlagRequired("instances")
	return cmd
}
