package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

var _ types.QueryServer = Keeper{}

// FibreProviderInfo queries the fibre provider information for a specific validator
func (k Keeper) FibreProviderInfo(goCtx context.Context, req *types.QueryFibreProviderInfoRequest) (*types.QueryFibreProviderInfoResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrIncorrectValidator, "empty request")
	}

	consAddr, err := sdk.ConsAddressFromBech32(req.ValidatorConsensusAddress)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrIncorrectValidator, "invalid consensus address: %v", err)
	}

	info, found := k.GetFibreProviderInfo(goCtx, consAddr)

	return &types.QueryFibreProviderInfoResponse{
		Info:  &info,
		Found: found,
	}, nil
}

// AllActiveFibreProviders queries fibre provider information for all validators in the active set
func (k Keeper) AllActiveFibreProviders(goCtx context.Context, req *types.QueryAllActiveFibreProvidersRequest) (*types.QueryAllActiveFibreProvidersResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrIncorrectValidator, "empty request")
	}

	bondedValidators, err := k.stakingKeeper.GetBondedValidatorsByPower(goCtx)
	if err != nil {
		return nil, errorsmod.Wrap(err, "failed to get bonded validators")
	}

	providers := make([]types.FibreProvider, 0, len(bondedValidators))
	for _, val := range bondedValidators {
		consPubKey, err := val.ConsPubKey()
		if err != nil {
			k.Logger(goCtx).Error("failed to get consensus public key for validator", "error", err)
			continue
		}
		consAddr := sdk.ConsAddress(consPubKey.Address())

		info, found := k.GetFibreProviderInfo(goCtx, consAddr)
		if !found {
			continue
		}

		providers = append(providers, types.FibreProvider{
			ValidatorConsensusAddress: consAddr.String(),
			Info:                      info,
		})
	}

	return &types.QueryAllActiveFibreProvidersResponse{
		Providers: providers,
	}, nil
}
