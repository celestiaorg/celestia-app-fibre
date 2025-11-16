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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

const (
	AWSDefaultInstanceType = "c7i.8xlarge"
	AWSDefaultDiskSizeGB   = 200
	awsRootDevice          = "/dev/sda1"
	awsSecurityGroupName   = "talis-allow-all"
)

var AWSPreferredRegions = []string{
	"us-east-1",
	"us-east-2",
	"us-west-2",
	"eu-central-1",
	"eu-west-1",
	"eu-west-2",
	"eu-west-3",
	"eu-north-1",
	"ap-south-1",
	"ap-southeast-1",
	"ap-southeast-2",
	"ap-northeast-1",
}

type AWSClient struct {
	ClientInfo
	awsCfg aws.Config

	mu             sync.Mutex
	ec2Clients     map[string]*ec2.Client
	securityGroups map[string]string
	subnets        map[string]string
	keyPairs       map[string]bool
	amiIDs         map[string]string
}

var errAWSInstanceNotFound = errors.New("aws instance not found")

func NewAWSClient(cfg *Config) (*AWSClient, error) {
	if cfg.SSHPubKeyPath == "" {
		return nil, errors.New("SSH public key path is required for AWS instances")
	}
	if cfg.SSHKeyName == "" {
		return nil, errors.New("SSH key name is required for AWS instances")
	}

	awsCfg, err := buildAWSConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build AWS config: %w", err)
	}

	sshKey, err := os.ReadFile(cfg.SSHPubKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH public key at %s: %w", cfg.SSHPubKeyPath, err)
	}

	return &AWSClient{
		ClientInfo: ClientInfo{
			sshKey: sshKey,
			cfg:    cfg,
		},
		awsCfg:         awsCfg,
		ec2Clients:     make(map[string]*ec2.Client),
		securityGroups: make(map[string]string),
		subnets:        make(map[string]string),
		keyPairs:       make(map[string]bool),
		amiIDs:         make(map[string]string),
	}, nil
}

func (c *AWSClient) Up(ctx context.Context, workers int) error {
	insts := c.selectAWSValidators(nil)
	if len(insts) == 0 {
		return fmt.Errorf("no instances to create")
	}

	return c.provisionAWSInstances(ctx, insts, workers)
}

func (c *AWSClient) Bump(ctx context.Context, workers int) error {
	insts := c.selectAWSValidators(func(inst Instance) bool {
		return inst.NeedsProvision()
	})
	if len(insts) == 0 {
		log.Println("No pending AWS instances to bump")
		return nil
	}

	return c.provisionAWSInstances(ctx, insts, workers)
}

func (c *AWSClient) Down(ctx context.Context, workers int) error {
	insts := c.selectAWSValidators(nil)
	if len(insts) == 0 {
		return fmt.Errorf("no instances to destroy")
	}

	_, err := c.destroyAWSInstances(ctx, insts, workers)
	return err
}

