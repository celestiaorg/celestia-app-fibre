package grpc_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	core "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpc2 "google.golang.org/grpc"
)

// mockQueryClient is a mock implementation of types.QueryClient for testing
type mockQueryClient struct {
	fibreProviderInfoFn func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error)
	allFibreProvidersFn func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error)
}

func (m *mockQueryClient) FibreProviderInfo(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
	if m.fibreProviderInfoFn != nil {
		return m.fibreProviderInfoFn(ctx, in, opts...)
	}
	return nil, errors.New("mock function not implemented")
}

func (m *mockQueryClient) AllFibreProviders(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
	if m.allFibreProvidersFn != nil {
		return m.allFibreProvidersFn(ctx, in, opts...)
	}
	return nil, errors.New("mock function not implemented")
}

// Helper function to create a test validator
func createTestValidator(address []byte) *core.Validator {
	if address == nil {
		address = []byte("test_validator_addr1")
	}
	return &core.Validator{
		Address:     address,
		VotingPower: 100,
	}
}

// Helper function to get consensus address string
func getConsAddrString(val *core.Validator) string {
	return sdk.ConsAddress(val.Address.Bytes()).String()
}

func TestNewHostRegistry(t *testing.T) {
	mockClient := &mockQueryClient{}
	registry := grpc.NewHostRegistry(mockClient)
	require.NotNil(t, registry)
}

func TestGetHost_WithEmptyCache_PullsAll_Success(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	consAddr := getConsAddrString(val)
	expectedHost := "validator1.example.com:9090"

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr,
						Info: types.FibreProviderInfo{
							Host: expectedHost,
						},
					},
				},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.GetHost(ctx, val)

	require.NoError(t, err)
	assert.Equal(t, expectedHost, host.String())
}

func TestGetHost_WithEmptyCache_PullsAll_ValidatorNotFound(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	differentConsAddr := "celestiavalcons1different"

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			// Return providers but not for the requested validator
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: differentConsAddr,
						Info: types.FibreProviderInfo{
							Host: "other.example.com:9090",
						},
					},
				},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.GetHost(ctx, val)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host not found for validator")
	assert.Empty(t, host.String())
}

func TestGetHost_WithEmptyCache_PullAllFails(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	expectedErr := errors.New("network error")

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			return nil, expectedErr
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.GetHost(ctx, val)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, host.String())
}

func TestGetHost_WithPopulatedCache_UsesCache(t *testing.T) {
	ctx := context.Background()
	val1 := createTestValidator([]byte("validator1"))
	val2 := createTestValidator([]byte("validator2"))
	consAddr1 := getConsAddrString(val1)
	consAddr2 := getConsAddrString(val2)
	expectedHost1 := "validator1.example.com:9090"
	expectedHost2 := "validator2.example.com:9090"

	allFibreProvidersCalls := 0
	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			allFibreProvidersCalls++
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr1,
						Info:                      types.FibreProviderInfo{Host: expectedHost1},
					},
					{
						ValidatorConsensusAddress: consAddr2,
						Info:                      types.FibreProviderInfo{Host: expectedHost2},
					},
				},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// First call - should populate cache
	host1, err := registry.GetHost(ctx, val1)
	require.NoError(t, err)
	assert.Equal(t, expectedHost1, host1.String())
	assert.Equal(t, 1, allFibreProvidersCalls)

	// Second call for same validator - should use cache
	host1Again, err := registry.GetHost(ctx, val1)
	require.NoError(t, err)
	assert.Equal(t, expectedHost1, host1Again.String())
	assert.Equal(t, 1, allFibreProvidersCalls, "should not call AllFibreProviders again")

	// Third call for different validator - should use cache
	host2, err := registry.GetHost(ctx, val2)
	require.NoError(t, err)
	assert.Equal(t, expectedHost2, host2.String())
	assert.Equal(t, 1, allFibreProvidersCalls, "should not call AllFibreProviders again")
}

func TestGetHost_ValidatorNotInCache_PullsHost_Success(t *testing.T) {
	ctx := context.Background()
	val1 := createTestValidator([]byte("validator1"))
	val2 := createTestValidator([]byte("validator2"))
	consAddr1 := getConsAddrString(val1)
	consAddr2 := getConsAddrString(val2)
	expectedHost1 := "validator1.example.com:9090"
	expectedHost2 := "validator2.example.com:9090"

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			// Only return validator1
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr1,
						Info:                      types.FibreProviderInfo{Host: expectedHost1},
					},
				},
			}, nil
		},
		fibreProviderInfoFn: func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
			// Return validator2 info when specifically queried
			if in.ValidatorConsensusAddress == consAddr2 {
				return &types.QueryFibreProviderInfoResponse{
					Info:  &types.FibreProviderInfo{Host: expectedHost2},
					Found: true,
				}, nil
			}
			return &types.QueryFibreProviderInfoResponse{Found: false}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// First call - populates cache with val1
	host1, err := registry.GetHost(ctx, val1)
	require.NoError(t, err)
	assert.Equal(t, expectedHost1, host1.String())

	// Second call for val2 - not in cache, should call PullHost
	host2, err := registry.GetHost(ctx, val2)
	require.NoError(t, err)
	assert.Equal(t, expectedHost2, host2.String())
}

