package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

const (
	FibreLoadSessionName = "load"
)

// startFibreLoadCmd creates a cobra command for starting fibre-load on remote instances.
func startFibreLoadCmd() *cobra.Command {
	var (
		instances           int
		instancesPerMachine int
		payloadSize         int
		namespace           string
		workers             int
		rootDir             string
		cfgPath             string
		SSHKeyPath          string
		pyroscopeURL        string
		pyroscopeTrace      bool
		pyroProfiles        []string
	)

	cmd := &cobra.Command{
		Use:   "fibre-load",
		Short: "Starts the fibre-load command on remote validators",
		Long:  "Connects to remote validators and starts the fibre-load command in a detached tmux session. Workers continuously send blobs without any delay between submissions.",
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

			// only spin up fibre-load on the number of instances that were specified.
			insts := []Instance{}
			for i, val := range cfg.Validators {
				if i >= instances || i >= len(cfg.Validators) {
					break
				}
				insts = append(insts, val)
			}

			// Use timestamp to make session names unique so we can run multiple times
			timestamp := time.Now().Unix()

			// If instancesPerMachine > 1, we need to run multiple fibre-load processes per machine
			if instancesPerMachine > 1 {
				// Run multiple instances per machine concurrently
				var wg sync.WaitGroup
				errCh := make(chan error, instancesPerMachine)

				for instID := 0; instID < instancesPerMachine; instID++ {
					wg.Add(1)
					go func(id int) {
						defer wg.Done()

						sessionName := fmt.Sprintf("%s-%d-%d", FibreLoadSessionName, id, timestamp)
						fibreLoadScript := fmt.Sprintf(
							"./payload/build/fibre-load -e 127.0.0.1:9091 -v ./payload/validator_hosts.json --reuse-blob -c %s -s %d -n %s -w %d -t /root/.celestia-app/data/traces --instance-id %d %s",
							cfg.ChainID,
							payloadSize,
							namespace,
							workers,
							id,
							pyroscopeURL,
						)

						fmt.Printf("Starting instance %d on %d machines\n", id, len(insts))
						fmt.Println("Script:", fibreLoadScript)

						if err := runScriptInTMux(insts, resolvedSSHKeyPath, fibreLoadScript, sessionName, time.Minute*5); err != nil {
							errCh <- fmt.Errorf("instance %d: %w", id, err)
						}
					}(instID)
				}

				wg.Wait()
				close(errCh)

				// Collect any errors
				var errs []error
				for err := range errCh {
					errs = append(errs, err)
				}
				if len(errs) > 0 {
					return fmt.Errorf("failed to start instances: %v", errs)
				}
				return nil
			}

			// Single instance per machine (original behavior)
			fibreLoadScript := fmt.Sprintf(
				"./payload/build/fibre-load -e 127.0.0.1:9091 -v ./payload/validator_hosts.json --reuse-blob -c %s -s %d -n %s -w %d -t /root/.celestia-app/data/traces %s",
				cfg.ChainID,
				payloadSize,
				namespace,
				workers,
				pyroscopeURL,
			)

			fmt.Println(insts, "\n", fibreLoadScript)

			sessionName := fmt.Sprintf("%s-%d", FibreLoadSessionName, timestamp)
			return runScriptInTMux(insts, resolvedSSHKeyPath, fibreLoadScript, sessionName, time.Minute*5)
		},
	}

	// Define flags for the command
	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory in which to initialize")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.json", "name of the config")
	cmd.Flags().StringVarP(&SSHKeyPath, "ssh-key-path", "k", "", "path to the user's SSH key (overrides environment variable and default)")
	cmd.Flags().IntVarP(&instances, "instances", "i", 1, "the number of instances of fibre-load, each ran on its own validator")
	cmd.Flags().IntVarP(&instancesPerMachine, "instances-per-machine", "m", 1, "the number of fibre-load instances to run per machine (default 1)")
	cmd.Flags().IntVarP(&payloadSize, "payload-size", "p", 128*1024*1024, "size of payload data in bytes (default 128MB)")
	cmd.Flags().IntVarP(&workers, "workers", "w", 1, "number of concurrent workers sending blobs continuously per instance")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "fibre", "namespace for blob submission")
	cmd.Flags().StringVarP(&pyroscopeURL, "pyroscope-url", "y", "", "Pyroscope server URL to enable continuous profiling for fibre-load")
	cmd.Flags().BoolVarP(&pyroscopeTrace, "pyroscope-trace", "x", false, "Attach trace data to Pyroscope profiles (requires --pyroscope-url)")
	_ = cmd.MarkFlagRequired("instances")
	return cmd
}

// stopFibreLoadCmd creates a cobra command for stopping all fibre-load sessions on remote instances.
func stopFibreLoadCmd() *cobra.Command {
	var (
		rootDir    string
		cfgPath    string
		SSHKeyPath string
		timeout    time.Duration
	)

	cmd := &cobra.Command{
		Use:     "stop-fibre-load",
		Short:   "Stops all fibre-load tmux sessions on remote validators",
		Long:    "Connects to remote validator nodes and kills all tmux sessions matching the 'load' pattern (load, load-0, load-1, etc.).",
		Aliases: []string{"stop-load"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if len(cfg.Validators) == 0 {
				return fmt.Errorf("no validators found in config")
			}

			resolvedSSHKeyPath := resolveValue(SSHKeyPath, EnvVarSSHKeyPath, strings.ReplaceAll(cfg.SSHPubKeyPath, ".pub", ""))

			// Kill all sessions matching the load pattern
			// This will kill 'load', 'load-{timestamp}', 'load-{id}-{timestamp}', etc.
			killScript := fmt.Sprintf(
				"for session in $(tmux list-sessions -F '#{session_name}' 2>/dev/null | grep -E '^%s(-[0-9]+)*$'); do tmux kill-session -t \"$session\" 2>/dev/null; done",
				FibreLoadSessionName,
			)

			fmt.Printf("Stopping all fibre-load sessions on %d validators...\n", len(cfg.Validators))

			return runScriptOverSSH(cfg.Validators, resolvedSSHKeyPath, killScript, timeout)
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory in which to initialize")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.json", "name of the config")
	cmd.Flags().StringVarP(&SSHKeyPath, "ssh-key-path", "k", "", "path to the user's SSH key (overrides environment variable and default)")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", time.Second*30, "timeout per machine for SSH command execution")

	return cmd
}
