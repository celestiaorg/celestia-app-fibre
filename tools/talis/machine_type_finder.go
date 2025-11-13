package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/spf13/cobra"
	"google.golang.org/api/option"
)

func findMachineTypesCmd() *cobra.Command {
	var machineType string
	var outputFormat string
	var project string
	var keyJSONPath string
	var prefix bool

	cmd := &cobra.Command{
		Use:   "find-machine-type",
		Short: "Find all zones that support a specific machine type or family",
		Long:  "Query Google Cloud to find all regions and zones where a specific machine type is available. Use --prefix to search by machine family (e.g., c4d, c4, n2).",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Extract project from JSON creds if provided
			if project == "" && keyJSONPath != "" {
				extractedProject, err := extractProjectFromJSON(keyJSONPath)
				if err == nil && extractedProject != "" {
					project = extractedProject
				}
			}

			// Try to get credentials from env or config if not provided via flags
			if project == "" || keyJSONPath == "" {
				cfg, err := LoadConfig(".")
				if err == nil {
					if project == "" && cfg.GoogleCloudProject != "" {
						project = cfg.GoogleCloudProject
					}
					if keyJSONPath == "" && cfg.GoogleCloudKeyJSONPath != "" {
						keyJSONPath = cfg.GoogleCloudKeyJSONPath
					}
				}
			}

			// Fall back to environment variables
			if project == "" {
				if envProject := os.Getenv("GOOGLE_CLOUD_PROJECT"); envProject != "" {
					project = envProject
				}
			}
			if keyJSONPath == "" {
				if envKey := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); envKey != "" {
					keyJSONPath = envKey
				}
			}

			if keyJSONPath == "" {
				return fmt.Errorf("credentials required: use --key-json flag or set GOOGLE_APPLICATION_CREDENTIALS env")
			}

			opts, err := buildClientOptions(keyJSONPath)
			if err != nil {
				return fmt.Errorf("failed to create client options: %w", err)
			}

			// Normalize machine type to lowercase for matching
			searchType := strings.ToLower(machineType)

			zones, err := findZonesWithMachineType(ctx, project, searchType, prefix, opts)
			if err != nil {
				return fmt.Errorf("failed to find zones: %w", err)
			}

			if len(zones) == 0 {
				if prefix {
					fmt.Printf("No zones found with machine types starting with: %s\n", machineType)
				} else {
					fmt.Printf("No zones found with machine type: %s\n", machineType)
				}
				return nil
			}

			switch outputFormat {
			case "go":
				printGoFormat(zones, machineType)
			case "list":
				printListFormat(zones)
			default:
				printTableFormat(zones, machineType, prefix)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&machineType, "machine-type", "m", "c4d-highcpu-64", "Machine type to search for (e.g., c4d-highcpu-64, c4-highcpu-64, or c4d for family)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, go, list")
	cmd.Flags().StringVarP(&project, "project", "p", "", "Google Cloud project ID (or set GOOGLE_CLOUD_PROJECT env)")
	cmd.Flags().StringVarP(&keyJSONPath, "key-json", "k", "", "Path to Google Cloud service account key JSON (or set GOOGLE_APPLICATION_CREDENTIALS env)")
	cmd.Flags().BoolVar(&prefix, "prefix", false, "Search by machine type prefix/family (e.g., -m c4d --prefix)")

	return cmd
}

func buildClientOptions(keyJSONPath string) ([]option.ClientOption, error) {
	var opts []option.ClientOption
	if keyJSONPath != "" {
		keyJSON, err := os.ReadFile(keyJSONPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read Google Cloud key file at %s: %w", keyJSONPath, err)
		}
		opts = append(opts, option.WithCredentialsJSON(keyJSON))
	}
	return opts, nil
}

func extractProjectFromJSON(keyJSONPath string) (string, error) {
	data, err := os.ReadFile(keyJSONPath)
	if err != nil {
		return "", err
	}

	var creds struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", err
	}

	return creds.ProjectID, nil
}

type zoneInfo struct {
	region string
	zone   string
}

