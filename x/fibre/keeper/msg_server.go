package keeper

import (
	"context"

	"cosmossdk.io/errors"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

// DepositToEscrow deposits funds to the signer's escrow account
func (k msgServer) DepositToEscrow(goCtx context.Context, msg *types.MsgDepositToEscrow) (*types.MsgDepositToEscrowResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	signerAddr, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, errors.Wrapf(sdkerrors.ErrInvalidAddress, "invalid signer address: %s", err)
	}

	// Transfer funds from signer to module account
	if err := k.Keeper.bankKeeper.SendCoinsFromAccountToModule(ctx, signerAddr, types.ModuleName, sdk.NewCoins(msg.Amount)); err != nil {
		return nil, errors.Wrap(err, "failed to transfer funds to escrow")
	}

	// Get or create escrow account
	escrowAccount, found := k.Keeper.GetEscrowAccount(ctx, msg.Signer)
	if !found {
		escrowAccount = types.EscrowAccount{
			Signer:           msg.Signer,
			Balance:          msg.Amount,
			AvailableBalance: msg.Amount,
		}
	} else {
		escrowAccount.Balance = escrowAccount.Balance.Add(msg.Amount)
		escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Add(msg.Amount)
	}

	// Save updated escrow account
	k.Keeper.SetEscrowAccount(ctx, escrowAccount)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(&types.EventDepositToEscrow{
		Signer: msg.Signer,
		Amount: msg.Amount,
	}); err != nil {
		return nil, errors.Wrap(err, "failed to emit event")
	}

	return &types.MsgDepositToEscrowResponse{}, nil
}

// RequestWithdrawal requests withdrawal from the signer's escrow account
func (k msgServer) RequestWithdrawal(goCtx context.Context, msg *types.MsgRequestWithdrawal) (*types.MsgRequestWithdrawalResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Get escrow account
	escrowAccount, found := k.Keeper.GetEscrowAccount(ctx, msg.Signer)
	if !found {
		return nil, errors.Wrap(sdkerrors.ErrNotFound, "escrow account not found")
	}

	// Check if sufficient available balance
	if escrowAccount.AvailableBalance.IsLT(msg.Amount) {
		return nil, errors.Wrapf(sdkerrors.ErrInsufficientFunds,
			"insufficient available balance: have %s, need %s",
			escrowAccount.AvailableBalance, msg.Amount)
	}

	// Create withdrawal request
	withdrawal := types.Withdrawal{
		Signer:             msg.Signer,
		Amount:             msg.Amount,
		RequestedTimestamp: ctx.BlockTime(),
	}

	// Update available balance (reduce by withdrawal amount)
	escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Sub(msg.Amount)

	// Save updated escrow account and withdrawal
	k.Keeper.SetEscrowAccount(ctx, escrowAccount)
	k.Keeper.SetWithdrawal(ctx, withdrawal)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(&types.EventWithdrawFromEscrowRequest{
		Signer:      msg.Signer,
		Amount:      msg.Amount,
		AvailableAt: ctx.BlockTime().Add(k.Keeper.GetParams(ctx).WithdrawalDelay),
	}); err != nil {
		return nil, errors.Wrap(err, "failed to emit event")
	}

	return &types.MsgRequestWithdrawalResponse{}, nil
}

// PayForFibre processes a payment for fibre service
func (k msgServer) PayForFibre(goCtx context.Context, msg *types.MsgPayForFibre) (*types.MsgPayForFibreResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate payment promise
	if err := k.Keeper.ValidatePaymentPromiseInternal(ctx, &msg.PaymentPromise); err != nil {
		return nil, errors.Wrap(err, "invalid payment promise")
	}

	// Get payment promise hash
	hash := k.Keeper.GetPaymentPromiseHash(&msg.PaymentPromise)

	// Get signer address from public key
	pubKey, ok := msg.PaymentPromise.SignerPublicKey.GetCachedValue().(cryptotypes.PubKey)
	if !ok {
		return nil, errors.Wrap(sdkerrors.ErrInvalidPubKey, "failed to get cached public key")
	}
	signerAddr := sdk.AccAddress(pubKey.Address())

	// Get escrow account
	escrowAccount, found := k.Keeper.GetEscrowAccount(ctx, signerAddr.String())
	if !found {
		return nil, errors.Wrap(sdkerrors.ErrNotFound, "escrow account not found")
	}

	// Calculate payment amount
	params := k.Keeper.GetParams(ctx)
	paymentAmount := sdk.NewInt64Coin("utia", int64(msg.PaymentPromise.BlobSize*params.GasPerBlobByte))

	// Deduct payment from escrow account
	escrowAccount.Balance = escrowAccount.Balance.Sub(paymentAmount)
	escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Sub(paymentAmount)

	// Mark payment promise as processed
	k.Keeper.SetPaymentPromiseProcessed(ctx, hash, ctx.BlockTime())

	// Save updated escrow account
	k.Keeper.SetEscrowAccount(ctx, escrowAccount)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(&types.EventPayForFibre{
		Signer:     signerAddr.String(),
		Namespace:  msg.PaymentPromise.Namespace,
		Commitment: msg.PaymentPromise.Commitment,
	}); err != nil {
		return nil, errors.Wrap(err, "failed to emit event")
	}

	return &types.MsgPayForFibreResponse{}, nil
}

