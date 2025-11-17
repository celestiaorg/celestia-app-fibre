package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

const (
	GCDefaultValidatorMachineType = "c4d-highcpu-64"
	// GCDefaultImage points to the Ubuntu 22.04 LTS family, which ships the gVNIC driver out of the box.
	GCDefaultImage      = "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts"
	GCDefaultDiskSizeGB = 120
)

var (
	C4D_GCRegions = []string{
		"asia-northeast1",
		"asia-south1",
		"asia-southeast1",
		"europe-west1",
		"europe-west2",
		"europe-west3",
		"europe-west4",
		"us-central1",
		"us-east1",
		"us-east4",
		"us-west1",
		"us-west4",
	}
	C4D_GCZones = map[string][]string{
		"asia-northeast1": {"asia-northeast1-b", "asia-northeast1-c"},
		"asia-south1":     {"asia-south1-b", "asia-south1-c"},
		"asia-southeast1": {"asia-southeast1-a", "asia-southeast1-b", "asia-southeast1-c"},
		"europe-west1":    {"europe-west1-b"},
		"europe-west2":    {"europe-west2-a", "europe-west2-b"},
		"europe-west3":    {"europe-west3-a", "europe-west3-b", "europe-west3-c"},
		"europe-west4":    {"europe-west4-a", "europe-west4-b", "europe-west4-c"},
		"us-central1":     {"us-central1-a", "us-central1-b", "us-central1-c"},
		"us-east1":        {"us-east1-b", "us-east1-c"},
		"us-east4":        {"us-east4-a", "us-east4-b", "us-east4-c"},
		"us-west1":        {"us-west1-a", "us-west1-b"},
		"us-west4":        {"us-west4-a", "us-west4-b"},
	}
)

var (
	C3D_GCRegions = []string{
		"us-central1", "us-east1", "us-east4", "asia-southeast1", "europe-west1", "asia-east1",
	}
	C3D_GCZones = map[string][]string{
		"us-central1":     {"us-central1-a", "us-central1-b", "us-central1-c"},
		"us-east1":        {"us-east1-b", "us-east1-c", "us-east1-d"},
		"us-east4":        {"us-east4-a", "us-east4-b", "us-east4-c"},
		"asia-southeast1": {"asia-southeast1-a"},
		"europe-west1":    {"europe-west1-b", "europe-west1-c", "europe-west1-d"},
		"asia-east1":      {"asia-east1-a", "asia-east1-b", "asia-east1-c"},
	}
)

type GCClient struct {
	ClientInfo
	project string
}

func NewGCClient(cfg Config) (*GCClient, error) {
	if cfg.GoogleCloudProject == "" {
		return nil, errors.New("google cloud project is required")
	}

	sshKey, err := os.ReadFile(cfg.SSHPubKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH public key at: %s %w", cfg.SSHPubKeyPath, err)
	}

	return &GCClient{
		ClientInfo: ClientInfo{
			sshKey: sshKey,
			cfg:    cfg,
		},
		project: cfg.GoogleCloudProject,
	}, nil
}

func (c *GCClient) Up(ctx context.Context, workers int) error {
	insts := c.selectGCValidators(nil)
	if len(insts) == 0 {
		return fmt.Errorf("no instances to create")
	}

	return c.provisionGCInstances(ctx, insts, workers)
}

func (c *GCClient) Bump(ctx context.Context, workers int) error {
	insts := c.selectGCValidators(func(inst Instance) bool {
		return inst.NeedsProvision()
	})
	if len(insts) == 0 {
		log.Println("No pending Google Cloud instances to bump")
		return nil
	}

	return c.provisionGCInstances(ctx, insts, workers)
}

func (c *GCClient) Down(ctx context.Context, workers int) error {
	insts := c.selectGCValidators(nil)
	if len(insts) == 0 {
		return fmt.Errorf("no instances to destroy")
	}

	opts, err := gcClientOptions(c.cfg)
	if err != nil {
		return fmt.Errorf("failed to create client options: %w", err)
	}

	_, err = DestroyGCInstances(ctx, c.project, insts, opts, workers)
	return err
}