func (c *AWSClient) List(ctx context.Context) error {
	client, err := c.ec2Client(c.awsCfg.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS client: %w", err)
	}

	regions, err := describeAWSRegions(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to describe AWS regions: %w", err)
	}

	printedHeader := false
	total := 0
	for _, region := range regions {
		ec2Client, err := c.ec2Client(region)
		if err != nil {
			return err
		}

		instances, err := listTalisInstances(ctx, ec2Client)
		if err != nil {
			return fmt.Errorf("list aws instances in %s: %w", region, err)
		}

		for _, inst := range instances {
			if !printedHeader && len(instances) > 0 {
				fmt.Printf("%-30s %-12s %-15s %-15s %s\n", "Name", "State", "Region", "Public IP", "Launch Time")
				fmt.Printf("%-30s %-12s %-15s %-15s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 12), strings.Repeat("-", 15), strings.Repeat("-", 15), strings.Repeat("-", 20))
				printedHeader = true
			}

			state := "unknown"
			if inst.State != nil {
				state = string(inst.State.Name)
			}
			name := awsTagValue(inst.Tags, "Name")
			fmt.Printf("%-30s %-12s %-15s %-15s %s\n",
				name,
				state,
				region,
				aws.ToString(inst.PublicIpAddress),
				formatLaunchTime(inst.LaunchTime),
			)
			total++
		}
	}

	fmt.Println("Total number of talis instances:", total)
	return nil
}

func (c *AWSClient) FindMachineType(ctx context.Context, provider Provider, machineType string, prefix bool) ([]MachineTypeLocation, error) {
	if provider != "" && provider != AWS {
		return nil, fmt.Errorf("aws client cannot query provider %s", provider)
	}
	if prefix {
		return nil, fmt.Errorf("aws machine type prefix search is not supported yet")
	}
	return findAWSMachineTypeLocations(ctx, c.awsCfg, machineType)
}

func (c *AWSClient) GetConfig() Config {
	return *c.cfg
}

func (c *AWSClient) selectAWSValidators(filter func(Instance) bool) []Instance {
	insts := make([]Instance, 0, len(c.cfg.Validators))
	for _, v := range c.cfg.Validators {
		if v.Provider != AWS {
			continue
		}
		if v.Region == "" || v.Region == RandomRegion {
			v.Region = RandomAWSRegion()
		}
		if v.Slug == "" {
			v.Slug = AWSDefaultInstanceType
		}
		if filter != nil && !filter(v) {
			continue
		}
		insts = append(insts, v)
	}
	return insts
}

func (c *AWSClient) provisionAWSInstances(ctx context.Context, insts []Instance, workers int) error {
	insts, existing, err := c.filterExistingAWSInstances(ctx, insts)
	if err != nil {
		return err
	}

	if len(existing) > 0 {
		log.Println("Existing AWS instances found, skipping creation:")
		for _, inst := range existing {
			log.Printf("- %s (%s)\n", inst.Name, inst.Region)
		}
	}

	if len(insts) == 0 {
		return nil
	}

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
		inst := inst
		go func() {
			workerChan <- struct{}{}
			defer func() {
				<-workerChan
				wg.Done()
			}()

			region := inst.Region
			instCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()

			start := time.Now()
			created, err := c.createAWSInstance(instCtx, inst)
			if err != nil {
				results <- result{inst: inst, err: err}
				return
			}

			results <- result{inst: created, err: nil, timeRequired: time.Since(start)}
			log.Printf("Provisioned AWS instance %s in %s", inst.Name, region)
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var created []Instance
	for res := range results {
		if res.err != nil {
			fmt.Printf("❌ %s failed: %v\n", res.inst.Name, res.err)
			continue
		}
		created = append(created, res.inst)
		fmt.Printf("✅ %s is up (public=%s) in %v\n", res.inst.Name, res.inst.PublicIP, res.timeRequired)
		if err := c.cfg.UpdateInstance(res.inst.Name, res.inst.PublicIP, res.inst.PrivateIP); err != nil {
			return fmt.Errorf("failed to update config for %s: %w", res.inst.Name, err)
		}
	}

	return nil
}

func (c *AWSClient) destroyAWSInstances(ctx context.Context, insts []Instance, workers int) ([]Instance, error) {
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
		inst := inst
		go func() {
			workerChan <- struct{}{}
			defer func() {
				<-workerChan
				wg.Done()
			}()

			instCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			start := time.Now()

			if err := c.terminateAWSInstance(instCtx, inst); err != nil {
				results <- result{inst: inst, err: err}
				return
			}

			results <- result{inst: inst, err: nil, timeRequired: time.Since(start)}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var removed []Instance
	for res := range results {
		if res.err != nil {
			fmt.Printf("❌ %s failed to delete: %v\n", res.inst.Name, res.err)
			continue
		}
		removed = append(removed, res.inst)
		fmt.Printf("✅ %s deleted (took %v)\n", res.inst.Name, res.timeRequired)
	}

	return removed, nil
}

func (c *AWSClient) createAWSInstance(ctx context.Context, inst Instance) (Instance, error) {
	ec2Client, err := c.ec2Client(inst.Region)
	if err != nil {
		return inst, err
	}

	if err := c.ensureKeyPair(ctx, ec2Client, inst.Region); err != nil {
		return inst, err
	}

	sgID, err := c.ensureSecurityGroup(ctx, ec2Client, inst.Region)
	if err != nil {
		return inst, err
	}

	subnetID, err := c.ensureDefaultSubnet(ctx, ec2Client, inst.Region)
	if err != nil {
		return inst, err
	}

	imageID, err := c.ensureUbuntuAMI(ctx, ec2Client, inst.Region)
	if err != nil {
		return inst, err
	}

	tags := awsTagsForInstance(inst)
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(imageID),
		InstanceType: types.InstanceType(inst.Slug),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		KeyName:      aws.String(c.cfg.SSHKeyName),
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{
			{
				AssociatePublicIpAddress: aws.Bool(true),
				DeviceIndex:              aws.Int32(0),
				SubnetId:                 aws.String(subnetID),
				Groups:                   []string{sgID},
			},
		},
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				DeviceName: aws.String(awsRootDevice),
				Ebs: &types.EbsBlockDevice{
					VolumeSize:          aws.Int32(AWSDefaultDiskSizeGB),
					VolumeType:          types.VolumeTypeGp3,
					DeleteOnTermination: aws.Bool(true),
				},
			},
		},
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: tags},
			{ResourceType: types.ResourceTypeVolume, Tags: tags},
		},
	}

	resp, err := ec2Client.RunInstances(ctx, input)
	if err != nil {
		return inst, fmt.Errorf("run instance %s: %w", inst.Name, err)
	}

	if len(resp.Instances) == 0 || resp.Instances[0].InstanceId == nil {
		return inst, fmt.Errorf("aws did not return instance id for %s", inst.Name)
	}
	instanceID := aws.ToString(resp.Instances[0].InstanceId)

	waiter := ec2.NewInstanceRunningWaiter(ec2Client)
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}}, 10*time.Minute); err != nil {
		return inst, fmt.Errorf("wait for %s to run: %w", inst.Name, err)
	}

	desc, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return inst, fmt.Errorf("describe %s: %w", inst.Name, err)
	}

	if len(desc.Reservations) == 0 || len(desc.Reservations[0].Instances) == 0 {
		return inst, fmt.Errorf("aws returned no reservations for %s", inst.Name)
	}
	awsInst := desc.Reservations[0].Instances[0]
	inst.PublicIP = aws.ToString(awsInst.PublicIpAddress)
	inst.PrivateIP = aws.ToString(awsInst.PrivateIpAddress)
	return inst, nil
}

