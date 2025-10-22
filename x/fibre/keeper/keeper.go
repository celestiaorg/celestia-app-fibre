package keeper

import (
	"fmt"
	"time"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	fibre "github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Keeper handles all the state changes for the fibre module.
type Keeper struct {
	cdc        codec.Codec
	storeKey   storetypes.StoreKey
	bankKeeper types.BankKeeper
	// authority is the address that has the authority to update module parameters.
	// This is typically the governance module address.
	authority string
}

// NewKeeper creates a new fibre Keeper instance
func NewKeeper(cdc codec.Codec, storeKey storetypes.StoreKey, bankKeeper types.BankKeeper, authority string) *Keeper {
	return &Keeper{
		cdc:        cdc,
		storeKey:   storeKey,
		bankKeeper: bankKeeper,
		authority:  authority,
	}
}

// GetAuthority returns the fibre module's authority.
func (k Keeper) GetAuthority() string {
	return k.authority
}

// Logger returns a x/fibre specific logger.
func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", fmt.Sprintf("x/%s", types.ModuleName))
}

// GetParams returns the x/fibre module's parameters.
func (k Keeper) GetParams(ctx sdk.Context) types.Params {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte(types.ParamsKey))
	if len(bz) == 0 {
		return types.DefaultParams()
	}

	var params types.Params
	k.cdc.MustUnmarshal(bz, &params)
	return params
}

// SetParams sets the params
func (k Keeper) SetParams(ctx sdk.Context, params types.Params) {
	store := ctx.KVStore(k.storeKey)
	bz := k.cdc.MustMarshal(&params)
	store.Set([]byte(types.ParamsKey), bz)
}

// GetEscrowAccount retrieves an escrow account by signer address.
func (k Keeper) GetEscrowAccount(ctx sdk.Context, signer string) (account types.EscrowAccount, isFound bool) {
	store := ctx.KVStore(k.storeKey)
	key := types.EscrowAccountKey(signer)
	bz := store.Get(key)
	if bz == nil {
		return types.EscrowAccount{}, false
	}

	k.cdc.MustUnmarshal(bz, &account)
	return account, true
}

// SetEscrowAccount stores an escrow account
func (k Keeper) SetEscrowAccount(ctx sdk.Context, account types.EscrowAccount) {
	store := ctx.KVStore(k.storeKey)
	key := types.EscrowAccountKey(account.Signer)
	bz := k.cdc.MustMarshal(&account)
	store.Set(key, bz)
}

// DeleteEscrowAccount removes an escrow account from the store
func (k Keeper) DeleteEscrowAccount(ctx sdk.Context, signer string) {
	store := ctx.KVStore(k.storeKey)
	key := types.EscrowAccountKey(signer)
	store.Delete(key)
}

// GetWithdrawal retrieves a withdrawal by signer and timestamp
func (k Keeper) GetWithdrawal(ctx sdk.Context, signer string, requestedTimestamp time.Time) (withdrawal types.Withdrawal, isFound bool) {
	store := ctx.KVStore(k.storeKey)
	key := types.WithdrawalKey(signer, requestedTimestamp)
	bz := store.Get(key)
	if bz == nil {
		return types.Withdrawal{}, false
	}

	k.cdc.MustUnmarshal(bz, &withdrawal)
	return withdrawal, true
}

// SetWithdrawal saves a withdrawal to the store
func (k Keeper) SetWithdrawal(ctx sdk.Context, withdrawal types.Withdrawal) {
	store := ctx.KVStore(k.storeKey)
	key := types.WithdrawalKey(withdrawal.Signer, withdrawal.RequestedTimestamp)
	bz := k.cdc.MustMarshal(&withdrawal)
	store.Set(key, bz)
}

// DeleteWithdrawal removes a withdrawal from the store
func (k Keeper) DeleteWithdrawal(ctx sdk.Context, signer string, requestedTimestamp time.Time) {
	store := ctx.KVStore(k.storeKey)
	key := types.WithdrawalKey(signer, requestedTimestamp)
	store.Delete(key)
}

// GetWithdrawalsBySigner retrieves all withdrawals for a signer
func (k Keeper) GetWithdrawalsBySigner(ctx sdk.Context, signer string) []types.Withdrawal {
	store := ctx.KVStore(k.storeKey)
	prefix := types.WithdrawalsBySignerPrefix(signer)
	iterator := storetypes.KVStorePrefixIterator(store, prefix)
	defer iterator.Close()

	var withdrawals []types.Withdrawal
	for ; iterator.Valid(); iterator.Next() {
		var withdrawal types.Withdrawal
		k.cdc.MustUnmarshal(iterator.Value(), &withdrawal)
		withdrawals = append(withdrawals, withdrawal)
	}

	return withdrawals
}

// GetPaymentPromiseEntry retrieves a payment promise entry by hash
func (k Keeper) GetPaymentPromiseEntry(ctx sdk.Context, promiseHash []byte) (entry types.PaymentPromiseEntry, isFound bool) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(promiseHash)
	bz := store.Get(key)
	if bz == nil {
		return types.PaymentPromiseEntry{}, false
	}
	k.cdc.MustUnmarshal(bz, &entry)
	return entry, true
}

// SetPaymentPromiseEntry saves a payment promise entry to the store as processed
func (k Keeper) SetPaymentPromiseEntry(ctx sdk.Context, entry types.PaymentPromiseEntry) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(entry.PaymentPromiseHash)
	bz := k.cdc.MustMarshal(&entry)
	store.Set(key, bz)
}

// DeletePaymentPromiseEntry removes a payment promise entry from the store
func (k Keeper) DeletePaymentPromiseEntry(ctx sdk.Context, promiseHash []byte) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(promiseHash)
	store.Delete(key)
}

// IsPaymentPromiseProcessed returns true if a payment promise has been processed.
func (k Keeper) IsPaymentPromiseProcessed(ctx sdk.Context, promise *types.PaymentPromise) bool {
	store := ctx.KVStore(k.storeKey)
	pp := fibre.PaymentPromise{}
	pp.FromProto(promise)
	hash, err := pp.Hash()
	if err != nil {
		return false
	}
	key := types.PaymentPromiseKey(hash)
	return store.Has(key)
}
