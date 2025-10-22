package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	fibre "github.com/celestiaorg/celestia-app/v6/fibre"
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
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	stateStore.MountStoreWithDB(tkey, storetypes.StoreTypeTransient, nil)
	require.NoError(suite.T(), stateStore.LoadLatestVersion())

	registry := codectypes.NewInterfaceRegistry()
	suite.cdc = codec.NewProtoCodec(registry)

	mockBankKeeper := &MockBankKeeper{}
	authority := authtypes.NewModuleAddress("gov").String()
	suite.ctx = sdk.NewContext(stateStore, cmtproto.Header{Time: time.Now().UTC()}, false, nil)
	suite.keeper = keeper.NewKeeper(suite.cdc, storeKey, mockBankKeeper, authority)
	suite.keeper.SetParams(suite.ctx, types.DefaultParams())
}

func (suite *KeeperTestSuite) TestSetGetParams() {
	suite.T().Run("keeper should have default params", func(t *testing.T) {
		params := suite.keeper.GetParams(suite.ctx)
		suite.Equal(types.DefaultParams(), params)
	})

	suite.T().Run("keeper should set and get params", func(t *testing.T) {
		want := types.NewParams(
			2,            // GasPerBlobByte
			48*time.Hour, // WithdrawalDelay
			2*time.Hour,  // PaymentPromiseTimeout
			48*time.Hour, // PaymentPromiseRetentionWindow
		)
		suite.keeper.SetParams(suite.ctx, want)
		got := suite.keeper.GetParams(suite.ctx)
		suite.Equal(want, got)
	})
}

func (suite *KeeperTestSuite) TestEscrowAccount() {
	signer := "celestia15drmhzw5kwgenvemy30rqqqgq52axf5wwrruf7"

	suite.T().Run("keeper should return false for non-existent account", func(t *testing.T) {
		_, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
		suite.False(found)
	})

	suite.T().Run("keeper should set and get account", func(t *testing.T) {
		want := types.EscrowAccount{
			Signer:           signer,
			Balance:          sdk.NewInt64Coin("utia", 1000),
			AvailableBalance: sdk.NewInt64Coin("utia", 800),
		}

		suite.keeper.SetEscrowAccount(suite.ctx, want)
		got, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)

		suite.True(found)
		suite.Equal(want.Signer, got.Signer)
		suite.Equal(want.Balance, got.Balance)
		suite.Equal(want.AvailableBalance, got.AvailableBalance)
	})

	suite.T().Run("keeper should delete account", func(t *testing.T) {
		suite.keeper.DeleteEscrowAccount(suite.ctx, signer)
		_, found := suite.keeper.GetEscrowAccount(suite.ctx, signer)
		suite.False(found)
	})
}

func (suite *KeeperTestSuite) TestWithdrawal() {
	signer := "celestia15drmhzw5kwgenvemy30rqqqgq52axf5wwrruf7"
	testTime := suite.ctx.BlockTime()

	suite.T().Run("keeper should return false for non-existent withdrawal", func(t *testing.T) {
		_, found := suite.keeper.GetWithdrawal(suite.ctx, signer, testTime)
		suite.False(found)
	})

	suite.T().Run("keeper should set and get withdrawal", func(t *testing.T) {
		want := types.Withdrawal{
			Signer:             signer,
			Amount:             sdk.NewInt64Coin("utia", 500),
			RequestedTimestamp: testTime,
		}

		suite.keeper.SetWithdrawal(suite.ctx, want)
		got, found := suite.keeper.GetWithdrawal(suite.ctx, signer, testTime)

		suite.True(found)
		suite.Equal(want, got)
	})

	suite.T().Run("keeper should delete withdrawal", func(t *testing.T) {
		suite.keeper.DeleteWithdrawal(suite.ctx, signer, testTime)
		_, found := suite.keeper.GetWithdrawal(suite.ctx, signer, testTime)
		suite.False(found)
	})

	suite.T().Run("keeper should get withdrawals by signer", func(t *testing.T) {
		want := types.Withdrawal{
			Signer:             signer,
			Amount:             sdk.NewInt64Coin("utia", 100),
			RequestedTimestamp: testTime.Add(2 * time.Hour),
		}
		suite.keeper.SetWithdrawal(suite.ctx, want)
		withdrawals := suite.keeper.GetWithdrawalsBySigner(suite.ctx, signer)
		suite.Len(withdrawals, 1)
		suite.Equal(want, withdrawals[0])
	})
}

func (suite *KeeperTestSuite) TestSetPaymentPromiseEntry() {
	signerPublicKey := generatePubKey()
	paymentPromise := &types.PaymentPromise{
		ChainId:           "test-chain",
		Height:            100,
		Namespace:         make([]byte, 29),
		BlobSize:          1000,
		BlobVersion:       0,
		Commitment:        make([]byte, 32),
		CreationTimestamp: suite.ctx.BlockTime(),
		SignerPublicKey:   signerPublicKey,
		Signature:         make([]byte, 64),
	}

	isProcessed := suite.keeper.IsPaymentPromiseProcessed(suite.ctx, paymentPromise)
	suite.False(isProcessed)

	pp := fibre.PaymentPromise{}
	pp.FromProto(paymentPromise)
	paymentPromiseHash, err := pp.Hash()
	suite.NoError(err)

	suite.keeper.SetPaymentPromiseEntry(suite.ctx, types.PaymentPromiseEntry{
		PaymentPromiseHash: paymentPromiseHash,
		ProcessedAt:        suite.ctx.BlockTime(),
	})

	isProcessed = suite.keeper.IsPaymentPromiseProcessed(suite.ctx, paymentPromise)
	suite.True(isProcessed)
}

func generatePubKey() secp256k1.PubKey {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	return *pubKey.(*secp256k1.PubKey)
}
