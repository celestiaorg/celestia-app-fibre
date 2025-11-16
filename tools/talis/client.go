package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/digitalocean/godo"
	"golang.org/x/oauth2"
)

// Environment Variables Used by Talis Client:
//
// SSH Configuration:
// - TALIS_SSH_KEY_NAME: Name of the SSH key registered with the cloud provider
// - TALIS_SSH_PUB_KEY_PATH: Path to the SSH public key file
// - TALIS_SSH_KEY_PATH: Path to the SSH private key file (used for connecting to instances)
//
// DigitalOcean Configuration:
// - DIGITALOCEAN_TOKEN: DigitalOcean API token (required for DigitalOcean operations)
//
// Google Cloud Configuration:
// - GOOGLE_CLOUD_PROJECT: Google Cloud project ID (required for Google Cloud operations)
// - GOOGLE_CLOUD_KEY_JSON_PATH: Path to Google Cloud service account key JSON file
//
// AWS S3 Configuration (for storing trace data):
// - AWS_ACCESS_KEY_ID: AWS access key ID (or compatible S3 service)
// - AWS_SECRET_ACCESS_KEY: AWS secret access key (or compatible S3 service)
// - AWS_DEFAULT_REGION: AWS region or S3-compatible service region
// - AWS_S3_BUCKET: S3 bucket name for storing trace data
// - AWS_S3_ENDPOINT: S3 endpoint URL (optional, for non-AWS S3-compatible services like DigitalOcean Spaces)

type Client interface {
	Up(ctx context.Context, workers int) error
	Bump(ctx context.Context, workers int) error
	Down(ctx context.Context, workers int) error
	List(ctx context.Context) error
	FindMachineType(ctx context.Context, provider Provider, machineType string, prefix bool) ([]MachineTypeLocation, error)
	GetConfig() Config
}

type ClientInfo struct {
	sshKey []byte
	cfg    *Config
}

type DOClient struct {
	ClientInfo
	do       *godo.Client
	doSSHKey godo.Key
}

func NewClient(cfg Config) (Client, error) {
	return newClientWithFilter(cfg, nil)
}

func NewClientForProviders(cfg Config, providers ...Provider) (Client, error) {
	allowed := make(map[Provider]bool)
	for _, p := range providers {
		if p == "" {
			continue
		}
		allowed[p] = true
	}
	return newClientWithFilter(cfg, allowed)
}

func newClientWithFilter(cfg Config, allowed map[Provider]bool) (Client, error) {
	cfgPtr := &cfg
	providers := providersInConfig(cfg)

	clients := make(map[Provider]Client)
	var order []Provider

	include := func(p Provider) bool {
		if allowed == nil {
			return true
		}
		return allowed[p]
	}

	if include(DigitalOcean) && (providers[DigitalOcean] || cfg.DigitalOceanToken != "" || allowed != nil && allowed[DigitalOcean]) {
		if cfg.DigitalOceanToken == "" {
			return nil, errors.New("digital ocean instances found but no DigitalOceanToken configured")
		}
		doClient, err := NewDOClient(cfgPtr)
		if err != nil {
			return nil, err
		}
		clients[DigitalOcean] = doClient
		order = append(order, DigitalOcean)
	}

	if include(GoogleCloud) && (providers[GoogleCloud] || cfg.GoogleCloudProject != "" || allowed != nil && allowed[GoogleCloud]) {
		if cfg.GoogleCloudProject == "" {
			return nil, errors.New("google cloud instances found but no GoogleCloudProject configured")
		}
		gcClient, err := NewGCClient(cfgPtr)
		if err != nil {
			return nil, err
		}
		clients[GoogleCloud] = gcClient
		order = append(order, GoogleCloud)
	}

	if include(AWS) && (providers[AWS] || allowed != nil && allowed[AWS]) {
		awsClient, err := NewAWSClient(cfgPtr)
		if err != nil {
			return nil, err
		}
		clients[AWS] = awsClient
		order = append(order, AWS)
	}

	if len(clients) == 0 {
		return nil, errors.New("no cloud provider credentials found")
	}

	sort.SliceStable(order, func(i, j int) bool {
		return order[i] < order[j]
	})

	return &MultiClient{
		cfg:     cfgPtr,
		clients: clients,
		order:   order,
	}, nil
}

