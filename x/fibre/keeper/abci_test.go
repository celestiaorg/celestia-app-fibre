package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/keeper"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ABCITestSuite struct {
	suite.Suite

	ctx        sdk.Context
	keeper     *keeper.Keeper
	msgServer  types.MsgServer
	cdc        codec.Codec
	bankKeeper *MockBankKeeper
}

func TestABCITestSuite(t *testing.T) {
	suite.Run(t, new(ABCITestSuite))
}

func (suite *ABCITestSuite) SetupTest() {
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	stateStore.MountStoreWithDB(tkey, storetypes.StoreTypeTransient, nil)
	require.NoError(suite.T(), stateStore.LoadLatestVersion())

	registry := codectypes.NewInterfaceRegistry()
	suite.cdc = codec.NewProtoCodec(registry)

	suite.bankKeeper = &MockBankKeeper{}
	mockStakingKeeper := &MockStakingKeeper{}
	authority := authtypes.NewModuleAddress("gov").String()
	suite.ctx = sdk.NewContext(stateStore, cmtproto.Header{Time: time.Now().UTC()}, false, nil)
	suite.keeper = keeper.NewKeeper(suite.cdc, storeKey, suite.bankKeeper, mockStakingKeeper, authority)
	suite.keeper.SetParams(suite.ctx, types.DefaultParams())
	suite.msgServer = keeper.NewMsgServerImpl(*suite.keeper)
}

func (suite *ABCITestSuite) TestBeginBlocker_ProcessAvailableWithdrawals() {
	// Create a test account
	privKey := secp256k1.GenPrivKey()
	signerAddr := sdk.AccAddress(privKey.PubKey().Address())
	signer := signerAddr.String()

	// Initial deposit
	depositAmount := sdk.NewCoin("utia", math.NewInt(1000000))

	// Deposit to escrow
	_, err := suite.msgServer.DepositToEscrow(suite.ctx, &types.MsgDepositToEscrow{
		Signer: signer,
		Amount: depositAmount,
	})
	suite.NoError(err)

	// Verify escrow account was created with correct balance
	escrowAccount, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(depositAmount, escrowAccount.Balance)
	suite.Equal(depositAmount, escrowAccount.AvailableBalance)

	// Request withdrawal
	withdrawalAmount := sdk.NewCoin("utia", math.NewInt(500000))
	_, err = suite.msgServer.RequestWithdrawal(suite.ctx, &types.MsgRequestWithdrawal{
		Signer: signer,
		Amount: withdrawalAmount,
	})
	suite.NoError(err)

	// Verify available balance was decreased but total balance unchanged
	escrowAccount, found = suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(depositAmount, escrowAccount.Balance)
	suite.Equal(depositAmount.Sub(withdrawalAmount), escrowAccount.AvailableBalance)

	// Advance time but not enough to process withdrawal
	params := suite.keeper.GetParams(suite.ctx)
	halfDelay := params.WithdrawalDelay / 2
	suite.ctx = suite.ctx.WithBlockTime(suite.ctx.BlockTime().Add(halfDelay))

	// Run BeginBlocker - should not process withdrawal yet
	err = suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify withdrawal still pending
	escrowAccount, found = suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(depositAmount, escrowAccount.Balance)
	suite.Equal(depositAmount.Sub(withdrawalAmount), escrowAccount.AvailableBalance)

	// Advance time past withdrawal delay
	suite.ctx = suite.ctx.WithBlockTime(suite.ctx.BlockTime().Add(params.WithdrawalDelay))

	// Run BeginBlocker - should process withdrawal now
	err = suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify escrow account balance was decreased
	escrowAccount, found = suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(depositAmount.Sub(withdrawalAmount), escrowAccount.Balance)
	suite.Equal(depositAmount.Sub(withdrawalAmount), escrowAccount.AvailableBalance)

	// Verify withdrawal was deleted from store
	withdrawals := suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Empty(withdrawals)
}

