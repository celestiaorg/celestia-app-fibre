package keeper

import (
	"context"
	"time"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ types.QueryServer = Keeper{}

// Params queries the parameters of the fibre module.
func (k Keeper) Params(c context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	ctx := sdk.UnwrapSDKContext(c)
	params := k.GetParams(ctx)

	return &types.QueryParamsResponse{Params: params}, nil
}

// EscrowAccount queries an escrow account by signer address.
func (k Keeper) EscrowAccount(c context.Context, req *types.QueryEscrowAccountRequest) (*types.QueryEscrowAccountResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	if req.Signer == "" {
		return nil, status.Error(codes.InvalidArgument, "signer address cannot be empty")
	}

	ctx := sdk.UnwrapSDKContext(c)
	account, found := k.GetEscrowAccount(ctx, req.Signer)

	return &types.QueryEscrowAccountResponse{
		EscrowAccount: &account,
		Found:         found,
	}, nil
}

// Withdrawals queries all withdrawals for an escrow account by signer address.
func (k Keeper) Withdrawals(c context.Context, req *types.QueryWithdrawalsRequest) (*types.QueryWithdrawalsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	if req.Signer == "" {
		return nil, status.Error(codes.InvalidArgument, "signer address cannot be empty")
	}

	ctx := sdk.UnwrapSDKContext(c)
	withdrawals := k.GetWithdrawalsBySigner(ctx, req.Signer)

	return &types.QueryWithdrawalsResponse{Withdrawals: withdrawals}, nil
}

// IsPaymentProcessed queries whether a payment promise has been processed.
func (k Keeper) IsPaymentProcessed(c context.Context, req *types.QueryIsPaymentProcessedRequest) (*types.QueryIsPaymentProcessedResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	if len(req.PromiseHash) == 0 {
		return nil, status.Error(codes.InvalidArgument, "payment promise hash cannot be empty")
	}

	ctx := sdk.UnwrapSDKContext(c)

	// Delegate to keeper method
	found := k.IsPaymentProcessedByHash(ctx, req.PromiseHash)

	var processedAt *time.Time
	if found {
		processedPayment, _ := k.GetProcessedPayment(ctx, req.PromiseHash)
		processedAt = &processedPayment.ProcessedAt
	}

	return &types.QueryIsPaymentProcessedResponse{
		ProcessedAt: processedAt,
		Found:       found,
	}, nil
}

// ValidatePaymentPromise validates a payment promise for server use.
func (k Keeper) ValidatePaymentPromise(c context.Context, req *types.QueryValidatePaymentPromiseRequest) (*types.QueryValidatePaymentPromiseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	ctx := sdk.UnwrapSDKContext(c)

	// Delegate to keeper method
	isValid, errorMessage, hasSufficientBalance, isProcessed, requiredAmount, availableBalance := k.ValidatePaymentPromiseInternal(ctx, &req.Promise)

	return &types.QueryValidatePaymentPromiseResponse{
		Valid:             isValid,
		ErrorMessage:      errorMessage,
		SufficientBalance: hasSufficientBalance,
		AlreadyProcessed:  isProcessed,
		RequiredPayment:   requiredAmount,
		AvailableBalance:  availableBalance,
	}, nil
}
