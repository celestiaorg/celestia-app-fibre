package keeper

import (
	"context"
	"fmt"
	"time"

	"cosmossdk.io/math"
	fibre "github.com/celestiaorg/celestia-app/v6/fibre"
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

// ProcessedPaymentPromise queries whether a payment promise has been processed.
func (k Keeper) ProcessedPaymentPromise(c context.Context, req *types.QueryProcessedPaymentPromiseRequest) (*types.QueryProcessedPaymentPromiseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	if len(req.PromiseHash) == 0 {
		return nil, status.Error(codes.InvalidArgument, "payment promise hash cannot be empty")
	}

	ctx := sdk.UnwrapSDKContext(c)
	processedPayment, found := k.GetProcessedPayment(ctx, req.PromiseHash)

	var processedAt *time.Time
	if found {
		processedAt = &processedPayment.ProcessedAt
	}

	return &types.QueryProcessedPaymentPromiseResponse{
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

	// Convert proto to fibre PaymentPromise for validation
	pp := fibre.PaymentPromise{}
	if err := pp.FromProto(&req.Promise); err != nil {
		return &types.QueryValidatePaymentPromiseResponse{
			Valid:        false,
			ErrorMessage: fmt.Sprintf("invalid payment promise format: %v", err),
		}, nil
	}

	// Validate the payment promise
	if err := pp.Validate(); err != nil {
		return &types.QueryValidatePaymentPromiseResponse{
			Valid:        false,
			ErrorMessage: err.Error(),
		}, nil
	}

	// Check if the payment promise has already been processed
	isProcessed := k.IsPaymentProcessed(ctx, &req.Promise)

	// Get signer address from public key
	signerAddr := sdk.AccAddress(req.Promise.SignerPublicKey.Address())
	signerAddrStr := signerAddr.String()

	// Additional validation: check if the escrow account exists and has sufficient balance
	escrowAccount, found := k.GetEscrowAccount(ctx, signerAddrStr)
	if !found {
		return &types.QueryValidatePaymentPromiseResponse{
			Valid:             false,
			ErrorMessage:      "escrow account not found for signer",
			AlreadyProcessed:  isProcessed,
			SufficientBalance: false,
		}, nil
	}

	// Calculate required payment based on blob size and gas parameters
	params := k.GetParams(ctx)
	gasRequired := uint64(req.Promise.BlobSize) * uint64(params.GasPerBlobByte)

	// For simplicity, assume 1 gas = 1 utia (this should be configurable in a real implementation)
	// In a real implementation, you'd need to get the gas price from somewhere
	requiredAmount := sdk.NewCoin("utia", math.NewInt(int64(gasRequired)))

	// Check if the escrow account has sufficient balance
	hasSufficientBalance := escrowAccount.AvailableBalance.IsGTE(requiredAmount)

	isValid := !isProcessed && hasSufficientBalance && found
	errorMessage := ""
	if isProcessed {
		errorMessage = "payment promise has already been processed"
	} else if !hasSufficientBalance {
		errorMessage = "insufficient balance in escrow account"
	}

	return &types.QueryValidatePaymentPromiseResponse{
		Valid:             isValid,
		ErrorMessage:      errorMessage,
		SufficientBalance: hasSufficientBalance,
		AlreadyProcessed:  isProcessed,
		RequiredPayment:   requiredAmount,
		AvailableBalance:  escrowAccount.AvailableBalance,
	}, nil
}
