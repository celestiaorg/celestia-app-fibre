package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	fibre "github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	core "github.com/cometbft/cometbft/types"
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
	event := types.NewEventDepositToEscrow(msg.Signer, msg.Amount)
	if err := ctx.EventManager().EmitTypedEvent(event); err != nil {
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
	event := types.NewEventWithdrawFromEscrowRequest(msg.Signer, msg.Amount, requestedTimestamp, availableAt)
	if err := ctx.EventManager().EmitTypedEvent(event); err != nil {
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

	// Validate validator signatures using existing fibre/validator.SignatureSet
	if err := ms.validateValidatorSignatures(ctx, &msg.PaymentPromise, msg.ValidatorSignatures); err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "validator signature validation failed: %s", err)
	}

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
	paymentAmount := ms.calculatePaymentAmount(ctx, msg.PaymentPromise.BlobSize)

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
	event := types.NewEventPayForFibre(signerAddr, msg.PaymentPromise.Namespace, msg.PaymentPromise.Commitment)
	if err := ctx.EventManager().EmitTypedEvent(event); err != nil {
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
	paymentAmount := ms.calculatePaymentAmount(ctx, msg.PaymentPromise.BlobSize)

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
	event := types.NewEventPaymentPromiseTimeout(msg.Signer, escrowSigner, promiseHash)
	if err := ctx.EventManager().EmitTypedEvent(event); err != nil {
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
	event := types.NewEventUpdateFibreParams(msg.Authority, msg.Params)
	if err := ctx.EventManager().EmitTypedEvent(event); err != nil {
		return nil, err
	}

	return &types.MsgUpdateFibreParamsResponse{}, nil
}

// calculatePaymentAmount calculates the payment amount for a fibre blob based on its size and gas parameters
func (ms msgServer) calculatePaymentAmount(ctx sdk.Context, blobSize uint32) sdk.Coin {
	params := ms.GetParams(ctx)
	// TODO: this assumes 1 utia per gas which may not be correct.
	return sdk.NewInt64Coin(appconsts.BondDenom, int64(blobSize*params.GasPerBlobByte))
}

// validateValidatorSignatures validates validator signatures using the existing SignatureSet infrastructure
func (ms msgServer) validateValidatorSignatures(ctx sdk.Context, paymentPromise *types.PaymentPromise, signatures [][]byte) error {
	// Get historical validator set at the height specified in the payment promise
	historicalInfo, err := ms.stakingKeeper.GetHistoricalInfo(ctx, paymentPromise.Height)
	if err != nil {
		return errorsmod.Wrapf(err, "failed to get historical validator set at height %d", paymentPromise.Height)
	}

	// Convert SDK validators to CometBFT validators
	cmtValidators := make([]*core.Validator, len(historicalInfo.Valset))
	for i, val := range historicalInfo.Valset {
		consPubKey, err := val.ConsPubKey()
		if err != nil {
			return errorsmod.Wrapf(err, "failed to get consensus public key for validator %s", val.GetOperator())
		}

		// Create CometBFT ed25519 public key from bytes
		pubKeyBytes := consPubKey.Bytes()
		if len(pubKeyBytes) != ed25519.PubKeySize {
			return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "invalid ed25519 public key size for validator %s", val.GetOperator())
		}

		cmtPubKey := ed25519.PubKey(pubKeyBytes)
		cmtValidators[i] = core.NewValidator(cmtPubKey, val.Tokens.Int64())
	}

	// Create validator set
	cmtValSet := core.NewValidatorSet(cmtValidators)
	valSet := validator.Set{
		ValidatorSet: cmtValSet,
		Height:       uint64(paymentPromise.Height),
	}

	// Convert payment promise to get sign bytes
	pp := fibre.PaymentPromise{}
	if err := pp.FromProto(paymentPromise); err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "failed to convert payment promise: %s", err)
	}

	signBytes, err := pp.SignBytes()
	if err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "failed to get sign bytes: %s", err)
	}

	// Create signature set with 2/3+ thresholds
	twoThirds := cmtmath.Fraction{Numerator: 2, Denominator: 3}
	sigSet := valSet.NewSignatureSet(twoThirds, twoThirds, signBytes)

	// Add all provided signatures to the signature set
	for i, signature := range signatures {
		if len(signature) == 0 {
			continue // Skip empty signatures
		}

		if i >= len(cmtValidators) {
			return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "signature index %d exceeds validator count %d", i, len(cmtValidators))
		}

		// Add signature to set (this validates the signature internally)
		if err := sigSet.Add(cmtValidators[i], signature); err != nil {
			return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "invalid signature at index %d: %s", i, err)
		}
	}

	// Check if thresholds are met
	_, err = sigSet.Signatures()
	if err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "signature validation failed: %s", err)
	}

	return nil
}