func (c *AWSClient) terminateAWSInstance(ctx context.Context, inst Instance) error {
	ec2Client, err := c.ec2Client(inst.Region)
	if err != nil {
		return err
	}

	awsInst, err := c.findInstanceByName(ctx, ec2Client, inst.Name)
	if err != nil {
		if errors.Is(err, errAWSInstanceNotFound) {
			return nil
		}
		return err
	}

	instanceID := aws.ToString(awsInst.InstanceId)
	if instanceID == "" {
		return fmt.Errorf("instance %s missing id", inst.Name)
	}

	_, err = ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return fmt.Errorf("terminate %s: %w", inst.Name, err)
	}

	waiter := ec2.NewInstanceTerminatedWaiter(ec2Client)
	return waiter.Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}}, 10*time.Minute)
}

func (c *AWSClient) filterExistingAWSInstances(ctx context.Context, insts []Instance) ([]Instance, []Instance, error) {
	var newInsts []Instance
	var existing []Instance

	for _, inst := range insts {
		ec2Client, err := c.ec2Client(inst.Region)
		if err != nil {
			return nil, nil, err
		}
		_, err = c.findInstanceByName(ctx, ec2Client, inst.Name)
		if err != nil {
			if errors.Is(err, errAWSInstanceNotFound) {
				newInsts = append(newInsts, inst)
				continue
			}
			return nil, nil, err
		}
		existing = append(existing, inst)
	}

	return newInsts, existing, nil
}