// PaymentPromiseTimeout processes a payment promise timeout
func (k msgServer) PaymentPromiseTimeout(goCtx context.Context, msg *types.MsgPaymentPromiseTimeout) (*types.MsgPaymentPromiseTimeoutResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Check if payment promise has already been processed
	hash := k.Keeper.GetPaymentPromiseHash(&msg.PaymentPromise)
	if k.Keeper.IsPaymentPromiseProcessed(ctx, hash) {
		return nil, errors.Wrap(sdkerrors.ErrInvalidRequest, "payment promise already processed")
	}

	// Check if timeout period has passed
	params := k.Keeper.GetParams(ctx)
	timeoutTime := msg.PaymentPromise.CreationTimestamp.Add(params.PaymentPromiseTimeout)
	if ctx.BlockTime().Before(timeoutTime) {
		return nil, errors.Wrap(sdkerrors.ErrInvalidRequest, "payment promise timeout period has not passed")
	}

	// Get signer address from public key
	pubKey, ok := msg.PaymentPromise.SignerPublicKey.GetCachedValue().(cryptotypes.PubKey)
	if !ok {
		return nil, errors.Wrap(sdkerrors.ErrInvalidPubKey, "failed to get cached public key")
	}
	signerAddr := sdk.AccAddress(pubKey.Address())

	// Get escrow account
	escrowAccount, found := k.Keeper.GetEscrowAccount(ctx, signerAddr.String())
	if !found {
		return nil, errors.Wrap(sdkerrors.ErrNotFound, "escrow account not found")
	}

	// Calculate payment amount
	paymentAmount := sdk.NewInt64Coin("utia", int64(msg.PaymentPromise.BlobSize*params.GasPerBlobByte))

	// Deduct payment from escrow account (timeout still charges the user)
	escrowAccount.Balance = escrowAccount.Balance.Sub(paymentAmount)
	escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Sub(paymentAmount)

	// Mark payment promise as processed
	k.Keeper.SetPaymentPromiseProcessed(ctx, hash, ctx.BlockTime())

	// Save updated escrow account
	k.Keeper.SetEscrowAccount(ctx, escrowAccount)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(&types.EventPaymentPromiseTimeout{
		Processor:          msg.Signer,
		EscrowSigner:       signerAddr.String(),
		PaymentPromiseHash: hash,
	}); err != nil {
		return nil, errors.Wrap(err, "failed to emit event")
	}

	return &types.MsgPaymentPromiseTimeoutResponse{}, nil
}

// UpdateFibreParams updates the fibre module parameters
func (k msgServer) UpdateFibreParams(goCtx context.Context, msg *types.MsgUpdateFibreParams) (*types.MsgUpdateFibreParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Check authority
	if k.Keeper.GetAuthority() != msg.Authority {
		return nil, errors.Wrapf(sdkerrors.ErrUnauthorized,
			"invalid authority; expected %s, got %s", k.Keeper.GetAuthority(), msg.Authority)
	}

	// Validate params
	if err := msg.Params.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid params")
	}

	// Set new params
	k.Keeper.SetParams(ctx, msg.Params)

	// Note: EventUpdateFibreParams doesn't exist in the protobuf definitions
	// We can emit a generic event or skip this for now

	return &types.MsgUpdateFibreParamsResponse{}, nil
}