func (suite *ABCITestSuite) TestBeginBlocker_ProcessMultipleWithdrawals() {
	// Create multiple test accounts
	amounts := []math.Int{
		math.NewInt(1000000),
		math.NewInt(2000000),
		math.NewInt(3000000),
	}

	signers := make([]string, 3)
	for i := 0; i < 3; i++ {
		privKey := secp256k1.GenPrivKey()
		signerAddr := sdk.AccAddress(privKey.PubKey().Address())
		signers[i] = signerAddr.String()
	}

	// Deposit and request withdrawals for each account
	for i, signer := range signers {
		depositAmount := sdk.NewCoin("utia", amounts[i])

		_, err := suite.msgServer.DepositToEscrow(suite.ctx, &types.MsgDepositToEscrow{
			Signer: signer,
			Amount: depositAmount,
		})
		suite.NoError(err)

		withdrawalAmount := sdk.NewCoin("utia", amounts[i].QuoRaw(2))
		_, err = suite.msgServer.RequestWithdrawal(suite.ctx, &types.MsgRequestWithdrawal{
			Signer: signer,
			Amount: withdrawalAmount,
		})
		suite.NoError(err)

		// Add small delay between requests to test time ordering
		suite.ctx = suite.ctx.WithBlockTime(suite.ctx.BlockTime().Add(1 * time.Second))
	}

	// Advance time past withdrawal delay
	params := suite.keeper.GetParams(suite.ctx)
	suite.ctx = suite.ctx.WithBlockTime(suite.ctx.BlockTime().Add(params.WithdrawalDelay))

	// Run BeginBlocker - should process all withdrawals
	err := suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify all withdrawals were processed
	for i, signer := range signers {
		escrowAccount, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
		suite.True(found)
		expectedBalance := sdk.NewCoin("utia", amounts[i].QuoRaw(2))
		suite.Equal(expectedBalance, escrowAccount.Balance)
		suite.Equal(expectedBalance, escrowAccount.AvailableBalance)

		// Verify withdrawal was deleted
		withdrawals := suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
		suite.Empty(withdrawals)
	}
}

func (suite *ABCITestSuite) TestBeginBlocker_WithdrawalStaggeredTimes() {
	privKey := secp256k1.GenPrivKey()
	signerAddr := sdk.AccAddress(privKey.PubKey().Address())
	signer := signerAddr.String()

	// Deposit enough for multiple withdrawals
	depositAmount := sdk.NewCoin("utia", math.NewInt(3000000))

	_, err := suite.msgServer.DepositToEscrow(suite.ctx, &types.MsgDepositToEscrow{
		Signer: signer,
		Amount: depositAmount,
	})
	suite.NoError(err)

	// Request first withdrawal at time T
	withdrawal1 := sdk.NewCoin("utia", math.NewInt(1000000))
	time1 := suite.ctx.BlockTime()
	_, err = suite.msgServer.RequestWithdrawal(suite.ctx, &types.MsgRequestWithdrawal{
		Signer: signer,
		Amount: withdrawal1,
	})
	suite.NoError(err)

	// Request second withdrawal at time T+1h
	suite.ctx = suite.ctx.WithBlockTime(suite.ctx.BlockTime().Add(1 * time.Hour))
	withdrawal2 := sdk.NewCoin("utia", math.NewInt(1000000))
	time2 := suite.ctx.BlockTime()
	_, err = suite.msgServer.RequestWithdrawal(suite.ctx, &types.MsgRequestWithdrawal{
		Signer: signer,
		Amount: withdrawal2,
	})
	suite.NoError(err)

	// Advance to T+24h (first withdrawal becomes available)
	params := suite.keeper.GetParams(suite.ctx)
	suite.ctx = suite.ctx.WithBlockTime(time1.Add(params.WithdrawalDelay))

	// Run BeginBlocker - should process only first withdrawal
	err = suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify only first withdrawal processed
	escrowAccount, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	expectedBalance := depositAmount.Sub(withdrawal1)
	suite.Equal(expectedBalance, escrowAccount.Balance)
	expectedAvailable := expectedBalance.Sub(withdrawal2)
	suite.Equal(expectedAvailable, escrowAccount.AvailableBalance)

	// Verify one withdrawal still pending
	withdrawals := suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Len(withdrawals, 1)

	// Advance to T+25h (second withdrawal becomes available)
	suite.ctx = suite.ctx.WithBlockTime(time2.Add(params.WithdrawalDelay))

	// Run BeginBlocker - should process second withdrawal
	err = suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify second withdrawal processed
	escrowAccount, found = suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	expectedBalance = expectedBalance.Sub(withdrawal2)
	suite.Equal(expectedBalance, escrowAccount.Balance)
	suite.Equal(expectedBalance, escrowAccount.AvailableBalance)

	// Verify all withdrawals processed
	withdrawals = suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Empty(withdrawals)
}