func (c *AWSClient) findInstanceByName(ctx context.Context, client *ec2.Client, name string) (*types.Instance, error) {
	filters := []types.Filter{
		{Name: aws.String("tag:Name"), Values: []string{name}},
		{Name: aws.String("tag-key"), Values: []string{"talis"}},
		{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
	}
	input := &ec2.DescribeInstancesInput{Filters: filters}
	paginator := ec2.NewDescribeInstancesPaginator(client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, res := range page.Reservations {
			for _, inst := range res.Instances {
				return &inst, nil
			}
		}
	}
	return nil, errAWSInstanceNotFound
}

func (c *AWSClient) ec2Client(region string) (*ec2.Client, error) {
	if region == "" {
		region = c.awsCfg.Region
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.ec2Clients[region]; ok {
		return client, nil
	}
	cfg := c.awsCfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)
	c.ec2Clients[region] = client
	return client, nil
}

func (c *AWSClient) ensureKeyPair(ctx context.Context, client *ec2.Client, region string) error {
	c.mu.Lock()
	if c.keyPairs[region] {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	_, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{
		KeyNames: []string{c.cfg.SSHKeyName},
	})
	if err == nil {
		c.mu.Lock()
		c.keyPairs[region] = true
		c.mu.Unlock()
		return nil
	}

	_, err = client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String(c.cfg.SSHKeyName),
		PublicKeyMaterial: c.sshKey,
	})
	if err != nil {
		return fmt.Errorf("import key pair %s: %w", c.cfg.SSHKeyName, err)
	}

	c.mu.Lock()
	c.keyPairs[region] = true
	c.mu.Unlock()
	return nil
}

func (c *AWSClient) ensureSecurityGroup(ctx context.Context, client *ec2.Client, region string) (string, error) {
	c.mu.Lock()
	if id, ok := c.securityGroups[region]; ok {
		c.mu.Unlock()
		return id, nil
	}
	c.mu.Unlock()

	vpcID, err := defaultVPC(ctx, client)
	if err != nil {
		return "", err
	}

	filt := []types.Filter{
		{Name: aws.String("group-name"), Values: []string{awsSecurityGroupName}},
		{Name: aws.String("vpc-id"), Values: []string{vpcID}},
	}
	resp, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: filt})
	if err == nil && len(resp.SecurityGroups) > 0 {
		id := aws.ToString(resp.SecurityGroups[0].GroupId)
		c.mu.Lock()
		c.securityGroups[region] = id
		c.mu.Unlock()
		return id, nil
	}

	createOut, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		Description: aws.String("Allow all traffic for Talis experiments"),
		GroupName:   aws.String(awsSecurityGroupName),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		return "", fmt.Errorf("create security group: %w", err)
	}
	groupID := aws.ToString(createOut.GroupId)

	ingress := []types.IpPermission{
		{
			IpProtocol: aws.String("-1"),
			IpRanges:   []types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		},
	}
	_, _ = client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String(groupID),
		IpPermissions: ingress,
	})
	_, _ = client.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId:       aws.String(groupID),
		IpPermissions: ingress,
	})

	c.mu.Lock()
	c.securityGroups[region] = groupID
	c.mu.Unlock()
	return groupID, nil
}

func (c *AWSClient) ensureDefaultSubnet(ctx context.Context, client *ec2.Client, region string) (string, error) {
	c.mu.Lock()
	if id, ok := c.subnets[region]; ok {
		c.mu.Unlock()
		return id, nil
	}
	c.mu.Unlock()

	vpcID, err := defaultVPC(ctx, client)
	if err != nil {
		return "", err
	}

	resp, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("default-for-az"), Values: []string{"true"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe subnets: %w", err)
	}
	if len(resp.Subnets) == 0 {
		return "", fmt.Errorf("no default subnets found in %s", region)
	}
	choice := resp.Subnets[rand.Intn(len(resp.Subnets))]
	subnetID := aws.ToString(choice.SubnetId)

	c.mu.Lock()
	c.subnets[region] = subnetID
	c.mu.Unlock()
	return subnetID, nil
}

