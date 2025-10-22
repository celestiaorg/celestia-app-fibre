package keeper_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	fibre "github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/keeper"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/go-square/v3/share"
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

func (suite *KeeperTestSuite) TestProcessedPayment() {
	suite.T().Run("keeper should return false for non-existent processed payment", func(t *testing.T) {
		_, found := suite.keeper.GetProcessedPayment(suite.ctx, []byte("test-hash"))
		suite.False(found)
	})

	suite.T().Run("keeper should set and get processed payment", func(t *testing.T) {
		want := types.ProcessedPayment{
			PaymentPromiseHash: []byte("test-hash"),
			ProcessedAt:        suite.ctx.BlockTime(),
		}
		suite.keeper.SetProcessedPayment(suite.ctx, want)

		got, found := suite.keeper.GetProcessedPayment(suite.ctx, []byte("test-hash"))
		suite.True(found)
		suite.Equal(want, got)
	})

	suite.T().Run("keeper should delete processed payment", func(t *testing.T) {
		suite.keeper.DeleteProcessedPayment(suite.ctx, []byte("test-hash"))
		_, found := suite.keeper.GetProcessedPayment(suite.ctx, []byte("test-hash"))
		suite.False(found)
	})

	suite.T().Run("isPaymentProcessed should return false for non-existent payment promise", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()
		suite.False(suite.keeper.IsPaymentPromiseProcessed(suite.ctx, &paymentPromise))
	})

	suite.T().Run("isPaymentProcessed should return true for existing payment promise", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()
		pp := fibre.PaymentPromise{}
		pp.FromProto(&paymentPromise)
		paymentPromiseHash, err := pp.Hash()
		suite.NoError(err)

		suite.keeper.SetProcessedPayment(suite.ctx, types.ProcessedPayment{
			PaymentPromiseHash: paymentPromiseHash,
			ProcessedAt:        suite.ctx.BlockTime(),
		})

		suite.True(suite.keeper.IsPaymentPromiseProcessed(suite.ctx, &paymentPromise))
	})
}

func (suite *KeeperTestSuite) TestValidatePaymentPromiseInternal() {
	suite.T().Run("valid payment promise should pass validation", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()
		suite.createEscrowAccount(paymentPromise, 1000)
		err := suite.keeper.ValidatePaymentPromiseInternal(suite.ctx, &paymentPromise)
		suite.NoError(err)
	})

	suite.T().Run("invalid payment promise format should fail", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()
		paymentPromise.Namespace = make([]byte, 10) // Invalid size (should be 29)
		err := suite.keeper.ValidatePaymentPromiseInternal(suite.ctx, &paymentPromise)
		suite.Error(err)
		suite.Contains(err.Error(), "invalid payment promise format")
	})

	suite.T().Run("invalid payment promise should fail validation", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()
		paymentPromise.BlobSize = 0 // Invalid: zero blob size

		err := suite.keeper.ValidatePaymentPromiseInternal(suite.ctx, &paymentPromise)
		suite.Error(err)
		suite.Contains(err.Error(), "invalid payment promise")
	})

	suite.T().Run("already processed payment promise should fail", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()
		suite.createEscrowAccount(paymentPromise, 1000)

		// Mark payment promise as already processed
		pp := fibre.PaymentPromise{}
		err := pp.FromProto(&paymentPromise)
		suite.NoError(err)

		promiseHash, err := pp.Hash()
		suite.NoError(err)
		processedPayment := types.ProcessedPayment{
			PaymentPromiseHash: promiseHash,
			ProcessedAt:        suite.ctx.BlockTime(),
		}
		suite.keeper.SetProcessedPayment(suite.ctx, processedPayment)

		// Validate should fail because it's already processed
		err = suite.keeper.ValidatePaymentPromiseInternal(suite.ctx, &paymentPromise)
		suite.Error(err)
		suite.Contains(err.Error(), "payment promise has already been processed")
	})

	suite.T().Run("escrow account not found should fail", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()

		// Validate should fail because escrow account doesn't exist
		err := suite.keeper.ValidatePaymentPromiseInternal(suite.ctx, &paymentPromise)
		suite.Error(err)
		suite.Contains(err.Error(), "escrow account not found for signer")
	})

	suite.T().Run("insufficient balance should fail", func(t *testing.T) {
		paymentPromise := suite.createPaymentPromise()

		signerAddr := sdk.AccAddress(paymentPromise.SignerPublicKey.Address())
		signerAddrStr := signerAddr.String()

		// Create escrow account with insufficient balance
		params := suite.keeper.GetParams(suite.ctx)
		gasRequired := uint64(paymentPromise.BlobSize) * uint64(params.GasPerBlobByte)
		requiredAmount := sdk.NewInt64Coin("utia", int64(gasRequired))
		insufficientBalance := sdk.NewInt64Coin("utia", int64(gasRequired)-1) // Less than required

		escrowAccount := types.EscrowAccount{
			Signer:           signerAddrStr,
			Balance:          insufficientBalance,
			AvailableBalance: insufficientBalance,
		}
		suite.keeper.SetEscrowAccount(suite.ctx, escrowAccount)

		// Validate should fail because of insufficient balance
		err := suite.keeper.ValidatePaymentPromiseInternal(suite.ctx, &paymentPromise)
		suite.Error(err)
		suite.Contains(err.Error(), "insufficient balance in escrow account")
		suite.Contains(err.Error(), fmt.Sprintf("required: %v", requiredAmount))
		suite.Contains(err.Error(), fmt.Sprintf("available: %v", insufficientBalance))
	})
}

