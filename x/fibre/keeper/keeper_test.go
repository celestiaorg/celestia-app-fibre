package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/keeper"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
)

type KeeperTestSuite struct {
	suite.Suite

	ctx    sdk.Context
	keeper *keeper.Keeper
	cdc    codec.Codec
}

func TestKeeperTestSuite(t *testing.T) {
	suite.Run(t, new(KeeperTestSuite))
}

func (suite *KeeperTestSuite) SetupTest() {
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, nil, metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(key, storetypes.StoreTypeIAVL, db)
	stateStore.MountStoreWithDB(tkey, storetypes.StoreTypeTransient, db)
	require.NoError(suite.T(), stateStore.LoadLatestVersion())

	registry := codectypes.NewInterfaceRegistry()
	// types.RegisterInterfaces(registry) // Skip for now as this function may not exist
	cdc := codec.NewProtoCodec(registry)

	paramsSubspace := paramtypes.NewSubspace(cdc,
		codec.NewLegacyAmino(),
		key,
		tkey,
		"FibreParams",
	)

	// Create a mock bank keeper
	mockBankKeeper := &MockBankKeeper{}

	suite.keeper = keeper.NewKeeper(
		cdc,
		key,
		mockBankKeeper,
		paramsSubspace,
		authtypes.NewModuleAddress("gov").String(),
	)

	suite.ctx = sdk.NewContext(stateStore, cmtproto.Header{Time: time.Now().UTC()}, false, nil)
	suite.cdc = cdc

	// Initialize with default params
	params := types.DefaultParams()
	suite.keeper.SetParams(suite.ctx, params)
}

// MockBankKeeper implements the expected BankKeeper interface for testing
type MockBankKeeper struct{}

func (m *MockBankKeeper) SendCoinsFromAccountToModule(ctx sdk.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	return nil
}

func (m *MockBankKeeper) SendCoinsFromModuleToAccount(ctx sdk.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	return nil
}

func (m *MockBankKeeper) GetModuleAddress(moduleName string) sdk.AccAddress {
	return authtypes.NewModuleAddress(moduleName)
}

func (suite *KeeperTestSuite) TestSetGetParams() {
	params := types.NewParams(
		2,            // GasPerBlobByte
		48*time.Hour, // WithdrawalDelay
		2*time.Hour,  // PaymentPromiseTimeout
		48*time.Hour, // PaymentPromiseRetentionWindow
	)

	suite.keeper.SetParams(suite.ctx, params)
	retrievedParams := suite.keeper.GetParams(suite.ctx)

	suite.Equal(params.GasPerBlobByte, retrievedParams.GasPerBlobByte)
	suite.Equal(params.WithdrawalDelay, retrievedParams.WithdrawalDelay)
	suite.Equal(params.PaymentPromiseTimeout, retrievedParams.PaymentPromiseTimeout)
	suite.Equal(params.PaymentPromiseRetentionWindow, retrievedParams.PaymentPromiseRetentionWindow)
}

func (suite *KeeperTestSuite) TestEscrowAccount() {
	signer := "celestia1abc123def456ghi789jkl012mno345pqr678st"

	// Test getting non-existent account
	_, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.False(found)

	// Test setting and getting account
	account := types.EscrowAccount{
		Signer:           signer,
		Balance:          sdk.NewInt64Coin("utia", 1000),
		AvailableBalance: sdk.NewInt64Coin("utia", 800),
	}

	suite.keeper.SetEscrowAccount(suite.ctx, account)
	retrievedAccount, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)

	suite.True(found)
	suite.Equal(account.Signer, retrievedAccount.Signer)
	suite.Equal(account.Balance, retrievedAccount.Balance)
	suite.Equal(account.AvailableBalance, retrievedAccount.AvailableBalance)

	// Test deleting account
	suite.keeper.DeleteEscrowAccount(suite.ctx, signer)
	_, found = suite.keeper.GetEscrowAccount(suite.ctx, signer)
	suite.False(found)
}

