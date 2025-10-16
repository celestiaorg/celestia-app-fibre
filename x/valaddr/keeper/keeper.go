package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/core/store"
	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Keeper of the valaddr store
type Keeper struct {
	cdc          codec.BinaryCodec
	storeService store.KVStoreService
	logger       log.Logger

	stakingKeeper types.StakingKeeper
}

// NewKeeper creates a new valaddr Keeper instance
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService store.KVStoreService,
	logger log.Logger,
	stakingKeeper types.StakingKeeper,
) Keeper {
	return Keeper{
		cdc:           cdc,
		storeService:  storeService,
		logger:        logger,
		stakingKeeper: stakingKeeper,
	}
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return k.logger.With("module", "x/"+types.ModuleName, "height", sdkCtx.BlockHeight())
}

// SetFibreProviderInfo sets the fibre provider info for a validator
func (k Keeper) SetFibreProviderInfo(ctx context.Context, consAddr sdk.ConsAddress, info types.FibreProviderInfo) error {
	store := k.storeService.OpenKVStore(ctx)
	key := types.GetFibreProviderInfoKey(consAddr)
	bz, err := k.cdc.Marshal(&info)
	if err != nil {
		return err
	}
	return store.Set(key, bz)
}

// GetFibreProviderInfo retrieves the fibre provider info for a validator
func (k Keeper) GetFibreProviderInfo(ctx context.Context, consAddr sdk.ConsAddress) (types.FibreProviderInfo, bool) {
	store := k.storeService.OpenKVStore(ctx)
	key := types.GetFibreProviderInfoKey(consAddr)

	bz, err := store.Get(key)
	if err != nil || bz == nil {
		return types.FibreProviderInfo{}, false
	}

	var info types.FibreProviderInfo
	if err := k.cdc.Unmarshal(bz, &info); err != nil {
		return types.FibreProviderInfo{}, false
	}

	return info, true
}

// DeleteFibreProviderInfo deletes the fibre provider info for a validator
func (k Keeper) DeleteFibreProviderInfo(ctx context.Context, consAddr sdk.ConsAddress) error {
	store := k.storeService.OpenKVStore(ctx)
	key := types.GetFibreProviderInfoKey(consAddr)
	return store.Delete(key)
}

// IterateFibreProviderInfo iterates over all fibre provider info entries
func (k Keeper) IterateFibreProviderInfo(ctx context.Context, cb func(consAddr sdk.ConsAddress, info types.FibreProviderInfo) bool) error {
	store := k.storeService.OpenKVStore(ctx)

	iterator, err := store.Iterator(
		types.FibreProviderInfoPrefix,
		storetypes.PrefixEndBytes(types.FibreProviderInfoPrefix),
	)
	if err != nil {
		return err
	}
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		var info types.FibreProviderInfo
		if err := k.cdc.Unmarshal(iterator.Value(), &info); err != nil {
			return err
		}

		// Extract consensus address from key (skip the prefix byte)
		consAddr := sdk.ConsAddress(iterator.Key()[len(types.FibreProviderInfoPrefix):])

		if cb(consAddr, info) {
			break
		}
	}

	return nil
}

// SetParams sets the module parameters
func (k Keeper) SetParams(ctx context.Context, params types.Params) error {
	if err := params.Validate(); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}

	store := k.storeService.OpenKVStore(ctx)
	bz, err := k.cdc.Marshal(&params)
	if err != nil {
		return err
	}
	return store.Set(types.ParamsKey, bz)
}

// GetParams retrieves the module parameters
func (k Keeper) GetParams(ctx context.Context) (types.Params, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.ParamsKey)
	if err != nil {
		return types.Params{}, err
	}
	if bz == nil {
		return types.DefaultParams(), nil
	}

	var params types.Params
	if err := k.cdc.Unmarshal(bz, &params); err != nil {
		return types.Params{}, err
	}

	return params, nil
}
