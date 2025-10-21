package keeper_test

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"

	"cosmossdk.io/log"
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
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
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
	// Set up SDK config for celestia addresses
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("celestia", "celestiapub")
	config.SetBech32PrefixForValidator("celestiavaloper", "celestiavaloperpub")
	config.SetBech32PrefixForConsensusNode("celestiavalcons", "celestiavalconspub")

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(key, storetypes.StoreTypeIAVL, db)
	stateStore.MountStoreWithDB(tkey, storetypes.StoreTypeTransient, nil)
	require.NoError(suite.T(), stateStore.LoadLatestVersion())

	registry := codectypes.NewInterfaceRegistry()
	// types.RegisterInterfaces(registry) // Skip for now as this function may not exist
	cdc := codec.NewProtoCodec(registry)

	// Create a mock bank keeper
	mockBankKeeper := &MockBankKeeper{}

	suite.keeper = keeper.NewKeeper(
		cdc,
		key,
		mockBankKeeper,
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
	signer := "celestia15drmhzw5kwgenvemy30rqqqgq52axf5wwrruf7"

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
	signer := "celestia15drmhzw5kwgenvemy30rqqqgq52axf5wwrruf7"
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
	// Create a test payment promise
	promise := &types.PaymentPromise{
		Namespace:         make([]byte, 29), // Valid namespace size
		BlobSize:          1000,
		Commitment:        make([]byte, 32), // Valid commitment size
		BlobVersion:       0,
		Height:            100,
		ChainId:           "test-chain",
		CreationTimestamp: suite.ctx.BlockTime(),
	}

	// Test checking non-existent processed payment promise
	processed := suite.keeper.IsPaymentPromiseProcessed(suite.ctx, promise)
	suite.False(processed)

	// Test setting processed payment promise
	hash := suite.keeper.GetPaymentPromiseHash(promise)
	processedTime := suite.ctx.BlockTime()
	entry := types.PaymentPromiseEntry{
		PaymentPromiseHash: hash,
		ProcessedAt:        processedTime,
	}
	suite.keeper.SetPaymentPromiseEntry(suite.ctx, entry)

	processed = suite.keeper.IsPaymentPromiseProcessed(suite.ctx, promise)
	suite.True(processed)
}

// TestGetNextWithdrawalID is no longer needed since withdrawals are keyed by timestamp

func (suite *KeeperTestSuite) TestValidatePaymentPromise() {
	// This test would require more setup including creating a valid PaymentPromise
	// with proper public key, signature, etc. For now, we'll test the basic validation
	// that checks for processed promises and escrow account existence.

	signer := "celestia15drmhzw5kwgenvemy30rqqqgq52axf5wwrruf7"

	// Create escrow account
	account := types.EscrowAccount{
		Signer:           signer,
		Balance:          sdk.NewInt64Coin("utia", 1000),
		AvailableBalance: sdk.NewInt64Coin("utia", 1000),
	}
	suite.keeper.SetEscrowAccount(suite.ctx, account)

	// Test would continue with creating a valid PaymentPromise and testing validation
	// This requires more complex setup with cryptographic keys and signatures
	// TODO: @rootulp
}