func (suite *ABCITestSuite) TestBeginBlocker_WithdrawalDelayParamChange() {
	// This test verifies that withdrawals are processed correctly even if the
	// WithdrawalDelay parameter changes after the withdrawal is requested.
	// This is critical because we store the full Withdrawal struct (including
	// RequestedTimestamp) rather than computing it from availableAt - delay.

	privKey := secp256k1.GenPrivKey()
	signerAddr := sdk.AccAddress(privKey.PubKey().Address())
	signer := signerAddr.String()

	// Deposit funds
	depositAmount := sdk.NewCoin("utia", math.NewInt(2000000))
	_, err := suite.msgServer.DepositToEscrow(suite.ctx, &types.MsgDepositToEscrow{
		Signer: signer,
		Amount: depositAmount,
	})
	suite.NoError(err)

	// Request withdrawal with original delay (24 hours)
	withdrawalAmount := sdk.NewCoin("utia", math.NewInt(1000000))
	requestTime := suite.ctx.BlockTime()
	_, err = suite.msgServer.RequestWithdrawal(suite.ctx, &types.MsgRequestWithdrawal{
		Signer: signer,
		Amount: withdrawalAmount,
	})
	suite.NoError(err)

	// Verify withdrawal was created
	withdrawals := suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Len(withdrawals, 1)
	suite.Equal(requestTime, withdrawals[0].RequestedTimestamp)

	// CHANGE THE PARAMETER: Update withdrawal delay to 48 hours
	params := suite.keeper.GetParams(suite.ctx)
	originalDelay := params.WithdrawalDelay
	params.WithdrawalDelay = 48 * time.Hour
	suite.keeper.SetParams(suite.ctx, params)

	// Advance time to original availableAt (T + 24h)
	// The withdrawal should still be processed because we stored the actual RequestedTimestamp
	suite.ctx = suite.ctx.WithBlockTime(requestTime.Add(originalDelay))

	// Run BeginBlocker - should process withdrawal even though current delay is 48h
	err = suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify withdrawal was processed successfully
	escrowAccount, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(depositAmount.Sub(withdrawalAmount), escrowAccount.Balance)
	suite.Equal(depositAmount.Sub(withdrawalAmount), escrowAccount.AvailableBalance)

	// Verify withdrawal was deleted from store
	withdrawals = suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Empty(withdrawals, "withdrawal should be deleted after processing")

	// Request another withdrawal with the new 48h delay
	withdrawal2Amount := sdk.NewCoin("utia", math.NewInt(500000))
	requestTime2 := suite.ctx.BlockTime()
	_, err = suite.msgServer.RequestWithdrawal(suite.ctx, &types.MsgRequestWithdrawal{
		Signer: signer,
		Amount: withdrawal2Amount,
	})
	suite.NoError(err)

	// Advance time by 24 hours - should NOT process (needs 48h now)
	suite.ctx = suite.ctx.WithBlockTime(requestTime2.Add(24 * time.Hour))
	err = suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify withdrawal still pending
	withdrawals = suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Len(withdrawals, 1, "second withdrawal should still be pending after 24h")

	// Advance time by another 24 hours (total 48h) - should process now
	suite.ctx = suite.ctx.WithBlockTime(requestTime2.Add(48 * time.Hour))
	err = suite.keeper.BeginBlocker(suite.ctx)
	suite.NoError(err)

	// Verify second withdrawal was processed
	escrowAccount, found = suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.True(found)
	suite.Equal(depositAmount.Sub(withdrawalAmount).Sub(withdrawal2Amount), escrowAccount.Balance)

	// Verify all withdrawals processed
	withdrawals = suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Empty(withdrawals, "all withdrawals should be processed")
}
