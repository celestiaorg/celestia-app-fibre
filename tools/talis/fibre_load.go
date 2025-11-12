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
		instances      int
		interval       time.Duration
		payloadSize    int
		namespace      string
		maxConcurrency int
		rootDir        string
		cfgPath        string
		SSHKeyPath     string
		pyroscopeURL   string
		pyroscopeTrace bool
		pyroProfiles   []string
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

			if pyroscopeTrace && pyroscopeURL == "" {
				return fmt.Errorf("--pyroscope-trace requires --pyroscope-url")
			}
			if len(pyroProfiles) > 0 && pyroscopeURL == "" {
				return fmt.Errorf("--pyroscope-profile requires --pyroscope-url")
			}

			fibreLoadScript := fmt.Sprintf(
				"./payload/build/fibre-load -e localhost:9091 -v ./payload/validator_hosts.json --reuse-blob -c %s -i %s -s %d -n %s -m %d -t /root/.celestia-app/data/traces %s",
				cfg.ChainID,
				interval,
				payloadSize,
				namespace,
				maxConcurrency,
				pyroscopeURL,
			)

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
	cmd.Flags().DurationVarP(&interval, "interval", "t", time.Second, "interval between transactions (e.g. 500ms, 2s)")
	cmd.Flags().IntVarP(&payloadSize, "payload-size", "p", 128*1024*1024, "size of payload data in bytes (default 128MB)")
	cmd.Flags().IntVarP(&maxConcurrency, "max-concurrency", "m", 1, "maximum number of concurrent transactions per node")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "fibre", "namespace for blob submission")
	cmd.Flags().StringVarP(&pyroscopeURL, "pyroscope-url", "y", "", "Pyroscope server URL to enable continuous profiling for fibre-load")
	cmd.Flags().BoolVarP(&pyroscopeTrace, "pyroscope-trace", "x", false, "Attach trace data to Pyroscope profiles (requires --pyroscope-url)")
	_ = cmd.MarkFlagRequired("instances")
	return cmd
}
