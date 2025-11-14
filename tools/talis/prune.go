package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/spf13/cobra"
	"google.golang.org/api/option"
)

// isGCPInstanceRunning checks if a GCP instance exists and is in RUNNING state
func isGCPInstanceRunning(ctx context.Context, instance Instance, project string, opts []option.ClientOption) bool {
	if instance.Provider != GoogleCloud {
		return true // Skip non-GCP instances
	}

	if instance.PublicIP == "" || instance.PublicIP == pendingIPPlaceholder {
		return false
	}

	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		log.Printf("⚠️  Failed to create GCP client: %v\n", err)
		return true // Assume responsive if we can't check
	}
	defer client.Close()

	// Try to find the instance in the region
	zone, err := findGCInstanceZone(ctx, project, instance.Name, instance.Region, opts)
	if err != nil {
		return false // Instance not found
	}

	req := &computepb.GetInstanceRequest{
		Project:  project,
		Zone:     zone,
		Instance: instance.Name,
	}

	gcpInst, err := client.Get(ctx, req)
	if err != nil {
		return false
	}

	// Check if instance status is RUNNING
	if gcpInst.Status == nil {
		return false
	}

	return *gcpInst.Status == "RUNNING"
}

func pruneCmd() *cobra.Command {
	var rootDir string
	var checkResponsive bool

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove instances that have TBD IPs or are not running",
		Long:  "Removes instances from the config file that were not successfully spun up (have 'TBD' IPs). Optionally checks if GCP instances are in RUNNING state with --check-responsive.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			originalCount := len(cfg.Validators) + len(cfg.Bridges) + len(cfg.Lights)

			// Setup GCP client options if responsive check is enabled
			var opts []option.ClientOption
			ctx := context.Background()
			if checkResponsive {
				if cfg.GoogleCloudProject == "" {
					return fmt.Errorf("google_cloud_project is required for --check-responsive")
				}
				if cfg.GoogleCloudKeyJSONPath != "" {
					opts = append(opts, option.WithCredentialsFile(cfg.GoogleCloudKeyJSONPath))
				}
				log.Println("🔍 Checking GCP instance status...")
			}

			// Helper to check if instance should be kept
			shouldKeepInstance := func(instance Instance, nodeTypeName string) bool {
				if instance.PublicIP == pendingIPPlaceholder || instance.PrivateIP == pendingIPPlaceholder {
					log.Printf("🗑️  Removing %s %s (IPs not assigned)\n", nodeTypeName, instance.Name)
					return false
				}
				if checkResponsive && !isGCPInstanceRunning(ctx, instance, cfg.GoogleCloudProject, opts) {
					log.Printf("🗑️  Removing %s %s (instance not running)\n", nodeTypeName, instance.Name)
					return false
				}
				return true
			}

			// Parallelize instance checks
			var mu sync.Mutex
			var wg sync.WaitGroup
			var prunedValidators []Instance
			var prunedBridges []Instance
			var prunedLights []Instance

			// Check validators
			for _, v := range cfg.Validators {
				wg.Add(1)
				go func(inst Instance) {
					defer wg.Done()
					if shouldKeepInstance(inst, "validator") {
						mu.Lock()
						prunedValidators = append(prunedValidators, inst)
						mu.Unlock()
					}
				}(v)
			}

			// Check bridges
			for _, b := range cfg.Bridges {
				wg.Add(1)
				go func(inst Instance) {
					defer wg.Done()
					if shouldKeepInstance(inst, "bridge") {
						mu.Lock()
						prunedBridges = append(prunedBridges, inst)
						mu.Unlock()
					}
				}(b)
			}

			// Check lights
			for _, l := range cfg.Lights {
				wg.Add(1)
				go func(inst Instance) {
					defer wg.Done()
					if shouldKeepInstance(inst, "light") {
						mu.Lock()
						prunedLights = append(prunedLights, inst)
						mu.Unlock()
					}
				}(l)
			}

			wg.Wait()

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
	cmd.Flags().BoolVar(&checkResponsive, "check-responsive", false, "check if GCP instances are in RUNNING state (requires GCP credentials)")

	return cmd
}
