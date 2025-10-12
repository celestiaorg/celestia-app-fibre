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
func NewKeeper(
	cdc codec.Codec,
	storeKey storetypes.StoreKey,
	bankKeeper types.BankKeeper,
	authority string,
) *Keeper {
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

// GetParams gets all parameters as types.Params
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
// TODO: Refactor this method to accept the payment promise as an argument instead of the payment promise hash.
func (k Keeper) IsPaymentPromiseProcessed(ctx sdk.Context, paymentPromiseHash []byte) bool {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(paymentPromiseHash)
	return store.Has(key)
}

// SetPaymentPromiseProcessed marks a payment promise as processed
func (k Keeper) SetPaymentPromiseProcessed(ctx sdk.Context, paymentPromiseHash []byte, processedAt time.Time) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(paymentPromiseHash)
	entry := types.PaymentPromiseEntry{
		PaymentPromiseHash: paymentPromiseHash,
		ProcessedAt:        processedAt,
	}
	bz := k.cdc.MustMarshal(&entry)
	store.Set(key, bz)
}

// GetPaymentPromiseHash calculates the hash of a payment promise
// TODO: make sure this implements the hashing described in the sdk_module spec.
func (k Keeper) GetPaymentPromiseHash(promise *types.PaymentPromise) []byte {
	bz := k.cdc.MustMarshal(promise)
	hash := sha256.Sum256(bz)
	return hash[:]
}

// isValidUnprocessedPaymentPromise returns nil if the payment promise is valid
// and unprocessed.
func (k Keeper) isValidUnprocessedPaymentPromise(ctx sdk.Context, promise *types.PaymentPromise) error {
	hash := k.GetPaymentPromiseHash(promise)
	if k.IsPaymentPromiseProcessed(ctx, hash) {
		return errors.Wrap(sdkerrors.ErrInvalidRequest, "payment promise already processed")
	}

	pubKey, ok := promise.SignerPublicKey.GetCachedValue().(cryptotypes.PubKey)
	if !ok {
		return errors.Wrap(sdkerrors.ErrInvalidPubKey, "failed to get cached public key")
	}
	signerAddr := sdk.AccAddress(pubKey.Address())

	escrowAccount, found := k.GetEscrowAccount(ctx, signerAddr.String())
	if !found {
		return errors.Wrap(sdkerrors.ErrNotFound, "escrow account not found")
	}

	// Calculate required payment based on blob size and gas per blob byte
	params := k.GetParams(ctx)
	// TODO: this doesn't account for the padding that is added to the blob.
	requiredAmount := sdk.NewInt64Coin("utia", int64(promise.BlobSize*params.GasPerBlobByte))

	// Check if available balance is sufficient
	if escrowAccount.AvailableBalance.IsLT(requiredAmount) {
		return errors.Wrapf(sdkerrors.ErrInsufficientFunds, "insufficient available balance: have %s, need %s", escrowAccount.AvailableBalance, requiredAmount)
	}

	// Check timestamp is within valid window
	now := ctx.BlockTime()
	params = k.GetParams(ctx)

	// Payment promise should not be too old or too far in the future
	if promise.CreationTimestamp.Before(now.Add(-params.WithdrawalDelay)) {
		return errors.Wrap(sdkerrors.ErrInvalidRequest, "Payment promise creation timestamp must be after the current block time minus withdrawal delay.")
	}

	if promise.CreationTimestamp.After(now) {
		return errors.Wrap(sdkerrors.ErrInvalidRequest, "Payment promise creation timestamp must be before than the current block time.")
	}

	return nil
}