func (c *GCClient) List(ctx context.Context) error {
	opts, err := gcClientOptions(c.cfg)
	if err != nil {
		return fmt.Errorf("failed to create client options: %w", err)
	}

	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	// Get all regions and zones across all machine types
	regions, zonesMap := GetAllRegionsAndZones()

	cnt := 0
	for _, region := range regions {
		zones := zonesMap[region]
		for _, zone := range zones {
			req := &computepb.ListInstancesRequest{
				Project: c.project,
				Zone:    zone,
			}
			it := client.List(ctx, req)
			for {
				instance, err := it.Next()
				if err != nil {
					break
				}

				if instance.Labels != nil {
					if _, hasTalis := instance.Labels["talis"]; hasTalis {
						publicIP := ""
						if len(instance.NetworkInterfaces) > 0 {
							ni := instance.NetworkInterfaces[0]
							if len(ni.AccessConfigs) > 0 && ni.AccessConfigs[0].NatIP != nil {
								publicIP = *ni.AccessConfigs[0].NatIP
							}
						}

						if cnt == 0 {
							fmt.Printf("%-30s %-10s %-15s %-15s %s\n", "Name", "Status", "Zone", "Public IP", "Created")
							fmt.Printf("%-30s %-10s %-15s %-15s %s\n", "----", "------", "------", "---------", "-------")
						}

						status := "UNKNOWN"
						if instance.Status != nil {
							status = *instance.Status
						}
						name := ""
						if instance.Name != nil {
							name = *instance.Name
						}
						created := ""
						if instance.CreationTimestamp != nil {
							created = *instance.CreationTimestamp
						}

						fmt.Printf("%-30s %-10s %-15s %-15s %s\n",
							name,
							status,
							zone,
							publicIP,
							created)
						cnt++
					}
				}
			}
		}
	}

	fmt.Println("Total number of talis instances:", cnt)
	return nil
}

func (c *GCClient) GetConfig() Config {
	return c.cfg
}

func (c *GCClient) selectGCValidators(filter func(Instance) bool) []Instance {
	insts := make([]Instance, 0, len(c.cfg.Validators))
	for _, v := range c.cfg.Validators {
		if v.Provider != GoogleCloud {
			continue
		}
		if v.Region == "" || v.Region == RandomRegion {
			v.Region = RandomGCRegion()
		}
		if filter != nil && !filter(v) {
			continue
		}
		insts = append(insts, v)
	}
	return insts
}

func (c *GCClient) provisionGCInstances(ctx context.Context, insts []Instance, workers int) error {
	opts, err := gcClientOptions(c.cfg)
	if err != nil {
		return fmt.Errorf("failed to create client options: %w", err)
	}

	created, err := CreateGCInstances(ctx, c.project, insts, string(c.sshKey), opts, workers)
	if err != nil {
		return fmt.Errorf("failed to create instances: %w", err)
	}

	for _, inst := range created {
		cfg, err := c.cfg.UpdateInstance(inst.Name, inst.PublicIP, inst.PrivateIP)
		if err != nil {
			return fmt.Errorf("failed to update config with instance %s: %w", inst.Name, err)
		}
		c.cfg = cfg
	}

	return nil
}

func NewGoogleCloudValidator(region string) Instance {
	if region == "" || region == RandomRegion {
		region = RandomGCRegion()
	}
	i := NewBaseInstance(Validator)
	i.Provider = GoogleCloud
	i.Slug = GCDefaultValidatorMachineType
	i.Region = region
	return i
}

func RandomGCRegion() string {
	return RandomGCRegionForMachineType(GCDefaultValidatorMachineType)
}

// RandomGCRegionForMachineType returns a random region that supports the given machine type
func RandomGCRegionForMachineType(machineType string) string {
	regions := GetRegionsForMachineType(machineType)
	if len(regions) == 0 {
		// Fallback to C3D regions if machine type not recognized
		regions = C3D_GCRegions
	}
	return regions[rand.Intn(len(regions))]
}

// GetRegionsForMachineType returns the list of regions that support a given machine type
func GetRegionsForMachineType(machineType string) []string {
	machineType = strings.ToLower(machineType)

	// Check by prefix
	if strings.HasPrefix(machineType, "c4d-") {
		return C4D_GCRegions
	}
	if strings.HasPrefix(machineType, "c3d-") {
		return C3D_GCRegions
	}

	// Default to C3D regions for backwards compatibility
	return C3D_GCRegions
}

