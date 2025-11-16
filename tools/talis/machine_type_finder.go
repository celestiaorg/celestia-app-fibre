package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

var (
	doGoTemplate = template.Must(template.New("do-go").Parse(`// DOMachineTypeRegions entry for {{ .MachineType }}
"{{ .MachineType }}": { {{- range .Regions }}"{{ . }}", {{ end }}},
`))

	gcGoTemplate = template.Must(template.New("gc-go").Parse(`// GCPMachineTypeRegions entry for {{ .MachineType }}
"{{ .MachineType }}": {
{{- range .Regions }}
	"{{ .Name }}": { {{- range .Zones }}"{{ . }}", {{ end }}},
{{- end }}
},
`))
)

func findMachineTypesCmd() *cobra.Command {
	var (
		rootDir      string
		machineType  string
		outputFormat string
		project      string
		keyJSONPath  string
		prefix       bool
		providerFlag string
		doToken      string
	)

	cmd := &cobra.Command{
		Use:   "find-machine-type",
		Short: "Find all locations that support a specific machine type or family",
		Long:  "Query Google Cloud or DigitalOcean to find all regions/zones where a specific machine type is available. Use --prefix to search by machine family (e.g., c4d, c4, n2).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := loadOptionalConfig(rootDir)
			if err != nil {
				return err
			}

			cfg.DigitalOceanToken = resolveValue(doToken, EnvVarDigitalOceanToken, cfg.DigitalOceanToken)
			cfg.GoogleCloudProject = resolveValue(project, EnvVarGoogleCloudProject, cfg.GoogleCloudProject)
			cfg.GoogleCloudKeyJSONPath = resolveValue(keyJSONPath, EnvVarGoogleCloudKeyJSONPath, cfg.GoogleCloudKeyJSONPath)
			if cfg.GoogleCloudKeyJSONPath == "" {
				cfg.GoogleCloudKeyJSONPath = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
			}

			provider := Provider(strings.ToLower(providerFlag))
			switch provider {
			case DigitalOcean:
				cfg.GoogleCloudProject = ""
				cfg.GoogleCloudKeyJSONPath = ""
			case GoogleCloud:
				cfg.DigitalOceanToken = ""
			default:
				return fmt.Errorf("unknown provider %q (supported: digitalocean, googlecloud)", providerFlag)
			}

			client, err := NewClientForProviders(cfg, provider)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			locations, err := client.FindMachineType(cmd.Context(), provider, machineType, prefix)
			if err != nil {
				return err
			}

			if len(locations) == 0 {
				scope := "regions"
				if provider == GoogleCloud {
					scope = "zones"
				}
				printNoResults(machineType, prefix, scope)
				return nil
			}

			scope := "regions"
			if provider == GoogleCloud {
				scope = "zones"
			}

			switch outputFormat {
			case "go":
				if provider == DigitalOcean {
					if err := printGoFormatDigitalOcean(locations, machineType); err != nil {
						return err
					}
				} else {
					if err := printGoFormatGoogleCloud(locations, machineType); err != nil {
						return err
					}
				}
			case "list":
				printListFormat(locations)
			default:
				printTableFormat(locations, machineType, prefix, scope)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory containing config.json")
	cmd.Flags().StringVarP(&machineType, "machine-type", "m", "c4d-highcpu-64", "Machine type to search for (e.g., c4d-highcpu-64, c4-highcpu-64, or c4d for family)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, go, list")
	cmd.Flags().StringVar(&project, "project", "", "Google Cloud project ID (or set GOOGLE_CLOUD_PROJECT env)")
	cmd.Flags().StringVarP(&keyJSONPath, "key-json", "k", "", "Path to Google Cloud service account key JSON (or set GOOGLE_APPLICATION_CREDENTIALS env)")
	cmd.Flags().BoolVar(&prefix, "prefix", false, "Search by machine type prefix/family (e.g., -m c4d --prefix)")
	cmd.Flags().StringVarP(&providerFlag, "provider", "p", string(GoogleCloud), "Cloud provider to query (digitalocean, googlecloud)")
	cmd.Flags().StringVar(&doToken, "do-api-token", "", "DigitalOcean API token (or set DIGITALOCEAN_TOKEN env)")

	return cmd
}

func printTableFormat(locations []MachineTypeLocation, machineType string, prefixMatch bool, scopeLabel string) {
	if prefixMatch {
		fmt.Printf("Machine types starting with '%s' are available in the following %s:\n\n", machineType, scopeLabel)
	} else {
		fmt.Printf("Machine type '%s' is available in the following %s:\n\n", machineType, scopeLabel)
	}
	header := strings.ToUpper(scopeLabel)
	fmt.Printf("%-20s %-20s\n", "REGION", header)
	fmt.Printf("%-20s %-20s\n", "------", strings.Repeat("-", len(header)))

	for _, loc := range locations {
		fmt.Printf("%-20s %-20s\n", loc.Region, loc.Zone)
	}

	if scopeLabel == "regions" {
		fmt.Printf("\nTotal: %d regions\n", countRegions(locations))
	} else {
		fmt.Printf("\nTotal: %d %s across %d regions\n", len(locations), scopeLabel, countRegions(locations))
	}
}

func printListFormat(locations []MachineTypeLocation) {
	for _, loc := range locations {
		fmt.Println(loc.Zone)
	}
}

func printGoFormatGoogleCloud(locations []MachineTypeLocation, machineType string) error {
	regionMap := make(map[string][]string)
	for _, loc := range locations {
		regionMap[loc.Region] = append(regionMap[loc.Region], loc.Zone)
	}

	var regions []string
	for region := range regionMap {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	type regionEntry struct {
		Name  string
		Zones []string
	}
	var regionData []regionEntry
	for _, region := range regions {
		zs := regionMap[region]
		regionData = append(regionData, regionEntry{
			Name:  region,
			Zones: zs,
		})
	}

	data := struct {
		MachineType string
		Regions     []regionEntry
	}{
		MachineType: machineType,
		Regions:     regionData,
	}

	return gcGoTemplate.Execute(os.Stdout, data)
}

func printGoFormatDigitalOcean(locations []MachineTypeLocation, machineType string) error {
	regions := make([]string, 0, len(locations))
	seen := make(map[string]bool)
	for _, loc := range locations {
		if !seen[loc.Region] {
			regions = append(regions, loc.Region)
			seen[loc.Region] = true
		}
	}
	sort.Strings(regions)

	data := struct {
		MachineType string
		Regions     []string
	}{
		MachineType: machineType,
		Regions:     regions,
	}

	return doGoTemplate.Execute(os.Stdout, data)
}

func printNoResults(machineType string, prefix bool, scope string) {
	if prefix {
		fmt.Printf("No %s found with machine types starting with: %s\n", scope, machineType)
	} else {
		fmt.Printf("No %s found with machine type: %s\n", scope, machineType)
	}
}

func loadOptionalConfig(rootDir string) (Config, bool, error) {
	cfg, err := LoadConfig(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, true, nil
}
