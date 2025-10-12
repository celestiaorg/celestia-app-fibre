package keeper_test

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/keeper"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
)

func (suite *KeeperTestSuite) TestMsgDepositToEscrow() {
	msgServer := keeper.NewMsgServerImpl(*suite.keeper)
	signer := "celestia1abc123def456ghi789jkl012mno345pqr678st"
	amount := sdk.NewInt64Coin("utia", 1000)

	msg := &types.MsgDepositToEscrow{
		Signer: signer,
		Amount: amount,
	}

	// Test successful deposit
	resp, err := msgServer.DepositToEscrow(suite.ctx, msg)
	suite.NoError(err)
	suite.NotNil(resp)

	// Verify escrow account was created/updated
	escrowAccount, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(signer, escrowAccount.Signer)
	suite.Equal(amount, escrowAccount.Balance)
	suite.Equal(amount, escrowAccount.AvailableBalance)

	// Test deposit to existing account
	additionalAmount := sdk.NewInt64Coin("utia", 500)
	msg.Amount = additionalAmount

	resp, err = msgServer.DepositToEscrow(suite.ctx, msg)
	suite.NoError(err)
	suite.NotNil(resp)

	// Verify balances were updated
	escrowAccount, found = suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	expectedBalance := amount.Add(additionalAmount)
	suite.Equal(expectedBalance, escrowAccount.Balance)
	suite.Equal(expectedBalance, escrowAccount.AvailableBalance)
}

func (suite *KeeperTestSuite) TestMsgRequestWithdrawal() {
	msgServer := keeper.NewMsgServerImpl(*suite.keeper)
	signer := "celestia1abc123def456ghi789jkl012mno345pqr678st"

	// First, create an escrow account with funds
	escrowAccount := types.EscrowAccount{
		Signer:           signer,
		Balance:          sdk.NewInt64Coin("utia", 1000),
		AvailableBalance: sdk.NewInt64Coin("utia", 1000),
	}
	suite.keeper.SetEscrowAccount(suite.ctx, escrowAccount)

	withdrawalAmount := sdk.NewInt64Coin("utia", 300)
	msg := &types.MsgRequestWithdrawal{
		Signer: signer,
		Amount: withdrawalAmount,
	}

	// Test successful withdrawal request
	resp, err := msgServer.RequestWithdrawal(suite.ctx, msg)
	suite.NoError(err)
	suite.NotNil(resp)

	// Verify escrow account available balance was reduced
	updatedAccount, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(sdk.NewInt64Coin("utia", 1000), updatedAccount.Balance)         // Total balance unchanged
	suite.Equal(sdk.NewInt64Coin("utia", 700), updatedAccount.AvailableBalance) // Available reduced

	// Verify withdrawal was created (we need to get it by checking withdrawals by signer)
	withdrawals := suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Len(withdrawals, 1)
	suite.Equal(signer, withdrawals[0].Signer)
	suite.Equal(withdrawalAmount, withdrawals[0].Amount)

	// Test insufficient balance
	largeAmount := sdk.NewInt64Coin("utia", 800) // More than available (700)
	msg.Amount = largeAmount

	_, err = msgServer.RequestWithdrawal(suite.ctx, msg)
	suite.Error(err)
	suite.Contains(err.Error(), "insufficient available balance")

	// Test non-existent escrow account
	nonExistentSigner := "celestia1nonexistent123456789012345678901234567"
	msg.Signer = nonExistentSigner
	msg.Amount = sdk.NewInt64Coin("utia", 100)

	_, err = msgServer.RequestWithdrawal(suite.ctx, msg)
	suite.Error(err)
	suite.Contains(err.Error(), "escrow account not found")
}

func (suite *KeeperTestSuite) TestMsgUpdateFibreParams() {
	msgServer := keeper.NewMsgServerImpl(*suite.keeper)
	authority := suite.keeper.GetAuthority()

	newParams := types.NewParams(
		5,            // GasPerBlobByte
		48*time.Hour, // WithdrawalDelay
		2*time.Hour,  // PaymentPromiseTimeout
		72*time.Hour, // PaymentPromiseRetentionWindow
	)

	msg := &types.MsgUpdateFibreParams{
		Authority: authority,
		Params:    newParams,
	}

	// Test successful params update
	resp, err := msgServer.UpdateFibreParams(suite.ctx, msg)
	suite.NoError(err)
	suite.NotNil(resp)

	// Verify params were updated
	updatedParams := suite.keeper.GetParams(suite.ctx)
	suite.Equal(newParams.GasPerBlobByte, updatedParams.GasPerBlobByte)
	suite.Equal(newParams.WithdrawalDelay, updatedParams.WithdrawalDelay)
	suite.Equal(newParams.PaymentPromiseTimeout, updatedParams.PaymentPromiseTimeout)
	suite.Equal(newParams.PaymentPromiseRetentionWindow, updatedParams.PaymentPromiseRetentionWindow)

	// Test unauthorized update
	msg.Authority = "celestia1unauthorized123456789012345678901234567"
	_, err = msgServer.UpdateFibreParams(suite.ctx, msg)
	suite.Error(err)
	suite.Contains(err.Error(), "invalid authority")

	// Test invalid params
	msg.Authority = authority
	invalidParams := types.Params{
		GasPerBlobByte: 0, // Invalid: cannot be zero
	}
	msg.Params = invalidParams

	_, err = msgServer.UpdateFibreParams(suite.ctx, msg)
	suite.Error(err)
	suite.Contains(err.Error(), "invalid params")
}