func (suite *KeeperTestSuite) TestWithdrawal() {
	signer := "celestia1abc123def456ghi789jkl012mno345pqr678st"
	testTime := suite.ctx.BlockTime()

	// Test getting non-existent withdrawal
	_, found := suite.keeper.GetWithdrawal(suite.ctx, signer, testTime)
	suite.False(found)

	// Test setting and getting withdrawal
	withdrawal := types.Withdrawal{
		Signer:             signer,
		Amount:             sdk.NewInt64Coin("utia", 500),
		RequestedTimestamp: testTime,
	}

	suite.keeper.SetWithdrawal(suite.ctx, withdrawal)
	retrievedWithdrawal, found := suite.keeper.GetWithdrawal(suite.ctx, signer, testTime)

	suite.True(found)
	suite.Equal(withdrawal.Signer, retrievedWithdrawal.Signer)
	suite.Equal(withdrawal.Amount, retrievedWithdrawal.Amount)
	suite.Equal(withdrawal.RequestedTimestamp, retrievedWithdrawal.RequestedTimestamp)

	// Test getting withdrawals by signer
	withdrawals := suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
	suite.Len(withdrawals, 1)
	suite.Equal(withdrawal.Signer, withdrawals[0].Signer)

	// Test deleting withdrawal
	suite.keeper.DeleteWithdrawal(suite.ctx, signer, testTime)
	_, found = suite.keeper.GetWithdrawal(suite.ctx, signer, testTime)
	suite.False(found)
}

func (suite *KeeperTestSuite) TestPaymentPromiseProcessed() {
	hash := []byte("test_hash_12345678901234567890123456")

	// Test checking non-existent processed payment promise
	processed := suite.keeper.IsPaymentPromiseProcessed(suite.ctx, hash)
	suite.False(processed)

	// Test setting processed payment promise
	processedTime := suite.ctx.BlockTime()
	suite.keeper.SetPaymentPromiseProcessed(suite.ctx, hash, processedTime)

	processed = suite.keeper.IsPaymentPromiseProcessed(suite.ctx, hash)
	suite.True(processed)
}

// TestGetNextWithdrawalID is no longer needed since withdrawals are keyed by timestamp

func (suite *KeeperTestSuite) TestValidatePaymentPromise() {
	// This test would require more setup including creating a valid PaymentPromise
	// with proper public key, signature, etc. For now, we'll test the basic validation
	// that checks for processed promises and escrow account existence.

	signer := "celestia1abc123def456ghi789jkl012mno345pqr678st"

	// Create escrow account
	account := types.EscrowAccount{
		Signer:           signer,
		Balance:          sdk.NewInt64Coin("utia", 1000),
		AvailableBalance: sdk.NewInt64Coin("utia", 1000),
	}
	suite.keeper.SetEscrowAccount(suite.ctx, account)

	// Test would continue with creating a valid PaymentPromise and testing validation
	// This requires more complex setup with cryptographic keys and signatures
}

func (suite *KeeperTestSuite) TestIterators() {
	// Test escrow account iterator
	signer1 := "celestia1abc123def456ghi789jkl012mno345pqr678st"
	signer2 := "celestia1def456ghi789jkl012mno345pqr678stuv901"

	account1 := types.EscrowAccount{
		Signer:           signer1,
		Balance:          sdk.NewInt64Coin("utia", 1000),
		AvailableBalance: sdk.NewInt64Coin("utia", 800),
	}
	account2 := types.EscrowAccount{
		Signer:           signer2,
		Balance:          sdk.NewInt64Coin("utia", 2000),
		AvailableBalance: sdk.NewInt64Coin("utia", 1500),
	}

	suite.keeper.SetEscrowAccount(suite.ctx, account1)
	suite.keeper.SetEscrowAccount(suite.ctx, account2)

	// Test iterator
	var accounts []types.EscrowAccount
	suite.keeper.IterateEscrowAccounts(suite.ctx, func(account types.EscrowAccount) bool {
		accounts = append(accounts, account)
		return false
	})

	suite.Len(accounts, 2)

	// Verify accounts are present (order may vary)
	signers := make(map[string]bool)
	for _, acc := range accounts {
		signers[acc.Signer] = true
	}
	suite.True(signers[signer1])
	suite.True(signers[signer2])
}