func NewDOClient(cfg *Config) (*DOClient, error) {
	if cfg.DigitalOceanToken == "" {
		return nil, errors.New("DigitalOcean token is required")
	}

	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cfg.DigitalOceanToken})
	client := godo.NewClient(oauth2.NewClient(context.Background(), tokenSource))

	if client == nil {
		return nil, errors.New("failed to create DigitalOcean client")
	}

	sshKey, err := os.ReadFile(cfg.SSHPubKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH public key at: %s %w", cfg.SSHPubKeyPath, err)
	}

	key, err := GetDOSSHKeyMeta(context.Background(), client, string(sshKey))
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH key ID: %w", err)
	}

	return &DOClient{
		ClientInfo: ClientInfo{
			sshKey: sshKey,
			cfg:    cfg,
		},
		do:       client,
		doSSHKey: key,
	}, nil
}

func (c *DOClient) Up(ctx context.Context, workers int) error {
	insts := c.selectDOValidators(nil)
	if len(insts) == 0 {
		return fmt.Errorf("no instances to create")
	}

	return c.provisionDroplets(ctx, insts, workers)
}

func (c *DOClient) Bump(ctx context.Context, workers int) error {
	insts := c.selectDOValidators(func(inst Instance) bool {
		return inst.NeedsProvision()
	})
	if len(insts) == 0 {
		log.Println("No pending DigitalOcean instances to bump")
		return nil
	}

	return c.provisionDroplets(ctx, insts, workers)
}

func (c *DOClient) Down(ctx context.Context, workers int) error {
	insts := c.selectDOValidators(nil)
	if len(insts) == 0 {
		return fmt.Errorf("no instances to destroy")
	}

	_, err := DestroyDroplets(ctx, c.do, insts, workers)
	return err
}

func (c *DOClient) List(ctx context.Context) error {
	opts := &godo.ListOptions{}
	cnt := 0
	for {
		droplets, resp, err := c.do.Droplets.List(ctx, opts)
		if err != nil {
			return fmt.Errorf("failed to list droplets: %w", err)
		}

		for _, droplet := range droplets {
			if hasAllTags(droplet.Tags, []string{"talis"}) {
				publicIP := ""
				privateIP := ""
				if len(droplet.Networks.V4) > 0 {
					for _, network := range droplet.Networks.V4 {
						if network.Type == "public" && publicIP == "" {
							publicIP = network.IPAddress
						}
						if network.Type == "private" && privateIP == "" {
							privateIP = network.IPAddress
						}
					}
				}

				if cnt == 0 {
					fmt.Printf("%-30s %-10s %-15s %-15s %s\n", "Name", "Status", "Region", "Public IP", "Created")
					fmt.Printf("%-30s %-10s %-15s %-15s %s\n", "----", "------", "------", "---------", "-------")
				}

				fmt.Printf("%-30s %-10s %-15s %-15s %s\n",
					droplet.Name,
					droplet.Status,
					droplet.Region.Slug,
					publicIP,
					droplet.Created)
				cnt++
			}
		}

		if resp.Links == nil || resp.Links.IsLastPage() {
			break
		}
		page, err := resp.Links.CurrentPage()
		if err != nil {
			return fmt.Errorf("failed to paginate droplets list: %w", err)
		}

		opts.Page = page + 1
	}

	fmt.Println("Total number of talis instances:", cnt)
	return nil
}

func (c *DOClient) GetConfig() Config {
	return *c.cfg
}