// GetZonesForMachineType returns the zone map for a given machine type
func GetZonesForMachineType(machineType string) map[string][]string {
	machineType = strings.ToLower(machineType)

	// Check by prefix
	if strings.HasPrefix(machineType, "c4d-") {
		return C4D_GCZones
	}
	if strings.HasPrefix(machineType, "c3d-") {
		return C3D_GCZones
	}

	// Default to C3D zones for backwards compatibility
	return C3D_GCZones
}

// GetAllRegionsAndZones returns all unique regions and zones across all machine type families
func GetAllRegionsAndZones() ([]string, map[string][]string) {
	allZones := make(map[string]map[string]bool)

	// Collect zones from all machine type families
	for region, zones := range C4D_GCZones {
		if allZones[region] == nil {
			allZones[region] = make(map[string]bool)
		}
		for _, zone := range zones {
			allZones[region][zone] = true
		}
	}

	for region, zones := range C3D_GCZones {
		if allZones[region] == nil {
			allZones[region] = make(map[string]bool)
		}
		for _, zone := range zones {
			allZones[region][zone] = true
		}
	}

	// Convert to sorted slices
	var regions []string
	zoneMap := make(map[string][]string)

	for region := range allZones {
		regions = append(regions, region)
		var zoneList []string
		for zone := range allZones[region] {
			zoneList = append(zoneList, zone)
		}
		sort.Strings(zoneList)
		zoneMap[region] = zoneList
	}
	sort.Strings(regions)

	return regions, zoneMap
}

func gcClientOptions(cfg Config) ([]option.ClientOption, error) {
	var opts []option.ClientOption
	if cfg.GoogleCloudKeyJSONPath != "" {
		keyJSON, err := os.ReadFile(cfg.GoogleCloudKeyJSONPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read Google Cloud key file at %s: %w", cfg.GoogleCloudKeyJSONPath, err)
		}
		opts = append(opts, option.WithCredentialsJSON(keyJSON))
	}
	return opts, nil
}

func RandomGCZone(region string) string {
	return RandomGCZoneForMachineType(region, GCDefaultValidatorMachineType)
}

// RandomGCZoneForMachineType returns a random zone for a region that supports the machine type
func RandomGCZoneForMachineType(region, machineType string) string {
	zonesMap := GetZonesForMachineType(machineType)
	zones, ok := zonesMap[region]
	if !ok || len(zones) == 0 {
		return region + "-a"
	}
	return zones[rand.Intn(len(zones))]
}

func ensureGCFirewallRule(ctx context.Context, project string, opts []option.ClientOption) error {
	client, err := compute.NewFirewallsRESTClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create firewall client: %w", err)
	}
	defer client.Close()

	firewallName := "talis-allow-all-ports"

	// Check if firewall rule already exists
	getReq := &computepb.GetFirewallRequest{
		Project:  project,
		Firewall: firewallName,
	}
	_, err = client.Get(ctx, getReq)
	if err == nil {
		// Firewall rule already exists
		log.Println("Firewall rule", firewallName, "already exists")
		return nil
	}

	// Create firewall rule to allow all incoming traffic
	log.Println("Creating firewall rule", firewallName, "to allow all incoming traffic")

	firewall := &computepb.Firewall{
		Name: &firewallName,
		Allowed: []*computepb.Allowed{
			{
				IPProtocol: ptr("tcp"),
				Ports:      []string{"0-65535"},
			},
			{
				IPProtocol: ptr("udp"),
				Ports:      []string{"0-65535"},
			},
			{
				IPProtocol: ptr("icmp"),
			},
		},
		Direction:    ptr(computepb.Firewall_INGRESS.String()),
		SourceRanges: []string{"0.0.0.0/0"},
		TargetTags:   []string{"talis-allow-all"},
	}

	insertReq := &computepb.InsertFirewallRequest{
		Project:          project,
		FirewallResource: firewall,
	}

	op, err := client.Insert(ctx, insertReq)
	if err != nil {
		return fmt.Errorf("failed to insert firewall rule: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed to wait for firewall rule creation: %w", err)
	}

	log.Println("Firewall rule", firewallName, "created successfully")
	return nil
}

