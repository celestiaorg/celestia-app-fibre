package keeper_test

import (
	gocontext "context"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/app"
	testutil "github.com/celestiaorg/celestia-app/v6/test/util"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestQueryFibreProviderInfo(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)

	queryHelper := baseapp.NewQueryServerTestHelper(ctx, testApp.GetEncodingConfig().InterfaceRegistry)
	types.RegisterQueryServer(queryHelper, testApp.ValAddrKeeper)
	queryClient := types.NewQueryClient(queryHelper)

	consAddr := sdk.ConsAddress("validator1")
	info := types.FibreProviderInfo{
		Host: "validator1.fibre.example.com",
	}

	err := testApp.ValAddrKeeper.SetFibreProviderInfo(ctx, consAddr, info)
	require.NoError(t, err)

	resp, err := queryClient.FibreProviderInfo(gocontext.Background(), &types.QueryFibreProviderInfoRequest{
		ValidatorConsensusAddress: consAddr.String(),
	})
	require.NoError(t, err)
	require.True(t, resp.Found)
	require.Equal(t, info.Host, resp.Info.Host)
}

func TestQueryFibreProviderInfoNotFound(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)

	queryHelper := baseapp.NewQueryServerTestHelper(ctx, testApp.GetEncodingConfig().InterfaceRegistry)
	types.RegisterQueryServer(queryHelper, testApp.ValAddrKeeper)
	queryClient := types.NewQueryClient(queryHelper)

	consAddr := sdk.ConsAddress("nonexistent")

	resp, err := queryClient.FibreProviderInfo(gocontext.Background(), &types.QueryFibreProviderInfoRequest{
		ValidatorConsensusAddress: consAddr.String(),
	})
	require.NoError(t, err)
	require.False(t, resp.Found)
}

func TestQueryAllActiveFibreProviders(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)

	queryHelper := baseapp.NewQueryServerTestHelper(ctx, testApp.GetEncodingConfig().InterfaceRegistry)
	types.RegisterQueryServer(queryHelper, testApp.ValAddrKeeper)
	queryClient := types.NewQueryClient(queryHelper)

	resp, err := queryClient.AllActiveFibreProviders(gocontext.Background(), &types.QueryAllActiveFibreProvidersRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
}