func TestGetHost_ValidatorNotInCache_PullHostNotFound(t *testing.T) {
	ctx := context.Background()
	val1 := createTestValidator([]byte("validator1"))
	val2 := createTestValidator([]byte("validator2"))
	consAddr1 := getConsAddrString(val1)
	expectedHost1 := "validator1.example.com:9090"

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			// Only return validator1
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr1,
						Info:                      types.FibreProviderInfo{Host: expectedHost1},
					},
				},
			}, nil
		},
		fibreProviderInfoFn: func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
			return &types.QueryFibreProviderInfoResponse{Found: false}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// First call - populates cache
	_, err := registry.GetHost(ctx, val1)
	require.NoError(t, err)

	// Second call for val2 - not in cache, PullHost returns not found
	host2, err := registry.GetHost(ctx, val2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host not found for validator")
	assert.Empty(t, host2.String())
}

func TestGetHost_ValidatorNotInCache_PullHostFails(t *testing.T) {
	ctx := context.Background()
	val1 := createTestValidator([]byte("validator1"))
	val2 := createTestValidator([]byte("validator2"))
	consAddr1 := getConsAddrString(val1)
	expectedHost1 := "validator1.example.com:9090"
	expectedErr := errors.New("network error")

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			// Only return validator1
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr1,
						Info:                      types.FibreProviderInfo{Host: expectedHost1},
					},
				},
			}, nil
		},
		fibreProviderInfoFn: func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
			return nil, expectedErr
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// First call - populates cache
	_, err := registry.GetHost(ctx, val1)
	require.NoError(t, err)

	// Second call for val2 - not in cache, PullHost returns error
	host2, err := registry.GetHost(ctx, val2)
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, host2.String())
}

func TestGetHost_InvalidURL(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	consAddr := getConsAddrString(val)
	invalidHost := "ht!tp://invalid url with spaces"

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr,
						Info:                      types.FibreProviderInfo{Host: invalidHost},
					},
				},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.GetHost(ctx, val)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "got invalid host")
	assert.Empty(t, host.String())
}

func TestPullAll_Success(t *testing.T) {
	ctx := context.Background()
	val1 := createTestValidator([]byte("validator1"))
	val2 := createTestValidator([]byte("validator2"))
	consAddr1 := getConsAddrString(val1)
	consAddr2 := getConsAddrString(val2)
	expectedHost1 := "validator1.example.com:9090"
	expectedHost2 := "validator2.example.com:9090"

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr1,
						Info:                      types.FibreProviderInfo{Host: expectedHost1},
					},
					{
						ValidatorConsensusAddress: consAddr2,
						Info:                      types.FibreProviderInfo{Host: expectedHost2},
					},
				},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	err := registry.PullAll(ctx)
	require.NoError(t, err)

	// Verify both hosts are cached
	host1, err := registry.GetHost(ctx, val1)
	require.NoError(t, err)
	assert.Equal(t, expectedHost1, host1.String())

	host2, err := registry.GetHost(ctx, val2)
	require.NoError(t, err)
	assert.Equal(t, expectedHost2, host2.String())
}

func TestPullAll_Error(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("grpc error")

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			return nil, expectedErr
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	err := registry.PullAll(ctx)
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

func TestPullAll_OverwritesExistingCache(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	consAddr := getConsAddrString(val)
	firstHost := "validator1.example.com:9090"
	secondHost := "validator1.example.com:9091"

	callCount := 0
	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			callCount++
			host := firstHost
			if callCount > 1 {
				host = secondHost
			}
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr,
						Info:                      types.FibreProviderInfo{Host: host},
					},
				},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// First PullAll
	err := registry.PullAll(ctx)
	require.NoError(t, err)

	host1, err := registry.GetHost(ctx, val)
	require.NoError(t, err)
	assert.Equal(t, firstHost, host1.String())

	// Second PullAll - should overwrite
	err = registry.PullAll(ctx)
	require.NoError(t, err)

	host2, err := registry.GetHost(ctx, val)
	require.NoError(t, err)
	assert.Equal(t, secondHost, host2.String())
}

