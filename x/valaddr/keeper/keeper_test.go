package keeper_test

import (
	"testing"

	"github.com/celestiaorg/celestia-app/v6/app"
	testutil "github.com/celestiaorg/celestia-app/v6/test/util"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestSetGetFibreProviderInfo(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	consAddr := sdk.ConsAddress("validator1")
	info := types.FibreProviderInfo{
		IpAddress: "192.168.1.1",
	}

	err := keeper.SetFibreProviderInfo(ctx, consAddr, info)
	require.NoError(t, err)

	retrieved, found := keeper.GetFibreProviderInfo(ctx, consAddr)
	require.True(t, found)
	require.Equal(t, info.IpAddress, retrieved.IpAddress)
}

func TestGetFibreProviderInfoNotFound(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	consAddr := sdk.ConsAddress("nonexistent")

	_, found := keeper.GetFibreProviderInfo(ctx, consAddr)
	require.False(t, found)
}

func TestDeleteFibreProviderInfo(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	consAddr := sdk.ConsAddress("validator1")
	info := types.FibreProviderInfo{
		IpAddress: "192.168.1.1",
	}

	err := keeper.SetFibreProviderInfo(ctx, consAddr, info)
	require.NoError(t, err)

	err = keeper.DeleteFibreProviderInfo(ctx, consAddr)
	require.NoError(t, err)

	_, found := keeper.GetFibreProviderInfo(ctx, consAddr)
	require.False(t, found)
}

func TestIterateFibreProviderInfo(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	providers := []struct {
		consAddr sdk.ConsAddress
		info     types.FibreProviderInfo
	}{
		{sdk.ConsAddress("validator1"), types.FibreProviderInfo{IpAddress: "192.168.1.1"}},
		{sdk.ConsAddress("validator2"), types.FibreProviderInfo{IpAddress: "192.168.1.2"}},
		{sdk.ConsAddress("validator3"), types.FibreProviderInfo{IpAddress: "192.168.1.3"}},
	}

	for _, p := range providers {
		err := keeper.SetFibreProviderInfo(ctx, p.consAddr, p.info)
		require.NoError(t, err)
	}

	count := 0
	err := keeper.IterateFibreProviderInfo(ctx, func(_ sdk.ConsAddress, _ types.FibreProviderInfo) bool {
		count++
		return false
	})
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

func TestSetGetParams(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	params := types.Params{
		MissingInfoCheckHeight: 12345,
	}

	err := keeper.SetParams(ctx, params)
	require.NoError(t, err)

	retrieved, err := keeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, params.MissingInfoCheckHeight, retrieved.MissingInfoCheckHeight)
}

func TestSetParamsInvalid(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	tests := []struct {
		name   string
		params types.Params
	}{
		{
			name: "negative height",
			params: types.Params{
				MissingInfoCheckHeight: -1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := keeper.SetParams(ctx, tc.params)
			require.Error(t, err)
			require.Contains(t, err.Error(), "missing_info_check_height must be non-negative")
		})
	}
}