func (c *AWSClient) ensureUbuntuAMI(ctx context.Context, client *ec2.Client, region string) (string, error) {
	c.mu.Lock()
	if id, ok := c.amiIDs[region]; ok {
		c.mu.Unlock()
		return id, nil
	}
	c.mu.Unlock()

	resp, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"099720109477"}, // Canonical
		Filters: []types.Filter{
			{Name: aws.String("name"), Values: []string{"ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"}},
			{Name: aws.String("architecture"), Values: []string{"x86_64"}},
			{Name: aws.String("root-device-type"), Values: []string{"ebs"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe images: %w", err)
	}
	if len(resp.Images) == 0 {
		return "", fmt.Errorf("no ubuntu images found in %s", region)
	}
	sort.Slice(resp.Images, func(i, j int) bool {
		return aws.ToString(resp.Images[i].CreationDate) > aws.ToString(resp.Images[j].CreationDate)
	})
	imageID := aws.ToString(resp.Images[0].ImageId)

	c.mu.Lock()
	c.amiIDs[region] = imageID
	c.mu.Unlock()
	return imageID, nil
}

func defaultVPC(ctx context.Context, client *ec2.Client) (string, error) {
	resp, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []types.Filter{{Name: aws.String("isDefault"), Values: []string{"true"}}},
	})
	if err != nil {
		return "", fmt.Errorf("describe vpcs: %w", err)
	}
	if len(resp.Vpcs) == 0 {
		return "", errors.New("no default VPC found for AWS account")
	}
	return aws.ToString(resp.Vpcs[0].VpcId), nil
}

func awsTagsForInstance(inst Instance) []types.Tag {
	var tags []types.Tag
	tags = append(tags, types.Tag{Key: aws.String("Name"), Value: aws.String(inst.Name)})
	seen := make(map[string]struct{})
	for _, tag := range inst.Tags {
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, types.Tag{Key: aws.String(tag), Value: aws.String("true")})
	}
	return tags
}

func awsTagValue(tags []types.Tag, key string) string {
	for _, tag := range tags {
		if tag.Key != nil && *tag.Key == key && tag.Value != nil {
			return *tag.Value
		}
	}
	return ""
}

func formatLaunchTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func describeAWSRegions(ctx context.Context, client *ec2.Client) ([]string, error) {
	resp, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, err
	}
	var regions []string
	for _, r := range resp.Regions {
		if r.RegionName == nil {
			continue
		}
		status := aws.ToString(r.OptInStatus)
		if status != "not-opted-in" {
			regions = append(regions, *r.RegionName)
		}
	}
	sort.Strings(regions)
	return regions, nil
}

func listTalisInstances(ctx context.Context, client *ec2.Client) ([]types.Instance, error) {
	filters := []types.Filter{
		{Name: aws.String("tag-key"), Values: []string{"talis"}},
		{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
	}
	input := &ec2.DescribeInstancesInput{Filters: filters}
	paginator := ec2.NewDescribeInstancesPaginator(client, input)
	var instances []types.Instance
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, res := range page.Reservations {
			instances = append(instances, res.Instances...)
		}
	}
	return instances, nil
}

func findAWSMachineTypeLocations(ctx context.Context, baseCfg aws.Config, machineType string) ([]MachineTypeLocation, error) {
	client := ec2.NewFromConfig(baseCfg)
	regions, err := describeAWSRegions(ctx, client)
	if err != nil {
		return nil, err
	}

	var locations []MachineTypeLocation
	for _, region := range regions {
		cfg := baseCfg
		cfg.Region = region
		regional := ec2.NewFromConfig(cfg)
		paginator := ec2.NewDescribeInstanceTypeOfferingsPaginator(regional, &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: types.LocationTypeAvailabilityZone,
			Filters:      []types.Filter{{Name: aws.String("instance-type"), Values: []string{machineType}}},
		})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("describe instance offerings in %s: %w", region, err)
			}
			for _, off := range page.InstanceTypeOfferings {
				loc := aws.ToString(off.Location)
				if loc == "" {
					continue
				}
				locations = append(locations, MachineTypeLocation{Region: region, Zone: loc})
			}
		}
	}

	sort.Slice(locations, func(i, j int) bool {
		if locations[i].Region != locations[j].Region {
			return locations[i].Region < locations[j].Region
		}
		return locations[i].Zone < locations[j].Zone
	})

	return locations, nil
}

func RandomAWSRegion() string {
	return AWSPreferredRegions[rand.Intn(len(AWSPreferredRegions))]
}

