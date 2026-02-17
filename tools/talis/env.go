package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func initEnvCmd() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "init-env",
		Short: "Generate a .env template file",
		Long:  "Generate a .env template file with the required environment variables for the specified cloud provider.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider == "" {
				provider = "digitalocean"
			}

			var envContent string

			switch provider {
			case "digitalocean":
				envContent = generateDigitalOceanEnv()
			case "googlecloud":
				envContent = generateGoogleCloudEnv()
			default:
				return fmt.Errorf("unknown provider %q (supported: digitalocean, googlecloud)", provider)
			}

			// Check if .env already exists
			if _, err := os.Stat(".env"); err == nil {
				return fmt.Errorf(".env file already exists. Delete it first or edit manually")
			}

			// Write .env file
			if err := os.WriteFile(".env", []byte(envContent), 0o600); err != nil {
				return fmt.Errorf("failed to write .env file: %w", err)
			}

			fmt.Printf("Created .env template for %s\n", provider)
			fmt.Println("\nNext steps:")
			fmt.Println("1. Edit .env and fill in your credentials")
			fmt.Println("2. Run: talis init -c <chain-id> -e <experiment> --with-observability --provider", provider)

			return nil
		},
	}

	cmd.Flags().StringVarP(&provider, "provider", "p", "digitalocean", "Cloud provider (digitalocean or googlecloud)")

	return cmd
}

func generateDigitalOceanEnv() string {
	return "# Provider Configuration\nPROVIDER=digitalocean\n\n# DigitalOcean Configuration\n# Get your API token from: https://cloud.digitalocean.com/account/api/tokens\nDIGITALOCEAN_TOKEN=\n\n# SSH Configuration (optional - will use defaults if not set)\n# TALIS_SSH_KEY_PATH=~/.ssh/id_ed25519.pub\n# TALIS_SSH_KEY_NAME=your-username\n\n# S3/DigitalOcean Spaces Configuration (optional - for payload distribution)\n# Create a Space and generate API keys at: https://cloud.digitalocean.com/spaces\n# AWS_DEFAULT_REGION=fra1\n# AWS_ACCESS_KEY_ID=\n# AWS_SECRET_ACCESS_KEY=\n# AWS_S3_BUCKET=\n# AWS_S3_ENDPOINT=https://fra1.digitaloceanspaces.com\n"
}

func generateGoogleCloudEnv() string {
	return "# Provider Configuration\nPROVIDER=googlecloud\n\n# Google Cloud Configuration\n# Project ID from: https://console.cloud.google.com/\nGOOGLE_CLOUD_PROJECT=\n\n# Service account key JSON path\n# Create at: https://console.cloud.google.com/iam-admin/serviceaccounts\n# Download the JSON key file and set the path below\nGOOGLE_CLOUD_KEY_JSON_PATH=\n\n# SSH Configuration (optional - will use defaults if not set)\n# TALIS_SSH_KEY_PATH=~/.ssh/id_ed25519.pub\n# TALIS_SSH_KEY_NAME=your-username\n\n# S3/DigitalOcean Spaces Configuration (optional - for payload distribution)\n# You can use DigitalOcean Spaces for S3-compatible storage\n# AWS_DEFAULT_REGION=fra1\n# AWS_ACCESS_KEY_ID=\n# AWS_SECRET_ACCESS_KEY=\n# AWS_S3_BUCKET=\n# AWS_S3_ENDPOINT=https://fra1.digitaloceanspaces.com\n"
}
