package main

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

func pruneCmd() *cobra.Command {
	var rootDir string

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove instances from config that didn't get spun up (have TBD IPs)",
		Long:  "Removes instances from the config file that were not successfully spun up. These are instances that still have 'TBD' as their public or private IP address.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			originalCount := len(cfg.Validators) + len(cfg.Bridges) + len(cfg.Lights)

			// Filter out instances with TBD IPs
			var prunedValidators []Instance
			var prunedBridges []Instance
			var prunedLights []Instance

			for _, v := range cfg.Validators {
				if v.PublicIP == pendingIPPlaceholder || v.PrivateIP == pendingIPPlaceholder {
					log.Printf("🗑️  Removing validator %s (IPs not assigned)\n", v.Name)
				} else {
					prunedValidators = append(prunedValidators, v)
				}
			}

			for _, b := range cfg.Bridges {
				if b.PublicIP == pendingIPPlaceholder || b.PrivateIP == pendingIPPlaceholder {
					log.Printf("🗑️  Removing bridge %s (IPs not assigned)\n", b.Name)
				} else {
					prunedBridges = append(prunedBridges, b)
				}
			}

			for _, l := range cfg.Lights {
				if l.PublicIP == pendingIPPlaceholder || l.PrivateIP == pendingIPPlaceholder {
					log.Printf("🗑️  Removing light node %s (IPs not assigned)\n", l.Name)
				} else {
					prunedLights = append(prunedLights, l)
				}
			}

			cfg.Validators = prunedValidators
			cfg.Bridges = prunedBridges
			cfg.Lights = prunedLights

			newCount := len(cfg.Validators) + len(cfg.Bridges) + len(cfg.Lights)
			removedCount := originalCount - newCount

			if removedCount == 0 {
				log.Println("✅ No instances to prune (all have assigned IPs)")
				return nil
			}

			log.Printf("📊 Removed %d instances from config\n", removedCount)
			log.Printf("📊 Remaining: %d validators, %d bridges, %d lights\n",
				len(cfg.Validators), len(cfg.Bridges), len(cfg.Lights))

			// Save the updated config
			if err := cfg.Save(rootDir); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			log.Println("✅ Config updated successfully")
			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory containing the config")

	return cmd
}