func (c *DOClient) FindMachineType(ctx context.Context, provider Provider, machineType string, prefix bool) ([]MachineTypeLocation, error) {
	if provider != "" && provider != DigitalOcean {
		return nil, fmt.Errorf("digitalocean client cannot query provider %s", provider)
	}
	searchType := strings.ToLower(machineType)
	return findDigitalOceanRegions(ctx, c.do, searchType, prefix)
}

func (c *DOClient) selectDOValidators(filter func(Instance) bool) []Instance {
	insts := make([]Instance, 0, len(c.cfg.Validators))
	for _, v := range c.cfg.Validators {
		if v.Provider != DigitalOcean {
			continue
		}
		if v.Region == "" || v.Region == RandomRegion {
			v.Region = RandomDORegion()
		}
		if filter != nil && !filter(v) {
			continue
		}
		insts = append(insts, v)
	}
	return insts
}

func (c *DOClient) provisionDroplets(ctx context.Context, insts []Instance, workers int) error {
	created, err := CreateDroplets(ctx, c.do, insts, c.doSSHKey, workers)
	if err != nil {
		return fmt.Errorf("failed to create droplets: %w", err)
	}

	for _, inst := range created {
		if err := c.cfg.UpdateInstance(inst.Name, inst.PublicIP, inst.PrivateIP); err != nil {
			return fmt.Errorf("failed to update config with instance %s: %w", inst.Name, err)
		}
	}

	return nil
}

type MultiClient struct {
	cfg     *Config
	clients map[Provider]Client
	order   []Provider
}

func (m *MultiClient) Up(ctx context.Context, workers int) error {
	return m.runParallel(ctx, func(ctx context.Context, c Client) error {
		return c.Up(ctx, workers)
	})
}

func (m *MultiClient) Bump(ctx context.Context, workers int) error {
	return m.runParallel(ctx, func(ctx context.Context, c Client) error {
		return c.Bump(ctx, workers)
	})
}

func (m *MultiClient) Down(ctx context.Context, workers int) error {
	return m.runParallel(ctx, func(ctx context.Context, c Client) error {
		return c.Down(ctx, workers)
	})
}

func (m *MultiClient) List(ctx context.Context) error {
	return m.run(func(c Client) error {
		return c.List(ctx)
	})
}

func (m *MultiClient) FindMachineType(ctx context.Context, provider Provider, machineType string, prefix bool) ([]MachineTypeLocation, error) {
	target := provider
	if target == "" {
		if len(m.order) == 1 {
			target = m.order[0]
		} else {
			return nil, errors.New("provider must be specified when multiple providers are configured")
		}
	}

	client, ok := m.clients[target]
	if !ok {
		return nil, fmt.Errorf("provider %s is not configured", target)
	}
	return client.FindMachineType(ctx, target, machineType, prefix)
}

func (m *MultiClient) GetConfig() Config {
	return *m.cfg
}

func (m *MultiClient) runParallel(ctx context.Context, fn func(context.Context, Client) error) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(m.clients))

	for _, provider := range m.order {
		wg.Add(1)
		go func(p Provider, client Client) {
			defer wg.Done()
			if err := fn(ctx, client); err != nil {
				errCh <- fmt.Errorf("%s: %w", p, err)
			}
		}(provider, m.clients[provider])
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (m *MultiClient) run(fn func(Client) error) error {
	var errs []error
	for _, provider := range m.order {
		if err := fn(m.clients[provider]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", provider, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func providersInConfig(cfg Config) map[Provider]bool {
	providers := make(map[Provider]bool)
	for _, inst := range cfg.Validators {
		if inst.Provider != "" {
			providers[inst.Provider] = true
		}
	}
	for _, inst := range cfg.Bridges {
		if inst.Provider != "" {
			providers[inst.Provider] = true
		}
	}
	for _, inst := range cfg.Lights {
		if inst.Provider != "" {
			providers[inst.Provider] = true
		}
	}
	return providers
}