func findZonesWithMachineType(ctx context.Context, project, machineType string, prefixMatch bool, opts []option.ClientOption) ([]zoneInfo, error) {
	// If no project specified, we need to list all zones globally
	// This requires using the Zones API to get all zones, then checking each zone
	if project == "" {
		return findZonesGlobally(ctx, machineType, prefixMatch, opts)
	}

	client, err := compute.NewMachineTypesRESTClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create machine types client: %w", err)
	}
	defer client.Close()

	aggregatedReq := &computepb.AggregatedListMachineTypesRequest{
		Project: project,
	}

	var foundZones []zoneInfo
	zoneSet := make(map[string]bool) // Track unique zones
	it := client.AggregatedList(ctx, aggregatedReq)

	for {
		pair, err := it.Next()
		if err != nil {
			break
		}

		// Zone names come in the format "zones/us-central1-a"
		scopeName := pair.Key
		if !strings.HasPrefix(scopeName, "zones/") {
			continue
		}

		zoneName := strings.TrimPrefix(scopeName, "zones/")

		// Check if this zone has the machine type we're looking for
		hasMatch := false
		for _, mt := range pair.Value.MachineTypes {
			if mt.Name != nil {
				mtName := strings.ToLower(*mt.Name)
				if prefixMatch {
					if strings.HasPrefix(mtName, machineType) {
						hasMatch = true
						break
					}
				} else {
					if mtName == machineType {
						hasMatch = true
						break
					}
				}
			}
		}

		if hasMatch && !zoneSet[zoneName] {
			// Extract region from zone (e.g., "us-central1-a" -> "us-central1")
			parts := strings.Split(zoneName, "-")
			region := strings.Join(parts[:len(parts)-1], "-")

			foundZones = append(foundZones, zoneInfo{
				region: region,
				zone:   zoneName,
			})
			zoneSet[zoneName] = true
		}
	}

	// Sort by region and zone
	sort.Slice(foundZones, func(i, j int) bool {
		if foundZones[i].region != foundZones[j].region {
			return foundZones[i].region < foundZones[j].region
		}
		return foundZones[i].zone < foundZones[j].zone
	})

	return foundZones, nil
}

// findZonesGlobally uses a public project to query machine types across all zones
func findZonesGlobally(ctx context.Context, machineType string, prefixMatch bool, opts []option.ClientOption) ([]zoneInfo, error) {
	// Use a public Google project that's available for queries
	publicProject := "ubuntu-os-cloud"
	return findZonesWithMachineType(ctx, publicProject, machineType, prefixMatch, opts)
}

func printTableFormat(zones []zoneInfo, machineType string, prefixMatch bool) {
	if prefixMatch {
		fmt.Printf("Machine types starting with '%s' are available in the following zones:\n\n", machineType)
	} else {
		fmt.Printf("Machine type '%s' is available in the following zones:\n\n", machineType)
	}
	fmt.Printf("%-20s %-20s\n", "REGION", "ZONE")
	fmt.Printf("%-20s %-20s\n", "------", "----")

	for _, z := range zones {
		fmt.Printf("%-20s %-20s\n", z.region, z.zone)
	}

	fmt.Printf("\nTotal: %d zones across %d regions\n", len(zones), countRegions(zones))
}

func printListFormat(zones []zoneInfo) {
	for _, z := range zones {
		fmt.Println(z.zone)
	}
}

func printGoFormat(zones []zoneInfo, machineType string) {
	// Group zones by region
	regionMap := make(map[string][]string)
	for _, z := range zones {
		regionMap[z.region] = append(regionMap[z.region], z.zone)
	}

	// Sort regions
	var regions []string
	for region := range regionMap {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	fmt.Printf("// Regions and zones that support %s\n", machineType)
	fmt.Println("var (")
	fmt.Printf("\tGCRegions = []string{\n")
	for i, region := range regions {
		if i < len(regions)-1 {
			fmt.Printf("\t\t%q,\n", region)
		} else {
			fmt.Printf("\t\t%q,\n", region)
		}
	}
	fmt.Printf("\t}\n")
	fmt.Printf("\tGCZones = map[string][]string{\n")
	for _, region := range regions {
		zoneList := regionMap[region]
		fmt.Printf("\t\t%q: {", region)
		for i, zone := range zoneList {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%q", zone)
		}
		fmt.Println("},")
	}
	fmt.Printf("\t}\n")
	fmt.Println(")")
}

func countRegions(zones []zoneInfo) int {
	regionSet := make(map[string]bool)
	for _, z := range zones {
		regionSet[z.region] = true
	}
	return len(regionSet)
}