func CreateGCInstances(ctx context.Context, project string, insts []Instance, sshKey string, opts []option.ClientOption, workers int) ([]Instance, error) {
	total := len(insts)

	type result struct {
		inst         Instance
		err          error
		timeRequired time.Duration
	}

	insts, existing, err := filterExistingGCInstances(ctx, project, insts, opts)
	if err != nil {
		return nil, err
	}

	if len(existing) > 0 {
		log.Println("Existing instances found, so they are not being created.")
		for _, v := range existing {
			log.Println("Skipping", v.Name, v.PublicIP, v.Tags)
		}
	}

	// Ensure a firewall rule exists to allow all ports
	if err := ensureGCFirewallRule(ctx, project, opts); err != nil {
		return nil, fmt.Errorf("failed to ensure firewall rule: %w", err)
	}

	results := make(chan result, total)
	workerChan := make(chan struct{}, workers)
	var wg sync.WaitGroup
	wg.Add(len(insts))

	for _, v := range insts {
		go func(inst Instance) {
			workerChan <- struct{}{}
			defer func() {
				<-workerChan
				wg.Done()
			}()

			ctx, cancel := context.WithTimeout(ctx, 7*time.Minute)
			defer cancel()

			start := time.Now()
			log.Println("Creating instance", inst.Name, "in region", inst.Region, start.Format(time.RFC3339))

			zone := RandomGCZoneForMachineType(inst.Region, inst.Slug)
			pubIP, privIP, err := createGCInstance(ctx, project, inst, zone, sshKey, opts)
			if err != nil {
				results <- result{inst: inst, err: fmt.Errorf("create %s: %w", inst.Name, err)}
				return
			}

			inst.PublicIP = pubIP
			inst.PrivateIP = privIP
			results <- result{inst: inst, err: nil, timeRequired: time.Since(start)}
		}(v)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var created []Instance
	for res := range results {
		if res.err != nil {
			fmt.Printf("❌ %s failed after %v %v\n", res.inst.Name, res.timeRequired, res.err)
		} else {
			created = append(created, res.inst)
			fmt.Printf("✅ %s is up (public=%s) in %v\n",
				res.inst.Name, res.inst.PublicIP, res.timeRequired)
		}
		fmt.Printf("---- Progress: %d/%d\n", len(created), total)
	}

	return created, nil
}

func createGCInstance(ctx context.Context, project string, inst Instance, zone string, sshKey string, opts []option.ClientOption) (string, string, error) {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return "", "", fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	labels := make(map[string]string)
	for _, tag := range inst.Tags {
		labels[strings.ReplaceAll(tag, "-", "_")] = "true"
	}

	username := "root"
	sshKeyMetadata := fmt.Sprintf("%s:%s", username, strings.TrimSpace(sshKey))

	machineType := fmt.Sprintf("zones/%s/machineTypes/%s", zone, inst.Slug)
	sourceImage := GCDefaultImage

	req := &computepb.InsertInstanceRequest{
		Project: project,
		Zone:    zone,
		InstanceResource: &computepb.Instance{
			Name:        &inst.Name,
			MachineType: &machineType,
			Labels:      labels,
			Tags: &computepb.Tags{
				Items: []string{"talis-allow-all"},
			},
			Disks: []*computepb.AttachedDisk{
				{
					Boot:       ptr(true),
					AutoDelete: ptr(true),
					GuestOsFeatures: []*computepb.GuestOsFeature{
						{Type: ptr(computepb.GuestOsFeature_GVNIC.String())},
					},
					InitializeParams: &computepb.AttachedDiskInitializeParams{
						SourceImage: &sourceImage,
						DiskSizeGb:  ptr(int64(GCDefaultDiskSizeGB)),
					},
				},
			},
			NetworkInterfaces: []*computepb.NetworkInterface{
				{
					NicType: ptr(computepb.NetworkInterface_GVNIC.String()),
					AccessConfigs: []*computepb.AccessConfig{
						{
							Name: ptr("External NAT"),
							Type: ptr(computepb.AccessConfig_ONE_TO_ONE_NAT.String()),
						},
					},
				},
			},
			NetworkPerformanceConfig: &computepb.NetworkPerformanceConfig{
				TotalEgressBandwidthTier: ptr("TIER_1"),
			},
			Metadata: &computepb.Metadata{
				Items: []*computepb.Items{
					{
						Key:   ptr("ssh-keys"),
						Value: &sshKeyMetadata,
					},
				},
			},
			Scheduling: &computepb.Scheduling{
				OnHostMaintenance: ptr(computepb.Scheduling_TERMINATE.String()),
			},
		},
	}

	op, err := client.Insert(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("failed to insert instance: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return "", "", fmt.Errorf("failed to wait for instance creation: %w", err)
	}

	pubIP, privIP, err := waitForGCNetworkIP(ctx, client, project, zone, inst.Name)
	if err != nil {
		return "", "", fmt.Errorf("failed to get IPs: %w", err)
	}

	return pubIP, privIP, nil
}

func waitForGCNetworkIP(ctx context.Context, client *compute.InstancesClient, project, zone, name string) (string, string, error) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-ticker.C:
			req := &computepb.GetInstanceRequest{
				Project:  project,
				Zone:     zone,
				Instance: name,
			}
			instance, err := client.Get(ctx, req)
			if err != nil {
				return "", "", err
			}

			var pubIP, privIP string
			if len(instance.NetworkInterfaces) > 0 {
				ni := instance.NetworkInterfaces[0]
				if ni.NetworkIP != nil {
					privIP = *ni.NetworkIP
				}
				if len(ni.AccessConfigs) > 0 && ni.AccessConfigs[0].NatIP != nil {
					pubIP = *ni.AccessConfigs[0].NatIP
				}
			}

			if pubIP != "" && privIP != "" {
				return pubIP, privIP, nil
			}
		}
	}
}

