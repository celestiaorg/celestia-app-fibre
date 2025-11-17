package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/spf13/cobra"
)

func rebootCmd() *cobra.Command {
	var (
		rootDir string
		workers int
	)

	cmd := &cobra.Command{
		Use:   "reboot",
		Short: "Reboot all GCP instances",
		Long:  "Reboots GCP instances registered in the local config by stopping and starting them. This will restart all running instances.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if cfg.GoogleCloudProject == "" {
				return fmt.Errorf("google cloud project is not configured")
			}

			// Filter for GCP instances in config
			var gcpInstances []Instance
			for _, v := range cfg.Validators {
				if v.Provider == GoogleCloud {
					gcpInstances = append(gcpInstances, v)
				}
			}

			if len(gcpInstances) == 0 {
				log.Println("No GCP instances found in config")
				return nil
			}

			ctx := context.Background()

			log.Printf("Rebooting %d GCP instances from config...\n", len(gcpInstances))

			if err := rebootConfiguredGCInstances(ctx, cfg.GoogleCloudProject, gcpInstances, cfg, workers); err != nil {
				return fmt.Errorf("failed to reboot instances: %w", err)
			}

			log.Println("✅ All instances rebooted successfully")
			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, rootDirFlag, "d", ".", "root directory containing config (default is current directory)")
	cmd.Flags().IntVarP(&workers, "workers", "w", 10, "number of parallel workers for rebooting instances")

	return cmd
}

func rebootConfiguredGCInstances(ctx context.Context, project string, instances []Instance, cfg Config, workers int) error {
	opts, err := gcClientOptions(cfg)
	if err != nil {
		return fmt.Errorf("failed to create client options: %w", err)
	}

	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	// Build list of instances to reboot from config
	var instancesToReboot []struct {
		name   string
		zone   string
		region string
	}

	for _, inst := range instances {
		// Find the zone for this instance
		zone, err := findGCInstanceZone(ctx, project, inst.Name, inst.Region, opts)
		if err != nil {
			log.Printf("⚠️  Warning: Could not find instance %s in region %s: %v\n", inst.Name, inst.Region, err)
			continue
		}

		instancesToReboot = append(instancesToReboot, struct {
			name   string
			zone   string
			region string
		}{
			name:   inst.Name,
			zone:   zone,
			region: inst.Region,
		})
	}

	if len(instancesToReboot) == 0 {
		return fmt.Errorf("no instances found to reboot")
	}

	log.Printf("Found %d instances to reboot\n", len(instancesToReboot))

	// Reboot instances in parallel
	type result struct {
		name         string
		err          error
		timeRequired time.Duration
	}

	results := make(chan result, len(instancesToReboot))
	workerChan := make(chan struct{}, workers)
	var wg sync.WaitGroup
	wg.Add(len(instancesToReboot))

	for _, inst := range instancesToReboot {
		go func(name, zone string) {
			workerChan <- struct{}{}
			defer func() {
				<-workerChan
				wg.Done()
			}()

			start := time.Now()

			rebootCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			log.Printf("⏳ Stopping instance %s in zone %s\n", name, zone)

			// Stop the instance
			stopReq := &computepb.StopInstanceRequest{
				Project:  project,
				Zone:     zone,
				Instance: name,
			}

			op, err := client.Stop(rebootCtx, stopReq)
			if err != nil {
				results <- result{
					name:         name,
					err:          fmt.Errorf("failed to stop instance: %w", err),
					timeRequired: time.Since(start),
				}
				return
			}

			if err := op.Wait(rebootCtx); err != nil {
				results <- result{
					name:         name,
					err:          fmt.Errorf("failed to wait for stop: %w", err),
					timeRequired: time.Since(start),
				}
				return
			}

			log.Printf("⏳ Starting instance %s in zone %s\n", name, zone)

			// Start the instance
			startReq := &computepb.StartInstanceRequest{
				Project:  project,
				Zone:     zone,
				Instance: name,
			}

			op, err = client.Start(rebootCtx, startReq)
			if err != nil {
				results <- result{
					name:         name,
					err:          fmt.Errorf("failed to start instance: %w", err),
					timeRequired: time.Since(start),
				}
				return
			}

			if err := op.Wait(rebootCtx); err != nil {
				results <- result{
					name:         name,
					err:          fmt.Errorf("failed to wait for start: %w", err),
					timeRequired: time.Since(start),
				}
				return
			}

			results <- result{
				name:         name,
				err:          nil,
				timeRequired: time.Since(start),
			}
		}(inst.name, inst.zone)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var rebooted, failed int
	for res := range results {
		if res.err != nil {
			fmt.Printf("❌ %s: %v (took %v)\n", res.name, res.err, res.timeRequired)
			failed++
		} else {
			fmt.Printf("✅ %s rebooted successfully (took %v)\n", res.name, res.timeRequired)
			rebooted++
		}
		fmt.Printf("---- Progress: %d/%d (failed: %d)\n", rebooted+failed, len(instancesToReboot), failed)
	}

	if failed > 0 {
		return fmt.Errorf("%d instances failed to reboot", failed)
	}

	return nil
}
