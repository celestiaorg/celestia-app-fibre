package keeper_test

import (
	"testing"

	"github.com/celestiaorg/celestia-app/v6/app"
	testutil "github.com/celestiaorg/celestia-app/v6/test/util"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	"github.com/stretchr/testify/require"
)

func TestInitGenesis(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	genesisState := &types.GenesisState{
		Params: types.Params{
			MissingInfoCheckHeight: 50000,
		},
	}

	valaddr.InitGenesis(ctx, keeper, genesisState)

	params, err := keeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(50000), params.MissingInfoCheckHeight)
}

func TestExportGenesis(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)
	keeper := testApp.ValaddrKeeper

	params := types.Params{
		MissingInfoCheckHeight: 75000,
	}

	err := keeper.SetParams(ctx, params)
	require.NoError(t, err)

	exported := valaddr.ExportGenesis(ctx, keeper)

	require.Equal(t, params.MissingInfoCheckHeight, exported.Params.MissingInfoCheckHeight)
}

func TestDefaultGenesis(t *testing.T) {
	genesis := valaddr.DefaultGenesisState()

	require.NotNil(t, genesis)
	require.Equal(t, types.DefaultMissingInfoCheckHeight, genesis.Params.MissingInfoCheckHeight)
}

func TestValidateGenesis(t *testing.T) {
	tests := []struct {
		name      string
		genesis   *types.GenesisState
		expectErr bool
	}{
		{
			name:      "valid genesis",
			genesis:   valaddr.DefaultGenesisState(),
			expectErr: false,
		},
		{
			name: "invalid params",
			genesis: &types.GenesisState{
				Params: types.Params{
					MissingInfoCheckHeight: -1,
				},
			},
			expectErr: true,
		},
		{
			name:      "nil genesis",
			genesis:   nil,
			expectErr: true,
		},
		{
			name: "zero check height - valid sentinel value for not set",
			genesis: &types.GenesisState{
				Params: types.Params{
					MissingInfoCheckHeight: 0,
				},
			},
			expectErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := valaddr.ValidateGenesis(tc.genesis)
			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
