package keeper

import (
	"fmt"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	fibre "github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Keeper handles all the state changes for the fibre module.
type Keeper struct {
	cdc           codec.Codec
	storeKey      storetypes.StoreKey
	bankKeeper    types.BankKeeper
	stakingKeeper types.StakingKeeper
	// authority is the address that has the authority to update module parameters.
	// This is typically the governance module address.
	authority string
}

// NewKeeper creates a new fibre Keeper instance
func NewKeeper(cdc codec.Codec, storeKey storetypes.StoreKey, bankKeeper types.BankKeeper, stakingKeeper types.StakingKeeper, authority string) *Keeper {
	return &Keeper{
		cdc:           cdc,
		storeKey:      storeKey,
		bankKeeper:    bankKeeper,
		stakingKeeper: stakingKeeper,
		authority:     authority,
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
	key := types.WithdrawalsBySignerKey(signer, requestedTimestamp)
	bz := store.Get(key)
	if bz == nil {
		return types.Withdrawal{}, false
	}

	k.cdc.MustUnmarshal(bz, &withdrawal)
	return withdrawal, true
}

// SetWithdrawal saves a withdrawal to both indexes:
// 1. Primary index: withdrawals_by_signer/{signer}/{requested_timestamp}
// 2. Secondary index: withdrawals_by_available/{available_timestamp}/{signer}
func (k Keeper) SetWithdrawal(ctx sdk.Context, withdrawal types.Withdrawal) {
	store := ctx.KVStore(k.storeKey)
	bz := k.cdc.MustMarshal(&withdrawal)

	// Store in primary index
	primaryKey := types.WithdrawalsBySignerKey(withdrawal.Signer, withdrawal.RequestedTimestamp)
	store.Set(primaryKey, bz)

	// Store in secondary index
	secondaryKey := types.WithdrawalsByAvailableKey(withdrawal.AvailableTimestamp, withdrawal.Signer)
	store.Set(secondaryKey, bz)
}

// DeleteWithdrawal removes a withdrawal from both indexes.
// This should be called when a withdrawal is processed or cancelled.
func (k Keeper) DeleteWithdrawal(ctx sdk.Context, withdrawal types.Withdrawal) {
	store := ctx.KVStore(k.storeKey)

	// Delete from primary index
	primaryKey := types.WithdrawalsBySignerKey(withdrawal.Signer, withdrawal.RequestedTimestamp)
	store.Delete(primaryKey)

	// Delete from secondary index
	secondaryKey := types.WithdrawalsByAvailableKey(withdrawal.AvailableTimestamp, withdrawal.Signer)
	store.Delete(secondaryKey)
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

// GetWithdrawalsByAvailableIterator returns an iterator for all withdrawals available up to the given time
func (k Keeper) GetWithdrawalsByAvailableIterator(ctx sdk.Context, upToTime time.Time) storetypes.Iterator {
	store := ctx.KVStore(k.storeKey)
	// Start from the beginning of the withdrawals-by-available index
	start := types.WithdrawalsByAvailableKeyPrefix
	// End at the last possible key for the given time
	end := storetypes.PrefixEndBytes(types.WithdrawalsByAvailablePrefix(upToTime))
	return store.Iterator(start, end)
}

// ParseWithdrawalsByAvailableKey parses the available_at timestamp and signer from the key
func (k Keeper) ParseWithdrawalsByAvailableKey(key []byte) (available time.Time, signer string, err error) {
	// Remove the prefix
	key = key[len(types.WithdrawalsByAvailableKeyPrefix):]

	// Parse the timestamp (first 29 bytes as per SDK's FormatTimeBytes)
	timestampBytes := key[:29]

	available, err = sdk.ParseTimeBytes(timestampBytes)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to parse timestamp: %w", err)
	}

	// Skip the separator "/"
	key = key[29:]
	if len(key) > 0 && key[0] == '/' {
		key = key[1:]
	}

	// The rest is the signer address
	signer = string(key)
	return available, signer, nil
}

// GetProcessedPayment retrieves a processed payment by promiseHash
func (k Keeper) GetProcessedPayment(ctx sdk.Context, promiseHash []byte) (payment types.ProcessedPayment, isFound bool) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(promiseHash)
	bz := store.Get(key)
	if bz == nil {
		return types.ProcessedPayment{}, false
	}
	k.cdc.MustUnmarshal(bz, &payment)
	return payment, true
}

// SetProcessedPayment saves a processed payment to the store
func (k Keeper) SetProcessedPayment(ctx sdk.Context, payment types.ProcessedPayment) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(payment.PaymentPromiseHash)
	bz := k.cdc.MustMarshal(&payment)
	store.Set(key, bz)
}