func TestPullHost_Success(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	consAddr := getConsAddrString(val)
	expectedHost := "validator1.example.com:9090"

	mockClient := &mockQueryClient{
		fibreProviderInfoFn: func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
			assert.Equal(t, consAddr, in.ValidatorConsensusAddress)
			return &types.QueryFibreProviderInfoResponse{
				Info:  &types.FibreProviderInfo{Host: expectedHost},
				Found: true,
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.PullHost(ctx, val)

	require.NoError(t, err)
	assert.Equal(t, expectedHost, host.String())
}

func TestPullHost_NotFound(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)

	mockClient := &mockQueryClient{
		fibreProviderInfoFn: func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
			return &types.QueryFibreProviderInfoResponse{Found: false}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.PullHost(ctx, val)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host not found for validator")
	assert.Empty(t, host.String())
}

func TestPullHost_Error(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	expectedErr := errors.New("grpc error")

	mockClient := &mockQueryClient{
		fibreProviderInfoFn: func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
			return nil, expectedErr
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.PullHost(ctx, val)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, host.String())
}

func TestPullHost_OverwritesCache(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	consAddr := getConsAddrString(val)
	firstHost := "validator1.example.com:9090"
	secondHost := "validator1.example.com:9091"

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr,
						Info:                      types.FibreProviderInfo{Host: firstHost},
					},
				},
			}, nil
		},
		fibreProviderInfoFn: func(ctx context.Context, in *types.QueryFibreProviderInfoRequest, opts ...grpc2.CallOption) (*types.QueryFibreProviderInfoResponse, error) {
			return &types.QueryFibreProviderInfoResponse{
				Info:  &types.FibreProviderInfo{Host: secondHost},
				Found: true,
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// Populate cache with first host
	host1, err := registry.GetHost(ctx, val)
	require.NoError(t, err)
	assert.Equal(t, firstHost, host1.String())

	// PullHost should overwrite cache
	host2, err := registry.PullHost(ctx, val)
	require.NoError(t, err)
	assert.Equal(t, secondHost, host2.String())

	// GetHost should now return the new host from cache
	host3, err := registry.GetHost(ctx, val)
	require.NoError(t, err)
	assert.Equal(t, secondHost, host3.String())
}

func TestHostRegistry_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)
	consAddr := getConsAddrString(val)
	expectedHost := "validator1.example.com:9090"

	var allProvidersCalls int
	var mu sync.Mutex
	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			mu.Lock()
			allProvidersCalls++
			mu.Unlock()
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{
					{
						ValidatorConsensusAddress: consAddr,
						Info:                      types.FibreProviderInfo{Host: expectedHost},
					},
				},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// Launch multiple goroutines trying to get the same host
	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errChan := make(chan error, numGoroutines)
	hostChan := make(chan validator.Host, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			host, err := registry.GetHost(ctx, val)
			if err != nil {
				errChan <- err
				return
			}
			hostChan <- host
		}()
	}

	wg.Wait()
	close(errChan)
	close(hostChan)

	// Check for errors
	for err := range errChan {
		require.NoError(t, err)
	}

	// Verify all goroutines got the correct host
	for host := range hostChan {
		assert.Equal(t, expectedHost, host.String())
	}

	// AllFibreProviders should only be called once (the first goroutine)
	// Other goroutines should use the cache
	mu.Lock()
	defer mu.Unlock()
	assert.LessOrEqual(t, allProvidersCalls, 5, "AllFibreProviders should be called only a few times even with concurrent access")
}

func TestHostRegistry_ImplementsInterface(t *testing.T) {
	mockClient := &mockQueryClient{}
	var _ validator.HostRegistry = grpc.NewHostRegistry(mockClient)
}

func TestGetHost_MultipleValidators(t *testing.T) {
	ctx := context.Background()

	// Create multiple validators
	vals := make([]*core.Validator, 5)
	consAddrs := make([]string, 5)
	expectedHosts := make([]string, 5)

	for i := 0; i < 5; i++ {
		vals[i] = createTestValidator([]byte(fmt.Sprintf("validator%d", i)))
		consAddrs[i] = getConsAddrString(vals[i])
		expectedHosts[i] = fmt.Sprintf("validator%d.example.com:909%d", i, i)
	}

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			providers := make([]types.FibreProvider, 5)
			for i := 0; i < 5; i++ {
				providers[i] = types.FibreProvider{
					ValidatorConsensusAddress: consAddrs[i],
					Info:                      types.FibreProviderInfo{Host: expectedHosts[i]},
				}
			}
			return &types.QueryAllFibreProvidersResponse{Providers: providers}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)

	// Get hosts for all validators
	for i := 0; i < 5; i++ {
		host, err := registry.GetHost(ctx, vals[i])
		require.NoError(t, err)
		assert.Equal(t, expectedHosts[i], host.String())
	}
}

func TestGetHost_EmptyResponse(t *testing.T) {
	ctx := context.Background()
	val := createTestValidator(nil)

	mockClient := &mockQueryClient{
		allFibreProvidersFn: func(ctx context.Context, in *types.QueryAllFibreProvidersRequest, opts ...grpc2.CallOption) (*types.QueryAllFibreProvidersResponse, error) {
			// Return empty providers list
			return &types.QueryAllFibreProvidersResponse{
				Providers: []types.FibreProvider{},
			}, nil
		},
	}

	registry := grpc.NewHostRegistry(mockClient)
	host, err := registry.GetHost(ctx, val)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host not found for validator")
	assert.Empty(t, host.String())
}
