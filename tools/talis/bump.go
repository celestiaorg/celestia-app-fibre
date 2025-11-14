package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func bumpCmd() *cobra.Command {
	var (
		rootDir       string
		cfgPath       string
		SSHPubKeyPath string
		SSHKeyName    string
		DOAPIToken    string
		GCProject     string
		GCKeyJSONPath string
		workers       int
	)

	cmd := &cobra.Command{
		Use:   "bump",
		Short: "Spins up any pending nodes defined in the config",
		Long:  "Detects nodes that were added to the config but never provisioned and attempts to spin them up.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			cfg.SSHKeyName = resolveValue(SSHKeyName, EnvVarSSHKeyName, cfg.SSHKeyName)
			cfg.SSHPubKeyPath = resolveValue(SSHPubKeyPath, EnvVarSSHKeyPath, cfg.SSHPubKeyPath)
			cfg.DigitalOceanToken = resolveValue(DOAPIToken, EnvVarDigitalOceanToken, cfg.DigitalOceanToken)
			cfg.GoogleCloudProject = resolveValue(GCProject, EnvVarGoogleCloudProject, cfg.GoogleCloudProject)
			cfg.GoogleCloudKeyJSONPath = resolveValue(GCKeyJSONPath, EnvVarGoogleCloudKeyJSONPath, cfg.GoogleCloudKeyJSONPath)

			client, err := NewClient(cfg)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			if err := client.Bump(cmd.Context(), workers); err != nil {
				return fmt.Errorf("failed to bump network: %w", err)
			}

			if err := client.GetConfig().Save(rootDir); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&SSHPubKeyPath, "ssh-pub-key-path", "s", "", "path to the user's SSH public key")
	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory in which to initialize")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.json", "name of the config")
	cmd.Flags().StringVarP(&SSHKeyName, "ssh-key-name", "n", "", "name for the SSH key")
	cmd.Flags().StringVarP(&DOAPIToken, "do-api-token", "t", "", "digital ocean api token (defaults to config or env)")
	cmd.Flags().StringVar(&GCProject, "gc-project", "", "google cloud project (defaults to config or env)")
	cmd.Flags().StringVar(&GCKeyJSONPath, "gc-key-json-path", "", "path to google cloud service account key JSON file (defaults to config or env)")
	cmd.Flags().IntVarP(&workers, "workers", "w", 10, "number of concurrent workers for parallel operations (should be > 0)")

	return cmd
}