// DeleteProcessedPayment removes a processed payment from the store
func (k Keeper) DeleteProcessedPayment(ctx sdk.Context, promiseHash []byte) {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(promiseHash)
	store.Delete(key)
}

// IsPaymentPromiseProcessed returns true if a payment has been processed for the given promise.
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

// IsPaymentProcessedByHash returns true if a payment has been processed for the given promise hash.
func (k Keeper) IsPaymentProcessedByHash(ctx sdk.Context, promiseHash []byte) bool {
	store := ctx.KVStore(k.storeKey)
	key := types.PaymentPromiseKey(promiseHash)
	return store.Has(key)
}

// ValidatePaymentPromiseInternal validates a payment promise and returns an error if the promise is invalid.
// It performs both stateless and stateful validation.
func (k Keeper) ValidatePaymentPromiseInternal(ctx sdk.Context, promise *types.PaymentPromise) error {
	// Perform stateless validation
	if err := k.ValidatePaymentPromiseStateless(ctx, promise); err != nil {
		return err
	}

	// Perform stateful validation
	if err := k.ValidatePaymentPromiseStateful(ctx, promise); err != nil {
		return err
	}

	return nil
}

func (k Keeper) ValidatePaymentPromiseStateless(ctx sdk.Context, promise *types.PaymentPromise) error {
	pp := fibre.PaymentPromise{}
	if err := pp.FromProto(promise); err != nil {
		return fmt.Errorf("invalid payment promise format: %v", err)
	}

	return pp.Validate()
}

// ValidatePaymentPromiseStateful performs stateful validation of a payment promise.
// It checks:
// 1. The payment promise has not already been processed
// 2. The escrow account exists for the signer
// 3. The escrow account has sufficient available balance
//
// This method does NOT perform stateless validation.
// Callers should perform stateless validation separately via pp.Validate().
func (k Keeper) ValidatePaymentPromiseStateful(ctx sdk.Context, promise *types.PaymentPromise) error {
	// Check if payment promise has already been processed
	if isAlreadyProcessed := k.IsPaymentPromiseProcessed(ctx, promise); isAlreadyProcessed {
		return fmt.Errorf("payment promise has already been processed")
	}

	// Check escrow account exists
	signerAddr := sdk.AccAddress(promise.SignerPublicKey.Address())
	signerAddrStr := signerAddr.String()
	escrowAccount, found := k.GetEscrowAccount(ctx, signerAddrStr)
	if !found {
		return fmt.Errorf("escrow account not found for signer %v", signerAddrStr)
	}

	// Check sufficient available balance
	params := k.GetParams(ctx)
	gasRequired := uint64(promise.BlobSize) * uint64(params.GasPerBlobByte)

	// TODO: This assumes 1 gas = 1 utia but the minimum gas price could be
	// different.
	requiredAmount := sdk.NewCoin("utia", math.NewInt(int64(gasRequired)))

	hasSufficientBalance := escrowAccount.AvailableBalance.IsGTE(requiredAmount)
	if !hasSufficientBalance {
		return fmt.Errorf("insufficient balance in escrow account. required: %v, available: %v", requiredAmount, escrowAccount.AvailableBalance)
	}

	return nil
}