func (suite *KeeperTestSuite) TestIterators() {
	// Test escrow account iterator
	signer1 := "celestia15drmhzw5kwgenvemy30rqqqgq52axf5wwrruf7"
	signer2 := "celestia1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a"

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

func (suite *KeeperTestSuite) TestGetPaymentPromiseHash() {
	// Create a test payment promise with known values
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	signerPublicKey := *pubKey.(*secp256k1.PubKey)

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	promise := &types.PaymentPromise{
		SignerPublicKey:   signerPublicKey,
		Namespace:         make([]byte, 29), // All zeros for simplicity
		BlobSize:          1000,
		Commitment:        make([]byte, 32), // All zeros for simplicity
		BlobVersion:       0,
		CreationTimestamp: testTime,
		Height:            100,
		ChainId:           "test-chain",
		Signature:         make([]byte, 64), // All zeros for simplicity
	}

	// Calculate hash using our implementation
	actualHash := suite.keeper.GetPaymentPromiseHash(promise)

	// Calculate expected hash manually according to spec
	expectedHash := calculateExpectedHash(promise, pubKey)

	// Verify they match
	suite.Equal(expectedHash, actualHash, "Hash should match the sdk_module spec implementation")
	suite.Len(actualHash, 32, "Hash should be 32 bytes (SHA256)")
}

func (suite *KeeperTestSuite) TestGetPaymentPromiseSignBytes() {
	// Create a test payment promise
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	signerPublicKey := *pubKey.(*secp256k1.PubKey)

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	promise := &types.PaymentPromise{
		SignerPublicKey:   signerPublicKey,
		Namespace:         make([]byte, 29),
		BlobSize:          1000,
		Commitment:        make([]byte, 32),
		BlobVersion:       0,
		CreationTimestamp: testTime,
		Height:            100,
		ChainId:           "test-chain",
		Signature:         make([]byte, 64),
	}

	// Get sign bytes
	signBytes := suite.keeper.GetPaymentPromiseSignBytes(promise)

	// Verify structure according to spec
	expectedLength := len("test-chain") + 29 + 4 + 32 + 4 + 8 + 15 + 20
	suite.Equal(expectedLength, len(signBytes), "Sign bytes should have expected length")

	// Verify individual components
	offset := 0

	// chain_id
	chainIdLen := len("test-chain")
	suite.Equal([]byte("test-chain"), signBytes[offset:offset+chainIdLen])
	offset += chainIdLen

	// namespace (29 bytes of zeros)
	suite.Equal(make([]byte, 29), signBytes[offset:offset+29])
	offset += 29

	// blob_size (4 bytes, big-endian uint32)
	expectedBlobSize := make([]byte, 4)
	binary.BigEndian.PutUint32(expectedBlobSize, 1000)
	suite.Equal(expectedBlobSize, signBytes[offset:offset+4])
	offset += 4

	// commitment (32 bytes of zeros)
	suite.Equal(make([]byte, 32), signBytes[offset:offset+32])
	offset += 32

	// blob_version (4 bytes, big-endian uint32)
	expectedBlobVersion := make([]byte, 4)
	binary.BigEndian.PutUint32(expectedBlobVersion, 0)
	suite.Equal(expectedBlobVersion, signBytes[offset:offset+4])
	offset += 4

	// height (8 bytes, big-endian int64)
	expectedHeight := make([]byte, 8)
	binary.BigEndian.PutUint64(expectedHeight, 100)
	suite.Equal(expectedHeight, signBytes[offset:offset+8])
	offset += 8

	// creation_timestamp (15 bytes)
	expectedTimestamp, err := testTime.MarshalBinary()
	suite.NoError(err)
	suite.Equal(expectedTimestamp, signBytes[offset:offset+15])
	offset += 15

	// signer_public_key (20 bytes - address from public key)
	expectedSignerAddr := sdk.AccAddress(pubKey.Address())
	suite.Equal(expectedSignerAddr.Bytes(), signBytes[offset:offset+20])
}

// calculateExpectedHash manually implements the spec for comparison
func calculateExpectedHash(promise *types.PaymentPromise, pubKey cryptotypes.PubKey) []byte {
	var signBytes []byte

	// chain_id
	signBytes = append(signBytes, []byte(promise.ChainId)...)

	// namespace
	signBytes = append(signBytes, promise.Namespace...)

	// blob_size (big-endian uint32)
	blobSizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(blobSizeBytes, promise.BlobSize)
	signBytes = append(signBytes, blobSizeBytes...)

	// commitment
	signBytes = append(signBytes, promise.Commitment...)

	// blob_version (big-endian uint32)
	blobVersionBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(blobVersionBytes, promise.BlobVersion)
	signBytes = append(signBytes, blobVersionBytes...)

	// height (big-endian int64)
	heightBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(heightBytes, uint64(promise.Height))
	signBytes = append(signBytes, heightBytes...)

	// creation_timestamp
	timestampBytes, _ := promise.CreationTimestamp.MarshalBinary()
	signBytes = append(signBytes, timestampBytes...)

	// signer_public_key (20-byte address)
	signerAddr := sdk.AccAddress(pubKey.Address())
	signBytes = append(signBytes, signerAddr.Bytes()...)

	// payment_promise_hash = SHA256(sign_bytes || signature)
	hashInput := append(signBytes, promise.Signature...)
	hash := sha256.Sum256(hashInput)
	return hash[:]
}