func NewAWSValidator(region string) Instance {
	if region == "" || region == RandomRegion {
		region = RandomAWSRegion()
	}
	i := NewBaseInstance(Validator)
	i.Provider = AWS
	i.Slug = AWSDefaultInstanceType
	i.Region = region
	return i
}

func buildAWSConfig(cfg *Config) (aws.Config, error) {
	region := cfg.AWSDefaultRegion
	if region == "" {
		region = RandomAWSRegion()
	}
	loadOpts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if cfg.AWSAccessKeyID != "" && cfg.AWSSecretAccessKey != "" {
		loadOpts = append(loadOpts, config.WithCredentialsProvider(
			aws.NewCredentialsCache(
				credentials.NewStaticCredentialsProvider(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, ""),
			),
		))
	}
	awsCfg, err := config.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		return aws.Config{}, err
	}
	if awsCfg.Region == "" {
		awsCfg.Region = region
	}
	return awsCfg, nil
}

func checkForRunningAWSExperiments(ctx context.Context, cfg Config, experimentID, chainID string) (bool, error) {
	awsCfg, err := buildAWSConfig(&cfg)
	if err != nil {
		if cfg.AWSAccessKeyID == "" && cfg.AWSSecretAccessKey == "" && cfg.AWSDefaultRegion == "" {
			return false, nil
		}
		return false, err
	}
	client := ec2.NewFromConfig(awsCfg)
	regions, err := describeAWSRegions(ctx, client)
	if err != nil {
		return false, err
	}

	for _, region := range regions {
		regionCfg := awsCfg
		regionCfg.Region = region
		regional := ec2.NewFromConfig(regionCfg)
		instances, err := listTalisInstances(ctx, regional)
		if err != nil {
			return false, err
		}
		for _, inst := range instances {
			tags := collectAWSTagKeys(inst.Tags)
			if hasExperimentTag(tags, experimentID, chainID) {
				return true, nil
			}
		}
	}
	return false, nil
}

func destroyAllTalisAWSInstances(ctx context.Context, cfg Config, workers int) ([]Instance, error) {
	awsCfg, err := buildAWSConfig(&cfg)
	if err != nil {
		return nil, err
	}
	client := ec2.NewFromConfig(awsCfg)
	regions, err := describeAWSRegions(ctx, client)
	if err != nil {
		return nil, err
	}

	type target struct {
		region string
		id     string
		name   string
	}

	var targets []target
	for _, region := range regions {
		regionCfg := awsCfg
		regionCfg.Region = region
		regional := ec2.NewFromConfig(regionCfg)
		instances, err := listTalisInstances(ctx, regional)
		if err != nil {
			return nil, err
		}
		for _, inst := range instances {
			if inst.InstanceId == nil {
				continue
			}
			state := inst.State
			if state != nil && state.Name == types.InstanceStateNameTerminated {
				continue
			}
			targets = append(targets, target{region: region, id: *inst.InstanceId, name: awsTagValue(inst.Tags, "Name")})
		}
	}

	if len(targets) == 0 {
		return nil, nil
	}

	type result struct {
		inst Instance
		err  error
	}
	results := make(chan result, len(targets))
	workerChan := make(chan struct{}, workers)
	var wg sync.WaitGroup
	wg.Add(len(targets))

	for _, t := range targets {
		t := t
		go func() {
			workerChan <- struct{}{}
			defer func() {
				<-workerChan
				wg.Done()
			}()
			regionCfg := awsCfg
			regionCfg.Region = t.region
			regional := ec2.NewFromConfig(regionCfg)
			_, err := regional.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{t.id}})
			results <- result{inst: Instance{Name: t.name, Region: t.region, PublicIP: ""}, err: err}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var destroyed []Instance
	for res := range results {
		if res.err != nil {
			return destroyed, res.err
		}
		destroyed = append(destroyed, res.inst)
	}
	return destroyed, nil
}

func collectAWSTagKeys(tags []types.Tag) []string {
	var out []string
	for _, tag := range tags {
		if tag.Key == nil || *tag.Key == "Name" {
			continue
		}
		out = append(out, *tag.Key)
	}
	return out
}
