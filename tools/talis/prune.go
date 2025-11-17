package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// isInstanceSSHable checks if an instance is reachable via SSH
func isInstanceSSHable(instance Instance, sshKeyPath string, timeout time.Duration) bool {
	if instance.PublicIP == "" || instance.PublicIP == pendingIPPlaceholder {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Try a simple SSH command to test connectivity
	ssh := exec.CommandContext(ctx,
		"ssh",
		"-i", sshKeyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		fmt.Sprintf("root@%s", instance.PublicIP),
		"echo 'SSH check successful'",
	)

	if err := ssh.Run(); err != nil {
		return false
	}

	return true
}

// deleteGCPInstance deletes a GCP instance
func deleteGCPInstance(ctx context.Context, instance Instance, project string, opts []option.ClientOption) error {
	if instance.Provider != GoogleCloud {
		return nil // Skip non-GCP instances
	}

	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create GCP client: %w", err)
	}
	defer client.Close()

	// Find the zone for this instance
	zone, err := findGCInstanceZone(ctx, project, instance.Name, instance.Region, opts)
	if err != nil {
		return fmt.Errorf("instance not found: %w", err)
	}

	req := &computepb.DeleteInstanceRequest{
		Project:  project,
		Zone:     zone,
		Instance: instance.Name,
	}

	op, err := client.Delete(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}

	// Wait for operation to complete
	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("delete operation failed: %w", err)
	}

	return nil
}

func pruneCmd() *cobra.Command {
	var rootDir string
	var checkResponsive bool
	var checkSSH bool
	var sshKeyPath string
	var destroy bool

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove instances that have TBD IPs or are not running",
		Long:  "Removes instances from the config file that were not successfully spun up (have 'TBD' IPs). Optionally checks if GCP instances are in RUNNING state with --check-responsive or if instances are SSH-able with --check-ssh. Use --destroy to delete instances from GCP.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			originalCount := len(cfg.Validators) + len(cfg.Bridges) + len(cfg.Lights)

			// Resolve SSH key path if check-ssh is enabled
			var resolvedSSHKeyPath string
			if checkSSH {
				resolvedSSHKeyPath = resolveValue(sshKeyPath, EnvVarSSHKeyPath, strings.ReplaceAll(cfg.SSHPubKeyPath, ".pub", ""))
				if resolvedSSHKeyPath == "" {
					return fmt.Errorf("ssh key path is required for --check-ssh (use --ssh-key-path or set SSH_KEY_PATH env var)")
				}
				log.Println("🔐 Checking SSH connectivity...")
			}

			// Setup GCP client options if responsive check or destroy is enabled
			var opts []option.ClientOption
			ctx := context.Background()
			if checkResponsive || destroy {
				if cfg.GoogleCloudProject == "" {
					return fmt.Errorf("google_cloud_project is required for --check-responsive or --destroy")
				}
				if cfg.GoogleCloudKeyJSONPath != "" {
					// Resolve path relative to config directory
					credPath := cfg.GoogleCloudKeyJSONPath
					if !filepath.IsAbs(credPath) {
						credPath = filepath.Join(rootDir, credPath)
					}
					opts = append(opts, option.WithCredentialsFile(credPath))
				}
				if checkResponsive {
					log.Println("🔍 Checking GCP instance status...")
				}
			}

			// Track instances to delete and keep
			type instanceWithType struct {
				instance Instance
				typeName string
			}

			var toDelete []instanceWithType
			var toDeleteMu sync.Mutex

			// Helper to check if instance should be kept
			shouldKeepInstance := func(instance Instance, nodeTypeName string) bool {
				if instance.PublicIP == pendingIPPlaceholder || instance.PrivateIP == pendingIPPlaceholder {
					log.Printf("🗑️  Removing %s %s (IPs not assigned)\n", nodeTypeName, instance.Name)
					toDeleteMu.Lock()
					toDelete = append(toDelete, instanceWithType{instance, nodeTypeName})
					toDeleteMu.Unlock()
					return false
				}
				if checkResponsive && !isGCPInstanceRunning(ctx, instance, cfg.GoogleCloudProject, opts) {
					log.Printf("🗑️  Removing %s %s (instance not running)\n", nodeTypeName, instance.Name)
					toDeleteMu.Lock()
					toDelete = append(toDelete, instanceWithType{instance, nodeTypeName})
					toDeleteMu.Unlock()
					return false
				}
				if checkSSH && !isInstanceSSHable(instance, resolvedSSHKeyPath, 15*time.Second) {
					log.Printf("🗑️  Removing %s %s (SSH not reachable)\n", nodeTypeName, instance.Name)
					toDeleteMu.Lock()
					toDelete = append(toDelete, instanceWithType{instance, nodeTypeName})
					toDeleteMu.Unlock()
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

			// Delete instances from GCP if --destroy flag is set
			if destroy && len(toDelete) > 0 {
				log.Printf("🔥 Deleting %d instances from GCP...\n", len(toDelete))

				var deleteWg sync.WaitGroup
				var deleteErrors []error
				var errorsMu sync.Mutex

				for _, item := range toDelete {
					deleteWg.Add(1)
					go func(inst instanceWithType) {
						defer deleteWg.Done()
						if err := deleteGCPInstance(ctx, inst.instance, cfg.GoogleCloudProject, opts); err != nil {
							log.Printf("❌ Failed to delete %s %s: %v\n", inst.typeName, inst.instance.Name, err)
							errorsMu.Lock()
							deleteErrors = append(deleteErrors, err)
							errorsMu.Unlock()
						} else {
							log.Printf("✅ Deleted %s %s from GCP\n", inst.typeName, inst.instance.Name)
						}
					}(item)
				}

				deleteWg.Wait()

				if len(deleteErrors) > 0 {
					log.Printf("⚠️  %d instances failed to delete from GCP\n", len(deleteErrors))
				} else {
					log.Println("✅ All instances deleted from GCP successfully")
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory containing the config")
	cmd.Flags().BoolVar(&checkResponsive, "check-responsive", false, "check if GCP instances are in RUNNING state (requires GCP credentials)")
	cmd.Flags().BoolVar(&checkSSH, "check-ssh", false, "check if instances are reachable via SSH (requires SSH key)")
	cmd.Flags().StringVarP(&sshKeyPath, "ssh-key-path", "k", "", "path to SSH private key (defaults to config's public key path without .pub)")
	cmd.Flags().BoolVar(&destroy, "destroy", false, "delete instances from GCP after removing from config (requires GCP credentials)")

	return cmd
}