// createPaymentPromise creates a properly signed and valid payment promise for testing
func (suite *KeeperTestSuite) createPaymentPromise() types.PaymentPromise {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	signerPublicKey := *pubKey.(*secp256k1.PubKey)

	paymentPromise := types.PaymentPromise{
		ChainId:           "test-chain",
		Height:            int64(100),
		Namespace:         share.MustNewV0Namespace(bytes.Repeat([]byte{0x1}, share.NamespaceVersionZeroIDSize)).Bytes(),
		BlobSize:          uint32(1000),
		BlobVersion:       0,
		Commitment:        make([]byte, 32),
		CreationTimestamp: time.Now().UTC().Truncate(time.Second),
		SignerPublicKey:   signerPublicKey,
		Signature:         make([]byte, 64),
	}

	paymentPromise = *suite.signPaymentPromise(&paymentPromise, privKey)
	return paymentPromise
}

// createEscrowAccount creates an escrow account for the given payment promise with sufficient balance
func (suite *KeeperTestSuite) createEscrowAccount(paymentPromise types.PaymentPromise, extraBalance int64) {
	signerAddr := sdk.AccAddress(paymentPromise.SignerPublicKey.Address())
	signerAddrStr := signerAddr.String()

	params := suite.keeper.GetParams(suite.ctx)
	gasRequired := uint64(paymentPromise.BlobSize) * uint64(params.GasPerBlobByte)
	availableBalance := sdk.NewInt64Coin("utia", int64(gasRequired)+extraBalance)

	escrowAccount := types.EscrowAccount{
		Signer:           signerAddrStr,
		Balance:          availableBalance,
		AvailableBalance: availableBalance,
	}
	suite.keeper.SetEscrowAccount(suite.ctx, escrowAccount)
}

func (suite *KeeperTestSuite) signPaymentPromise(paymentPromise *types.PaymentPromise, privKey *secp256k1.PrivKey) *types.PaymentPromise {
	pp := fibre.PaymentPromise{}
	err := pp.FromProto(paymentPromise)
	suite.NoError(err)

	signBytes, err := pp.SignBytes()
	suite.NoError(err)

	signature, err := privKey.Sign(signBytes)
	suite.NoError(err)
	paymentPromise.Signature = signature
	return paymentPromise
}