func filterExistingGCInstances(ctx context.Context, project string, insts []Instance, opts []option.ClientOption) ([]Instance, []Instance, error) {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	existingTags := make(map[string]bool)
	regions, zonesMap := GetAllRegionsAndZones()
	for _, region := range regions {
		zones := zonesMap[region]
		for _, zone := range zones {
			req := &computepb.ListInstancesRequest{
				Project: project,
				Zone:    zone,
			}
			it := client.List(ctx, req)
			for {
				instance, err := it.Next()
				if err != nil {
					break
				}
				if instance.Labels != nil {
					for label := range instance.Labels {
						existingTags[strings.ReplaceAll(label, "_", "-")] = true
					}
				}
			}
		}
	}

	var newInsts, existing []Instance
	for _, inst := range insts {
		experimentTag := GetExperimentTag(inst.Tags)
		if experimentTag == "" || !existingTags[experimentTag] {
			newInsts = append(newInsts, inst)
		} else {
			existing = append(existing, inst)
		}
	}

	return newInsts, existing, nil
}

func DestroyGCInstances(ctx context.Context, project string, insts []Instance, opts []option.ClientOption, workers int) ([]Instance, error) {
	return destroyGCInstancesInternal(ctx, project, insts, opts, workers)
}

func findGCInstanceZone(ctx context.Context, project, instanceName, region string, opts []option.ClientOption) (string, error) {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return "", fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	// Try all zones across all machine types for this region
	_, zonesMap := GetAllRegionsAndZones()
	zones := zonesMap[region]
	if len(zones) == 0 {
		zones = []string{region + "-a", region + "-b", region + "-c"}
	}

	for _, zone := range zones {
		req := &computepb.GetInstanceRequest{
			Project:  project,
			Zone:     zone,
			Instance: instanceName,
		}
		_, err := client.Get(ctx, req)
		if err == nil {
			return zone, nil
		}
	}

	return "", fmt.Errorf("instance %s not found in any zone of region %s", instanceName, region)
}

func deleteGCInstance(ctx context.Context, project, zone, name string, opts []option.ClientOption) error {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	req := &computepb.DeleteInstanceRequest{
		Project:  project,
		Zone:     zone,
		Instance: name,
	}

	op, err := client.Delete(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed to wait for deletion: %w", err)
	}

	return nil
}

