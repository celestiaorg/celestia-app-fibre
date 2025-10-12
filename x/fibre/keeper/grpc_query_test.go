package keeper_test

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
)

func (suite *KeeperTestSuite) TestQueryParams() {
	// Test successful params query
	req := &types.QueryParamsRequest{}
	resp, err := suite.keeper.Params(suite.ctx, req)
	suite.NoError(err)
	suite.NotNil(resp)
	suite.Equal(types.DefaultParams(), resp.Params)

	// Test nil request
	_, err = suite.keeper.Params(suite.ctx, nil)
	suite.Error(err)
}

func (suite *KeeperTestSuite) TestQueryEscrowAccount() {
	signer := "celestia1abc123def456ghi789jkl012mno345pqr678st"

	// Create escrow account
	account := types.EscrowAccount{
		Signer:           signer,
		Balance:          sdk.NewInt64Coin("utia", 1000),
		AvailableBalance: sdk.NewInt64Coin("utia", 800),
	}
	suite.keeper.SetEscrowAccount(suite.ctx, account)

	// Test successful query
	req := &types.QueryEscrowAccountRequest{Signer: signer}
	resp, err := suite.keeper.EscrowAccount(suite.ctx, req)
	suite.NoError(err)
	suite.NotNil(resp)
	suite.Equal(account.Signer, resp.EscrowAccount.Signer)
	suite.Equal(account.Balance, resp.EscrowAccount.Balance)
	suite.Equal(account.AvailableBalance, resp.EscrowAccount.AvailableBalance)

	// Test non-existent account
	nonExistentSigner := "celestia1nonexistent123456789012345678901234567"
	req.Signer = nonExistentSigner
	_, err = suite.keeper.EscrowAccount(suite.ctx, req)
	suite.Error(err)
	suite.Contains(err.Error(), "escrow account not found")

	// Test nil request
	_, err = suite.keeper.EscrowAccount(suite.ctx, nil)
	suite.Error(err)

	// Test empty signer
	req = &types.QueryEscrowAccountRequest{Signer: ""}
	_, err = suite.keeper.EscrowAccount(suite.ctx, req)
	suite.Error(err)
	suite.Contains(err.Error(), "signer address cannot be empty")

	// Test invalid signer address
	req.Signer = "invalid_address"
	_, err = suite.keeper.EscrowAccount(suite.ctx, req)
	suite.Error(err)
	suite.Contains(err.Error(), "invalid signer address")
}

func (suite *KeeperTestSuite) TestQueryWithdrawals() {
	signer := "celestia1abc123def456ghi789jkl012mno345pqr678st"

	// Create some withdrawals
	testTime1 := suite.ctx.BlockTime()
	testTime2 := suite.ctx.BlockTime().Add(1 * time.Hour)

	withdrawal1 := types.Withdrawal{
		Signer:             signer,
		Amount:             sdk.NewInt64Coin("utia", 100),
		RequestedTimestamp: testTime1,
	}
	withdrawal2 := types.Withdrawal{
		Signer:             signer,
		Amount:             sdk.NewInt64Coin("utia", 200),
		RequestedTimestamp: testTime2,
	}

	suite.keeper.SetWithdrawal(suite.ctx, withdrawal1)
	suite.keeper.SetWithdrawal(suite.ctx, withdrawal2)

	// Test successful query
	req := &types.QueryWithdrawalsRequest{Signer: signer}
	resp, err := suite.keeper.Withdrawals(suite.ctx, req)
	suite.NoError(err)
	suite.NotNil(resp)
	suite.Len(resp.Withdrawals, 2)

	// Verify withdrawals are present (order may vary)
	withdrawalAmounts := make(map[int64]bool)
	for _, w := range resp.Withdrawals {
		withdrawalAmounts[w.Amount.Amount.Int64()] = true
	}
	suite.True(withdrawalAmounts[100])
	suite.True(withdrawalAmounts[200])

	// Test signer with no withdrawals
	emptySigner := "celestia1empty123456789012345678901234567890123"
	req.Signer = emptySigner
	resp, err = suite.keeper.Withdrawals(suite.ctx, req)
	suite.NoError(err)
	suite.NotNil(resp)
	suite.Len(resp.Withdrawals, 0)

	// Test nil request
	_, err = suite.keeper.Withdrawals(suite.ctx, nil)
	suite.Error(err)

	// Test empty signer
	req = &types.QueryWithdrawalsRequest{Signer: ""}
	_, err = suite.keeper.Withdrawals(suite.ctx, req)
	suite.Error(err)
	suite.Contains(err.Error(), "signer address cannot be empty")

	// Test invalid signer address
	req.Signer = "invalid_address"
	_, err = suite.keeper.Withdrawals(suite.ctx, req)
	suite.Error(err)
	suite.Contains(err.Error(), "invalid signer address")
}

func (suite *KeeperTestSuite) TestQueryProcessedPaymentPromise() {
	// Create a simple payment promise for testing
	// Note: This is a simplified version. In practice, you'd need proper cryptographic setup
	promise := &types.PaymentPromise{
		Namespace:  make([]byte, 29), // Valid namespace size
		BlobSize:   1000,
		Commitment: make([]byte, 32), // Valid commitment size
		RowVersion: 0,
		Height:     100,
		ChainId:    "test-chain",
	}

	// Test unprocessed payment promise
	hash := suite.keeper.GetPaymentPromiseHash(promise)
	req := &types.QueryProcessedPaymentPromiseRequest{PromiseHash: hash}
	resp, err := suite.keeper.ProcessedPaymentPromise(suite.ctx, req)
	suite.NoError(err)
	suite.NotNil(resp)
	suite.False(resp.Found)

	// Mark as processed
	processedTime := suite.ctx.BlockTime()
	suite.keeper.SetPaymentPromiseProcessed(suite.ctx, hash, processedTime)

	// Test processed payment promise
	resp, err = suite.keeper.ProcessedPaymentPromise(suite.ctx, req)
	suite.NoError(err)
	suite.NotNil(resp)
	suite.True(resp.Found)

	// Test nil request
	_, err = suite.keeper.ProcessedPaymentPromise(suite.ctx, nil)
	suite.Error(err)

	// Test empty promise hash
	req = &types.QueryProcessedPaymentPromiseRequest{PromiseHash: nil}
	_, err = suite.keeper.ProcessedPaymentPromise(suite.ctx, req)
	suite.Error(err)
	suite.Contains(err.Error(), "promise hash cannot be empty")
}
