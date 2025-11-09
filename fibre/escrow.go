package fibre

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// ensureEscrowFunds makes sure the caller's escrow account exists and has enough
// available balance to pay for an upload of the provided size.
// It broadcasts and confirms a deposit transaction when necessary.
func (c *Client) ensureEscrowFunds(ctx context.Context, uploadSize int) error {
	if !c.cfg.AutoFundEscrow {
		return nil
	}
	if c.txClient == nil {
		return fmt.Errorf("tx client is not configured; Put requires a transaction client")
	}
	if c.queryClient == nil {
		return fmt.Errorf("query client is not configured; cannot auto-fund escrow")
	}

	paramsResp, err := c.queryClient.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		return fmt.Errorf("querying fibre params: %w", err)
	}

	required := requiredPaymentAmount(uploadSize, paramsResp.Params.GasPerBlobByte)
	if !required.Amount.IsPositive() {
		return nil
	}

	signer := c.txClient.DefaultAddress().String()
	escrowResp, err := c.queryClient.EscrowAccount(ctx, &types.QueryEscrowAccountRequest{Signer: signer})
	if err != nil {
		return fmt.Errorf("querying escrow account: %w", err)
	}

	available := zeroCoin(required.Denom)
	if escrowResp.Found && escrowResp.EscrowAccount != nil {
		balance := escrowResp.EscrowAccount.AvailableBalance
		if balance.Denom == required.Denom {
			available = balance
		} else {
			c.log.WarnContext(ctx, "escrow balance denom mismatch",
				"expected", required.Denom,
				"actual", balance.Denom,
			)
		}
	}

	if available.IsGTE(required) {
		return nil
	}

	depositAmount := required.Sub(available)
	c.log.InfoContext(ctx, "funding fibre escrow account",
		"signer", signer,
		"required", required,
		"available", available,
		"deposit", depositAmount,
	)

	msg := &types.MsgDepositToEscrow{Signer: signer, Amount: depositAmount}
	txResp, err := c.txClient.BroadcastTx(ctx, []sdk.Msg{msg})
	if err != nil {
		return fmt.Errorf("broadcasting deposit transaction: %w", err)
	}
	if _, err := c.txClient.ConfirmTx(ctx, txResp.TxHash); err != nil {
		return fmt.Errorf("confirming deposit transaction: %w", err)
	}

	return nil
}

func zeroCoin(denom string) sdk.Coin {
	return sdk.NewCoin(denom, sdkmath.ZeroInt())
}

func requiredPaymentAmount(uploadSize int, gasPerByte uint32) sdk.Coin {
	if uploadSize <= 0 || gasPerByte == 0 {
		return sdk.NewCoin(appconsts.BondDenom, sdkmath.ZeroInt())
	}
	amount := sdkmath.NewIntFromUint64(uint64(uploadSize) * uint64(gasPerByte))
	return sdk.NewCoin(appconsts.BondDenom, amount)
}
