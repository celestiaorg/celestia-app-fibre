package keeper

import (
	"context"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ types.QueryServer = Keeper{}

// Params queries the parameters of the fibre module.
func (k Keeper) Params(goCtx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	params := k.GetParams(ctx)

	return &types.QueryParamsResponse{Params: params}, nil
}

// EscrowAccount queries an escrow account by signer address.
func (k Keeper) EscrowAccount(goCtx context.Context, req *types.QueryEscrowAccountRequest) (*types.QueryEscrowAccountResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	if req.Signer == "" {
		return nil, status.Error(codes.InvalidArgument, "signer address cannot be empty")
	}

	// Validate signer address
	if _, err := sdk.AccAddressFromBech32(req.Signer); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid signer address")
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	escrowAccount, found := k.GetEscrowAccount(ctx, req.Signer)
	if !found {
		return nil, status.Error(codes.NotFound, "escrow account not found")
	}

	return &types.QueryEscrowAccountResponse{EscrowAccount: &escrowAccount, Found: true}, nil
}

// Withdrawals queries all withdrawals for an escrow account by signer address.
func (k Keeper) Withdrawals(goCtx context.Context, req *types.QueryWithdrawalsRequest) (*types.QueryWithdrawalsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	if req.Signer == "" {
		return nil, status.Error(codes.InvalidArgument, "signer address cannot be empty")
	}

	// Validate signer address
	if _, err := sdk.AccAddressFromBech32(req.Signer); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid signer address")
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	withdrawals := k.GetWithdrawalsBySigner(ctx, req.Signer)

	return &types.QueryWithdrawalsResponse{Withdrawals: withdrawals}, nil
}

// ProcessedPaymentPromise queries whether a payment promise has been processed.
func (k Keeper) ProcessedPaymentPromise(goCtx context.Context, req *types.QueryProcessedPaymentPromiseRequest) (*types.QueryProcessedPaymentPromiseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	if len(req.PromiseHash) == 0 {
		return nil, status.Error(codes.InvalidArgument, "promise hash cannot be empty")
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	processed := k.IsPaymentPromiseProcessed(ctx, req.PromiseHash)

	if !processed {
		return &types.QueryProcessedPaymentPromiseResponse{Found: false}, nil
	}

	// If processed, we could return the processed timestamp, but for now just return found=true
	return &types.QueryProcessedPaymentPromiseResponse{Found: true}, nil
}

// ValidatePaymentPromise validates a payment promise for server use.
func (k Keeper) ValidatePaymentPromise(goCtx context.Context, req *types.QueryValidatePaymentPromiseRequest) (*types.QueryValidatePaymentPromiseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	// Validate payment promise basic structure
	if err := req.Promise.ValidateBasic(); err != nil {
		return &types.QueryValidatePaymentPromiseResponse{
			Valid:        false,
			ErrorMessage: err.Error(),
		}, nil
	}

	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate payment promise against current state
	if err := k.ValidatePaymentPromiseInternal(ctx, &req.Promise); err != nil {
		return &types.QueryValidatePaymentPromiseResponse{
			Valid:        false,
			ErrorMessage: err.Error(),
		}, nil
	}

	// Get signer address from public key
	pubKey, ok := req.Promise.SignerPublicKey.GetCachedValue().(cryptotypes.PubKey)
	if !ok {
		return &types.QueryValidatePaymentPromiseResponse{
			Valid:        false,
			ErrorMessage: "failed to get cached public key",
		}, nil
	}
	signerAddr := sdk.AccAddress(pubKey.Address())

	// Get escrow account to return balance information
	escrowAccount, found := k.GetEscrowAccount(ctx, signerAddr.String())
	if !found {
		return &types.QueryValidatePaymentPromiseResponse{
			Valid:        false,
			ErrorMessage: "escrow account not found",
		}, nil
	}

	// Calculate required payment
	params := k.GetParams(ctx)
	requiredAmount := sdk.NewInt64Coin("utia", int64(req.Promise.BlobSize*params.GasPerBlobByte))

	// Check if already processed
	hash := k.GetPaymentPromiseHash(&req.Promise)
	alreadyProcessed := k.IsPaymentPromiseProcessed(ctx, hash)

	return &types.QueryValidatePaymentPromiseResponse{
		Valid:             true,
		ErrorMessage:      "",
		SufficientBalance: escrowAccount.AvailableBalance.IsGTE(requiredAmount),
		AlreadyProcessed:  alreadyProcessed,
		RequiredPayment:   requiredAmount,
		AvailableBalance:  escrowAccount.AvailableBalance,
	}, nil
}
