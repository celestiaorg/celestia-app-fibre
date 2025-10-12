package keeper

import (
	storetypes "cosmossdk.io/store/types"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) {
	// Set params
	k.SetParams(ctx, genState.Params)

	// Set escrow accounts
	for _, escrowAccount := range genState.EscrowAccounts {
		k.SetEscrowAccount(ctx, escrowAccount)
	}

	// Set withdrawals
	for _, withdrawal := range genState.Withdrawals {
		k.SetWithdrawal(ctx, withdrawal)
	}

	// Set processed payment promises
	for _, entry := range genState.ProcessedPaymentPromises {
		k.SetPaymentPromiseProcessed(ctx, entry.PromiseHash, entry.ProcessedAt)
	}
}

// ExportGenesis returns the module's exported genesis
func (k Keeper) ExportGenesis(ctx sdk.Context) *types.GenesisState {
	genesis := &types.GenesisState{}
	genesis.Params = k.GetParams(ctx)

	// Export escrow accounts
	k.IterateEscrowAccounts(ctx, func(account types.EscrowAccount) bool {
		genesis.EscrowAccounts = append(genesis.EscrowAccounts, account)
		return false
	})

	// Export withdrawals
	k.IterateWithdrawals(ctx, func(withdrawal types.Withdrawal) bool {
		genesis.Withdrawals = append(genesis.Withdrawals, withdrawal)
		return false
	})

	// Export processed payment promises
	k.IterateProcessedPaymentPromises(ctx, func(entry types.ProcessedPaymentPromiseEntry) bool {
		genesis.ProcessedPaymentPromises = append(genesis.ProcessedPaymentPromises, entry)
		return false
	})

	return genesis
}

// IterateEscrowAccounts iterates over all escrow accounts and calls the provided callback function
func (k Keeper) IterateEscrowAccounts(ctx sdk.Context, cb func(account types.EscrowAccount) bool) {
	store := ctx.KVStore(k.storeKey)
	iterator := storetypes.KVStorePrefixIterator(store, types.EscrowAccountKeyPrefix)
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		var account types.EscrowAccount
		k.cdc.MustUnmarshal(iterator.Value(), &account)
		if cb(account) {
			break
		}
	}
}

// IterateWithdrawals iterates over all withdrawals and calls the provided callback function
func (k Keeper) IterateWithdrawals(ctx sdk.Context, cb func(withdrawal types.Withdrawal) bool) {
	store := ctx.KVStore(k.storeKey)
	iterator := storetypes.KVStorePrefixIterator(store, types.WithdrawalKeyPrefix)
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		var withdrawal types.Withdrawal
		k.cdc.MustUnmarshal(iterator.Value(), &withdrawal)
		if cb(withdrawal) {
			break
		}
	}
}

// IterateProcessedPaymentPromises iterates over all processed payment promises and calls the provided callback function
func (k Keeper) IterateProcessedPaymentPromises(ctx sdk.Context, cb func(entry types.ProcessedPaymentPromiseEntry) bool) {
	store := ctx.KVStore(k.storeKey)
	iterator := storetypes.KVStorePrefixIterator(store, types.PaymentPromiseKeyPrefix)
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		var entry types.ProcessedPaymentPromiseEntry
		k.cdc.MustUnmarshal(iterator.Value(), &entry)
		if cb(entry) {
			break
		}
	}
}