func checkForRunningGCExperiments(ctx context.Context, project string, opts []option.ClientOption, experimentID, chainID string) (bool, error) {
	if project == "" {
		return false, nil
	}

	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return false, fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	regions, zonesMap := GetAllRegionsAndZones()
	for _, region := range regions {
		zones := zonesMap[region]
		for _, zone := range zones {
			req := &computepb.ListInstancesRequest{
				Project: project,
				Zone:    zone,
			}
			it := client.List(ctx, req)
			for {
				instance, err := it.Next()
				if err != nil {
					break
				}
				if instance.Labels != nil {
					if _, hasTalis := instance.Labels["talis"]; hasTalis {
						for label := range instance.Labels {
							if hasGCExperimentLabel(label, experimentID, chainID) {
								return true, nil
							}
						}
					}
				}
			}
		}
	}

	return false, nil
}

func hasGCExperimentLabel(label, experimentID, chainID string) bool {
	if !strings.HasPrefix(label, "validator_") && !strings.HasPrefix(label, "bridge_") && !strings.HasPrefix(label, "light_") {
		return false
	}
	experimentIDLabel := strings.ReplaceAll(experimentID, "-", "_")
	chainIDLabel := strings.ReplaceAll(chainID, "-", "_")
	return strings.Contains(label, experimentIDLabel) && strings.Contains(label, chainIDLabel)
}

func destroyAllTalisGCInstances(ctx context.Context, project string, opts []option.ClientOption, workers int) ([]Instance, error) {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	var talisInstances []Instance
	regions, zonesMap := GetAllRegionsAndZones()
	for _, region := range regions {
		zones := zonesMap[region]
		for _, zone := range zones {
			req := &computepb.ListInstancesRequest{
				Project: project,
				Zone:    zone,
			}
			it := client.List(ctx, req)
			for {
				instance, err := it.Next()
				if err != nil {
					break
				}
				if instance.Labels != nil {
					if _, hasTalis := instance.Labels["talis"]; hasTalis {
						publicIP := ""
						if len(instance.NetworkInterfaces) > 0 {
							ni := instance.NetworkInterfaces[0]
							if len(ni.AccessConfigs) > 0 && ni.AccessConfigs[0].NatIP != nil {
								publicIP = *ni.AccessConfigs[0].NatIP
							}
						}
						name := ""
						if instance.Name != nil {
							name = *instance.Name
						}
						talisInstances = append(talisInstances, Instance{
							Name:     name,
							PublicIP: publicIP,
							Region:   region,
						})
					}
				}
			}
		}
	}

	if len(talisInstances) == 0 {
		log.Println("No talis instances found to destroy")
		return nil, nil
	}

	return destroyGCInstancesInternal(ctx, project, talisInstances, opts, workers)
}

func destroyGCInstancesInternal(ctx context.Context, project string, insts []Instance, opts []option.ClientOption, workers int) ([]Instance, error) {
	type result struct {
		inst         Instance
		err          error
		timeRequired time.Duration
	}

	results := make(chan result, len(insts))
	workerChan := make(chan struct{}, workers)
	var wg sync.WaitGroup
	wg.Add(len(insts))

	for _, inst := range insts {
		go func(inst Instance) {
			workerChan <- struct{}{}
			defer func() {
				<-workerChan
				wg.Done()
			}()
			start := time.Now()

			fmt.Println("⏳ Deleting instance", inst.Name, inst.PublicIP)

			delCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			zone, err := findGCInstanceZone(delCtx, project, inst.Name, inst.Region, opts)
			if err != nil {
				results <- result{inst: inst, err: fmt.Errorf("find zone for %s: %w", inst.Name, err)}
				return
			}

			if err := deleteGCInstance(delCtx, project, zone, inst.Name, opts); err != nil {
				results <- result{inst: inst, err: fmt.Errorf("delete %s: %w", inst.Name, err)}
				return
			}

			results <- result{inst: inst, err: nil, timeRequired: time.Since(start)}
		}(inst)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var removed []Instance
	var failed []result
	for res := range results {
		if res.err != nil {
			fmt.Printf("❌ %s failed to delete after %v: %v\n",
				res.inst.Name, res.timeRequired, res.err)
			failed = append(failed, res)
		} else {
			removed = append(removed, res.inst)
			fmt.Printf("✅ %s deleted (took %v)\n", res.inst.Name, res.timeRequired)
		}
		fmt.Printf("---- Progress: %d/%d\n", len(removed)+len(failed), len(insts))
	}

	return removed, nil
}

func ptr[T any](v T) *T {
	return &v
}
