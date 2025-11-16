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
	"github.com/digitalocean/godo"
	"google.golang.org/api/option"
)

// MachineTypeLocation represents a region/zone combination where a machine type is available.
type MachineTypeLocation struct {
	Region string
	Zone   string
}

func findZonesWithMachineType(ctx context.Context, project, machineType string, prefixMatch bool, opts []option.ClientOption) ([]MachineTypeLocation, error) {
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

	var found []MachineTypeLocation
	seen := make(map[string]bool)
	it := client.AggregatedList(ctx, aggregatedReq)

	for {
		pair, err := it.Next()
		if err != nil {
			break
		}

		scope := pair.Key
		if !strings.HasPrefix(scope, "zones/") {
			continue
		}
		zoneName := strings.TrimPrefix(scope, "zones/")

		var hasMatch bool
		for _, mt := range pair.Value.MachineTypes {
			if mt.Name == nil {
				continue
			}
			name := strings.ToLower(*mt.Name)
			if prefixMatch && strings.HasPrefix(name, machineType) {
				hasMatch = true
				break
			}
			if !prefixMatch && name == machineType {
				hasMatch = true
				break
			}
		}

		if hasMatch && !seen[zoneName] {
			parts := strings.Split(zoneName, "-")
			region := strings.Join(parts[:len(parts)-1], "-")

			found = append(found, MachineTypeLocation{
				Region: region,
				Zone:   zoneName,
			})
			seen[zoneName] = true
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].Region != found[j].Region {
			return found[i].Region < found[j].Region
		}
		return found[i].Zone < found[j].Zone
	})

	return found, nil
}

func findZonesGlobally(ctx context.Context, machineType string, prefixMatch bool, opts []option.ClientOption) ([]MachineTypeLocation, error) {
	publicProject := "ubuntu-os-cloud"
	return findZonesWithMachineType(ctx, publicProject, machineType, prefixMatch, opts)
}

func findDigitalOceanRegions(ctx context.Context, client *godo.Client, machineType string, prefixMatch bool) ([]MachineTypeLocation, error) {
	if client == nil {
		return nil, fmt.Errorf("digitalocean client is nil")
	}

	opt := &godo.ListOptions{PerPage: 200}
	seen := make(map[string]bool)
	var locations []MachineTypeLocation

	for {
		sizes, resp, err := client.Sizes.List(ctx, opt)
		if err != nil {
			return nil, fmt.Errorf("failed to list DigitalOcean sizes: %w", err)
		}

		for _, size := range sizes {
			if size.Slug == "" {
				continue
			}
			slug := strings.ToLower(size.Slug)
			match := slug == machineType
			if prefixMatch && strings.HasPrefix(slug, machineType) {
				match = true
			}
			if !match {
				continue
			}

			for _, region := range size.Regions {
				if region == "" || seen[region] {
					continue
				}
				locations = append(locations, MachineTypeLocation{
					Region: region,
					Zone:   region,
				})
				seen[region] = true
			}
		}

		if resp == nil || resp.Links == nil || resp.Links.IsLastPage() {
			break
		}
		page, err := resp.Links.CurrentPage()
		if err != nil {
			return nil, fmt.Errorf("failed to paginate DigitalOcean sizes: %w", err)
		}
		opt.Page = page + 1
	}

	sort.Slice(locations, func(i, j int) bool {
		return locations[i].Region < locations[j].Region
	})

	return locations, nil
}

func countRegions(locations []MachineTypeLocation) int {
	regions := make(map[string]bool)
	for _, loc := range locations {
		regions[loc.Region] = true
	}
	return len(regions)
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
