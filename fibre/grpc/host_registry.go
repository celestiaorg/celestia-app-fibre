package grpc

import (
	"context"
	"fmt"
	"net/url"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	core "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

var _ validator.HostRegistry = &HostRegistry{}

// HostRegistry is a registry of validator hosts. It caches the hosts for validators in the active set.
// It uses the [types.QueryClient] to query the fibre provider information for validators in the active set.
type HostRegistry struct {
	queryClient types.QueryClient
	cachedHosts map[string]validator.Host //
}

func NewHostRegistry(queryClient types.QueryClient) *HostRegistry {
	return &HostRegistry{
		queryClient: queryClient,
		cachedHosts: make(map[string]validator.Host),
	}
}

func (g *HostRegistry) GetHost(ctx context.Context, val *core.Validator) (validator.Host, error) {
	host, err := g.getHost(ctx, val)
	if err != nil {
		return "", err
	}

	// check if the host is a valid URL
	_, err = url.Parse(host.String())
	if err != nil {
		return "", fmt.Errorf("got invalid host %s: %w", host.String(), err)
	}

	return host, nil
}

func (g *HostRegistry) getHost(ctx context.Context, val *core.Validator) (validator.Host, error) {
	valConAddr := sdk.ConsAddress(val.Address.Bytes()).String()
	// check the cache first
	if host, ok := g.cachedHosts[valConAddr]; ok {
		return host, nil
	}

	// if the cache is empty, fetch all active fibre providers
	if len(g.cachedHosts) == 0 {
		if err := g.PullAll(ctx); err != nil {
			return "", err
		}
		if host, ok := g.cachedHosts[valConAddr]; ok {
			return host, nil
		} else {
			return "", fmt.Errorf("host not found for validator %s", valConAddr)
		}
	}

	// look up the specific validator's host if it's missing from the cache. It might have
	// been added to the active set since the last refresh.
	return g.PullHost(ctx, val)
}

// PullAll pulls all active fibre providers from the query client and caches them, overwriting any existing cached hosts.
func (g *HostRegistry) PullAll(ctx context.Context) error {
	resp, err := g.queryClient.AllFibreProviders(ctx, &types.QueryAllFibreProvidersRequest{})
	if err != nil {
		return err
	}
	for _, provider := range resp.Providers {
		g.cachedHosts[provider.ValidatorConsensusAddress] = validator.Host(provider.Info.Host)
	}
	return nil
}

// PullHost pulls the host for a specific validator from the query client and caches it, overwriting any existing cached host.
func (g *HostRegistry) PullHost(ctx context.Context, val *core.Validator) (validator.Host, error) {
	consAddr := sdk.ConsAddress(val.Address.Bytes())
	resp, err := g.queryClient.FibreProviderInfo(ctx, &types.QueryFibreProviderInfoRequest{
		ValidatorConsensusAddress: consAddr.String(),
	})
	if err != nil {
		return "", err
	}
	if !resp.Found {
		return "", fmt.Errorf("host not found for validator %s", consAddr.String())
	}

	g.cachedHosts[val.Address.String()] = validator.Host(resp.Info.Host)
	return validator.Host(resp.Info.Host), nil
}
