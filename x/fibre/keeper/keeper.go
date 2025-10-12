package keeper

import (
	"crypto/sha256"
	"fmt"
	"time"

	"cosmossdk.io/errors"
	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cosmos/cosmos-sdk/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
)

// Keeper handles all the state changes for the fibre module.
type Keeper struct {
	cdc            codec.Codec
	storeKey       storetypes.StoreKey
	bankKeeper     types.BankKeeper
	legacySubspace paramtypes.Subspace
	authority      string
}

// NewKeeper creates a new fibre Keeper instance
func NewKeeper(
	cdc codec.Codec,
	storeKey storetypes.StoreKey,
	bankKeeper types.BankKeeper,
	legacySubspace paramtypes.Subspace,
	authority string,
) *Keeper {
	if !legacySubspace.HasKeyTable() {
		legacySubspace = legacySubspace.WithKeyTable(types.ParamKeyTable())
	}

	return &Keeper{
		cdc:            cdc,
		storeKey:       storeKey,
		bankKeeper:     bankKeeper,
		legacySubspace: legacySubspace,
		authority:      authority,
	}
}

// GetAuthority returns the fibre module's authority.
func (k Keeper) GetAuthority() string {
	return k.authority
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", fmt.Sprintf("x/%s", types.ModuleName))
}

// GetParams gets all parameters as types.Params
func (k Keeper) GetParams(ctx sdk.Context) types.Params {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte(types.ParamsKey))
	if len(bz) == 0 {
		// fallback to legacy store space.
		var params types.Params
		k.legacySubspace.GetParamSet(ctx, &params)
		return params
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

// GetEscrowAccount retrieves an escrow account by signer address
func (k Keeper) GetEscrowAccount(ctx sdk.Context, signer string) (types.EscrowAccount, bool) {
	store := ctx.KVStore(k.storeKey)
	key := types.EscrowAccountKey(signer)
	bz := store.Get(key)
	if bz == nil {
		return types.EscrowAccount{}, false
	}

	var account types.EscrowAccount
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
func (k Keeper) GetWithdrawal(ctx sdk.Context, signer string, requestedTimestamp time.Time) (types.Withdrawal, bool) {
	store := ctx.KVStore(k.storeKey)
	key := types.WithdrawalKey(signer, requestedTimestamp)
	bz := store.Get(key)
	if bz == nil {
		return types.Withdrawal{}, false
	}

	var withdrawal types.Withdrawal
	k.cdc.MustUnmarshal(bz, &withdrawal)
	return withdrawal, true
}

// SetWithdrawal stores a withdrawal
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

// IsPaymentPromiseProcessed checks if a payment promise has been processed
func (k Keeper) IsPaymentPromiseProcessed(ctx sdk.Context, hash []byte) bool {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(hash)
	return store.Has(key)
}

// SetPaymentPromiseProcessed marks a payment promise as processed
func (k Keeper) SetPaymentPromiseProcessed(ctx sdk.Context, paymentPromiseHash []byte, processedAt time.Time) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(paymentPromiseHash)
	entry := types.ProcessedPaymentPromiseEntry{
		PaymentPromiseHash: paymentPromiseHash,
		ProcessedAt:        processedAt,
	}
	bz := k.cdc.MustMarshal(&entry)
	store.Set(key, bz)
}

// GetPaymentPromiseHash calculates the hash of a payment promise
func (k Keeper) GetPaymentPromiseHash(promise *types.PaymentPromise) []byte {
	bz := k.cdc.MustMarshal(promise)
	hash := sha256.Sum256(bz)
	return hash[:]
}

// ValidatePaymentPromiseInternal validates a payment promise for server use
func (k Keeper) ValidatePaymentPromiseInternal(ctx sdk.Context, promise *types.PaymentPromise) error {
	// Check if already processed
	hash := k.GetPaymentPromiseHash(promise)
	if k.IsPaymentPromiseProcessed(ctx, hash) {
		return errors.Wrap(sdkerrors.ErrInvalidRequest, "payment promise already processed")
	}

	// Get signer address from public key
	pubKey, ok := promise.SignerPublicKey.GetCachedValue().(cryptotypes.PubKey)
	if !ok {
		return errors.Wrap(sdkerrors.ErrInvalidPubKey, "failed to get cached public key")
	}
	signerAddr := sdk.AccAddress(pubKey.Address())

	// Check escrow account exists and has sufficient balance
	escrowAccount, found := k.GetEscrowAccount(ctx, signerAddr.String())
	if !found {
		return errors.Wrap(sdkerrors.ErrNotFound, "escrow account not found")
	}

	// Calculate required payment based on blob size and gas per blob byte
	params := k.GetParams(ctx)
	requiredAmount := sdk.NewInt64Coin("utia", int64(promise.BlobSize*params.GasPerBlobByte))

	// Check if available balance is sufficient
	if escrowAccount.AvailableBalance.IsLT(requiredAmount) {
		return errors.Wrapf(sdkerrors.ErrInsufficientFunds,
			"insufficient available balance: have %s, need %s",
			escrowAccount.AvailableBalance, requiredAmount)
	}

	// Check timestamp is within valid window
	now := ctx.BlockTime()
	params = k.GetParams(ctx)

	// Payment promise should not be too old or too far in the future
	if promise.CreationTimestamp.Before(now.Add(-params.WithdrawalDelay)) {
		return errors.Wrap(sdkerrors.ErrInvalidRequest, "payment promise too old")
	}

	if promise.CreationTimestamp.After(now.Add(5 * time.Minute)) { // Allow 5 minutes in the future
		return errors.Wrap(sdkerrors.ErrInvalidRequest, "payment promise too far in the future")
	}

	return nil
}

// Note: GetNextWithdrawalID is no longer needed since withdrawals are keyed by timestamp
