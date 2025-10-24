package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	fibre "github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

var _ types.MsgServer = msgServer{}

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the fibre MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

// DepositToEscrow deposits funds to the signer's escrow account
func (ms msgServer) DepositToEscrow(goCtx context.Context, msg *types.MsgDepositToEscrow) (*types.MsgDepositToEscrowResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}

	// Convert signer address
	signerAddr, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidAddress, "invalid signer address: %s", err)
	}

	// Get or create escrow account
	escrowAccount, found := ms.GetEscrowAccount(ctx, msg.Signer)
	if !found {
		escrowAccount = types.EscrowAccount{
			Signer:           msg.Signer,
			Balance:          sdk.NewCoin(msg.Amount.Denom, math.ZeroInt()),
			AvailableBalance: sdk.NewCoin(msg.Amount.Denom, math.ZeroInt()),
		}
	}

	// Transfer funds from user to module
	if err := ms.bankKeeper.SendCoinsFromAccountToModule(ctx, signerAddr, types.ModuleName, sdk.NewCoins(msg.Amount)); err != nil {
		return nil, errorsmod.Wrapf(err, "failed to transfer funds to escrow")
	}

	// Update escrow account balances
	escrowAccount.Balance = escrowAccount.Balance.Add(msg.Amount)
	escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Add(msg.Amount)

	// Save the updated escrow account
	ms.SetEscrowAccount(ctx, escrowAccount)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(types.NewEventDepositToEscrow(msg.Signer, msg.Amount)); err != nil {
		return nil, err
	}

	return &types.MsgDepositToEscrowResponse{}, nil
}

// RequestWithdrawal requests withdrawal from the signer's escrow account
func (ms msgServer) RequestWithdrawal(goCtx context.Context, msg *types.MsgRequestWithdrawal) (*types.MsgRequestWithdrawalResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}

	// Get escrow account
	escrowAccount, found := ms.GetEscrowAccount(ctx, msg.Signer)
	if !found {
		return nil, errorsmod.Wrapf(sdkerrors.ErrNotFound, "escrow account not found for signer: %s", msg.Signer)
	}

	// Check if sufficient available balance
	if escrowAccount.AvailableBalance.IsLT(msg.Amount) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInsufficientFunds, "insufficient available balance: have %s, need %s", escrowAccount.AvailableBalance, msg.Amount)
	}

	// Get withdrawal delay from params
	params := ms.GetParams(ctx)
	requestedTimestamp := ctx.BlockTime()

	// Create withdrawal request
	withdrawal := types.Withdrawal{
		Signer:             msg.Signer,
		Amount:             msg.Amount,
		RequestedTimestamp: requestedTimestamp,
	}

	// Update escrow account available balance (lock the funds)
	escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Sub(msg.Amount)
	ms.SetEscrowAccount(ctx, escrowAccount)

	// Save withdrawal request
	ms.SetWithdrawal(ctx, withdrawal)

	// Calculate available timestamp
	availableAt := requestedTimestamp.Add(params.WithdrawalDelay)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(
		types.NewEventWithdrawFromEscrowRequest(msg.Signer, msg.Amount, availableAt),
	); err != nil {
		return nil, err
	}

	return &types.MsgRequestWithdrawalResponse{}, nil
}

// PayForFibre processes a payment promise with validator signatures
func (ms msgServer) PayForFibre(goCtx context.Context, msg *types.MsgPayForFibre) (*types.MsgPayForFibreResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}

	// Validate payment promise internally
	if err := ms.ValidatePaymentPromiseInternal(ctx, &msg.PaymentPromise); err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "payment promise validation failed: %s", err)
	}

	// Check if payment promise has already been processed
	if ms.IsPaymentPromiseProcessed(ctx, &msg.PaymentPromise) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "payment promise has already been processed")
	}

	// TODO: Validate validator signatures
	// This would involve checking that the signatures are from valid validators
	// and that they collectively represent sufficient stake/voting power

	// Convert payment promise to internal format to get hash
	pp := fibre.PaymentPromise{}
	if err := pp.FromProto(&msg.PaymentPromise); err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "failed to convert payment promise: %s", err)
	}

	promiseHash, err := pp.Hash()
	if err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "failed to hash payment promise: %s", err)
	}

	// Get escrow account for the payment promise signer
	signerPubKey := msg.PaymentPromise.SignerPublicKey
	signerAddr := sdk.AccAddress(signerPubKey.Address()).String()

	escrowAccount, found := ms.GetEscrowAccount(ctx, signerAddr)
	if !found {
		return nil, errorsmod.Wrapf(sdkerrors.ErrNotFound, "escrow account not found for signer: %s", signerAddr)
	}

	// Calculate payment amount based on blob size and gas per byte
	params := ms.GetParams(ctx)
	paymentAmount := sdk.NewInt64Coin("utia", int64(msg.PaymentPromise.BlobSize*params.GasPerBlobByte))

	// Check if sufficient available balance
	if escrowAccount.AvailableBalance.IsLT(paymentAmount) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInsufficientFunds, "insufficient available balance: have %s, need %s", escrowAccount.AvailableBalance, paymentAmount)
	}

	// Deduct payment from escrow account
	escrowAccount.Balance = escrowAccount.Balance.Sub(paymentAmount)
	escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Sub(paymentAmount)
	ms.SetEscrowAccount(ctx, escrowAccount)

	// Record processed payment
	processedPayment := types.ProcessedPayment{
		PaymentPromiseHash: promiseHash,
		ProcessedAt:        ctx.BlockTime(),
	}
	ms.SetProcessedPayment(ctx, processedPayment)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(
		types.NewEventPayForFibre(signerAddr, msg.PaymentPromise.Namespace, msg.PaymentPromise.Commitment),
	); err != nil {
		return nil, err
	}

	return &types.MsgPayForFibreResponse{}, nil
}

