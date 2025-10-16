package valaddr

import (
	"fmt"

	"github.com/celestiaorg/celestia-app/v6/x/valaddr/keeper"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesisState returns the default genesis state for the valaddr module
func DefaultGenesisState() *types.GenesisState {
	return &types.GenesisState{
		Params: types.DefaultParams(),
	}
}

// ValidateGenesis validates the genesis state
func ValidateGenesis(data *types.GenesisState) error {
	if data == nil {
		return fmt.Errorf("genesis state cannot be nil")
	}

	if err := data.Params.Validate(); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}

	return nil
}

// InitGenesis initializes the module's state from a genesis state
func InitGenesis(ctx sdk.Context, k keeper.Keeper, data *types.GenesisState) {
	if err := k.SetParams(ctx, data.Params); err != nil {
		panic(fmt.Sprintf("failed to set params: %v", err))
	}
}

// ExportGenesis exports the module's state to a genesis state
func ExportGenesis(ctx sdk.Context, k keeper.Keeper) *types.GenesisState {
	params, err := k.GetParams(ctx)
	if err != nil {
		panic(fmt.Sprintf("failed to get params: %v", err))
	}

	return &types.GenesisState{
		Params: params,
	}
}