// PaymentPromiseTimeout processes a payment promise after the timeout period
func (ms msgServer) PaymentPromiseTimeout(goCtx context.Context, msg *types.MsgPaymentPromiseTimeout) (*types.MsgPaymentPromiseTimeoutResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}

	// Convert payment promise to internal format
	pp := fibre.PaymentPromise{}
	if err := pp.FromProto(&msg.PaymentPromise); err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "failed to convert payment promise: %s", err)
	}

	promiseHash, err := pp.Hash()
	if err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "failed to hash payment promise: %s", err)
	}

	// Check if payment promise has already been processed
	if ms.IsPaymentProcessedByHash(ctx, promiseHash) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "payment promise has already been processed")
	}

	// Check if timeout period has passed
	params := ms.GetParams(ctx)
	timeoutDeadline := msg.PaymentPromise.CreationTimestamp.Add(params.PaymentPromiseTimeout)

	if ctx.BlockTime().Before(timeoutDeadline) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "payment promise has not yet timed out. Timeout at: %s, current time: %s", timeoutDeadline, ctx.BlockTime())
	}

	// Get escrow account for the payment promise signer
	signerPubKey := msg.PaymentPromise.SignerPublicKey
	escrowSigner := sdk.AccAddress(signerPubKey.Address()).String()

	escrowAccount, found := ms.GetEscrowAccount(ctx, escrowSigner)
	if !found {
		return nil, errorsmod.Wrapf(sdkerrors.ErrNotFound, "escrow account not found for signer: %s", escrowSigner)
	}

	// Calculate payment amount based on blob size and gas per byte (same as PayForFibre)
	paymentAmount := sdk.NewInt64Coin("utia", int64(msg.PaymentPromise.BlobSize*params.GasPerBlobByte))

	// Check if sufficient balance (should always be true since promise was validated, but safety check)
	if escrowAccount.Balance.IsLT(paymentAmount) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInsufficientFunds, "insufficient balance: have %s, need %s", escrowAccount.Balance, paymentAmount)
	}

	// Deduct payment from escrow account (both balance and available_balance)
	escrowAccount.Balance = escrowAccount.Balance.Sub(paymentAmount)
	escrowAccount.AvailableBalance = escrowAccount.AvailableBalance.Sub(paymentAmount)
	ms.SetEscrowAccount(ctx, escrowAccount)

	// Record processed payment (timeout)
	processedPayment := types.ProcessedPayment{
		PaymentPromiseHash: promiseHash,
		ProcessedAt:        ctx.BlockTime(),
	}
	ms.SetProcessedPayment(ctx, processedPayment)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(
		types.NewEventPaymentPromiseTimeout(msg.Signer, escrowSigner, promiseHash),
	); err != nil {
		return nil, err
	}

	return &types.MsgPaymentPromiseTimeoutResponse{}, nil
}

// UpdateFibreParams updates the fibre module parameters
func (ms msgServer) UpdateFibreParams(goCtx context.Context, msg *types.MsgUpdateFibreParams) (*types.MsgUpdateFibreParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate the message
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}

	// Check if the signer is the module authority
	if msg.Authority != ms.GetAuthority() {
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "invalid authority; expected %s, got %s", ms.GetAuthority(), msg.Authority)
	}

	// Set the new parameters
	ms.SetParams(ctx, msg.Params)

	// Emit event
	if err := ctx.EventManager().EmitTypedEvent(
		types.NewEventUpdateFibreParams(msg.Authority, msg.Params),
	); err != nil {
		return nil, err
	}

	return &types.MsgUpdateFibreParamsResponse{}, nil
}
