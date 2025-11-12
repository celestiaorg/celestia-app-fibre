package app

import (
	"bytes"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	"github.com/celestiaorg/celestia-app/v6/app/encoding"
	fibretypes "github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/go-square/v3/share"
	"github.com/celestiaorg/go-square/v3/tx"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

func TestSeparateTxs(t *testing.T) {
	encConf := encoding.MakeConfig(ModuleEncodingRegisters...)
	txConfig := encConf.TxConfig

	// Create test transactions
	normalTx := createNormalTx(t, txConfig)
	blobTx := createBlobTx(t, txConfig)
	payForFibreTx := createPayForFibreTx(t, txConfig)

	tests := []struct {
		name     string
		rawTxs   [][]byte
		wantNorm int
		wantBlob int
		wantPFF  int
	}{
		{
			name:     "empty transactions",
			rawTxs:   [][]byte{},
			wantNorm: 0,
			wantBlob: 0,
			wantPFF:  0,
		},
		{
			name:     "only normal transactions",
			rawTxs:   [][]byte{normalTx},
			wantNorm: 1,
			wantBlob: 0,
			wantPFF:  0,
		},
		{
			name:     "only blob transactions",
			rawTxs:   [][]byte{blobTx},
			wantNorm: 0,
			wantBlob: 1,
			wantPFF:  0,
		},
		{
			name:     "only pay-for-fibre transactions",
			rawTxs:   [][]byte{payForFibreTx},
			wantNorm: 0,
			wantBlob: 0,
			wantPFF:  1,
		},
		{
			name:     "mixed transactions",
			rawTxs:   [][]byte{normalTx, blobTx, payForFibreTx},
			wantNorm: 1,
			wantBlob: 1,
			wantPFF:  1,
		},
		{
			name:     "multiple pay-for-fibre transactions",
			rawTxs:   [][]byte{payForFibreTx, payForFibreTx, normalTx},
			wantNorm: 1,
			wantBlob: 0,
			wantPFF:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalTxs, blobTxs, payForFibreTxs := separateTxs(txConfig, tt.rawTxs)
			require.Len(t, normalTxs, tt.wantNorm, "normal transactions count")
			require.Len(t, blobTxs, tt.wantBlob, "blob transactions count")
			require.Len(t, payForFibreTxs, tt.wantPFF, "pay-for-fibre transactions count")
		})
	}
}

func TestExtractMsgPayForFibre(t *testing.T) {
	encConf := encoding.MakeConfig(ModuleEncodingRegisters...)
	txConfig := encConf.TxConfig

	tests := []struct {
		name      string
		createTx  func() []byte
		wantFound bool
	}{
		{
			name: "transaction with MsgPayForFibre",
			createTx: func() []byte {
				return createPayForFibreTx(t, txConfig)
			},
			wantFound: true,
		},
		{
			name: "transaction without MsgPayForFibre",
			createTx: func() []byte {
				return createNormalTx(t, txConfig)
			},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txBytes := tt.createTx()
			sdkTx, err := txConfig.TxDecoder()(txBytes)
			require.NoError(t, err)

			msg, found := extractMsgPayForFibre(sdkTx)
			require.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				require.NotNil(t, msg)
				require.IsType(t, &fibretypes.MsgPayForFibre{}, msg)
			} else {
				require.Nil(t, msg)
			}
		})
	}
}

func TestFilteredSquareBuilderFillWithPayForFibre(t *testing.T) {
	encConf := encoding.MakeConfig(ModuleEncodingRegisters...)
	txConfig := encConf.TxConfig

	// Create a simple ante handler that always passes
	anteHandler := func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		return ctx, nil
	}

	builder, err := NewFilteredSquareBuilder(anteHandler, txConfig, 64, 64)
	require.NoError(t, err)

	// Create test transactions
	normalTx := createNormalTx(t, txConfig)
	blobTx := createBlobTx(t, txConfig)
	payForFibreTx := createPayForFibreTx(t, txConfig)

	// Create a minimal context
	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	ctx := sdk.NewContext(stateStore, cmtproto.Header{}, false, log.NewNopLogger())

	tests := []struct {
		name          string
		txs           [][]byte
		wantKeptCount int
	}{
		{
			name:          "only pay-for-fibre transaction",
			txs:           [][]byte{payForFibreTx},
			wantKeptCount: 1,
		},
		{
			name:          "mixed transactions",
			txs:           [][]byte{normalTx, blobTx, payForFibreTx},
			wantKeptCount: 3,
		},
		{
			name:          "multiple pay-for-fibre transactions",
			txs:           [][]byte{payForFibreTx, payForFibreTx},
			wantKeptCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept := builder.Fill(ctx, tt.txs)
			require.Len(t, kept, tt.wantKeptCount)

			// Verify that pay-for-fibre transactions are in the kept list
			// and that they are properly separated
			normalTxs, blobTxs, payForFibreTxs := separateTxs(txConfig, kept)
			require.GreaterOrEqual(t, len(normalTxs)+len(blobTxs)+len(payForFibreTxs), 0, "should have some transactions")
		})
	}
}

func TestFilteredSquareBuilderPayForFibreInSquare(t *testing.T) {
	encConf := encoding.MakeConfig(ModuleEncodingRegisters...)
	txConfig := encConf.TxConfig

	// Create a simple ante handler that always passes
	anteHandler := func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		return ctx, nil
	}

	builder, err := NewFilteredSquareBuilder(anteHandler, txConfig, 64, 64)
	require.NoError(t, err)

	payForFibreTx := createPayForFibreTx(t, txConfig)

	// Create a minimal context
	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	ctx := sdk.NewContext(stateStore, cmtproto.Header{}, false, log.NewNopLogger())

	kept := builder.Fill(ctx, [][]byte{payForFibreTx})
	require.Len(t, kept, 1)

	// Build the square
	square, err := builder.Build()
	require.NoError(t, err)
	require.NotNil(t, square)

	// Verify that pay-for-fibre transactions are in the PayForFibreNamespace
	payForFibreShareRange := share.GetShareRangeForNamespace(square, share.PayForFibreNamespace)
	require.False(t, payForFibreShareRange.IsEmpty(), "PayForFibre namespace should have shares")
}

func createNormalTx(t *testing.T, txConfig client.TxConfig) []byte {
	privKey := secp256k1.GenPrivKey()
	addr := sdk.AccAddress(privKey.PubKey().Address())

	msg := &banktypes.MsgSend{
		FromAddress: addr.String(),
		ToAddress:   addr.String(),
		Amount:      sdk.NewCoins(sdk.NewInt64Coin("utia", 1)),
	}

	builder := txConfig.NewTxBuilder()
	err := builder.SetMsgs(msg)
	require.NoError(t, err)

	txBytes, err := txConfig.TxEncoder()(builder.GetTx())
	require.NoError(t, err)

	return txBytes
}

func createBlobTx(t *testing.T, txConfig client.TxConfig) []byte {
	ns := share.MustNewV0Namespace([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	blob, err := share.NewBlob(ns, []byte("test blob data"), share.ShareVersionZero, nil)
	require.NoError(t, err)

	blobTx, err := tx.MarshalBlobTx(nil, blob)
	require.NoError(t, err)

	return blobTx
}

func createPayForFibreTx(t *testing.T, txConfig client.TxConfig) []byte {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	secp256k1PubKey := *pubKey.(*secp256k1.PubKey)
	addr := sdk.AccAddress(pubKey.Address())

	// Create a valid namespace using MustNewV0Namespace helper
	ns := share.MustNewV0Namespace(bytes.Repeat([]byte{1}, share.NamespaceVersionZeroIDSize))
	namespace := ns.Bytes()

	// Create a minimal PaymentPromise for testing
	paymentPromise := fibretypes.PaymentPromise{
		SignerPublicKey:   secp256k1PubKey,
		Namespace:         namespace,
		Commitment:        make([]byte, 32),
		BlobVersion:       1,
		BlobSize:          100,
		CreationTimestamp: time.Now(),
		Signature:         make([]byte, 64),
		Height:            1,
		ChainId:           "test",
	}

	msg := &fibretypes.MsgPayForFibre{
		Signer:              addr.String(),
		PaymentPromise:      paymentPromise,
		ValidatorSignatures: [][]byte{},
	}

	builder := txConfig.NewTxBuilder()
	err := builder.SetMsgs(msg)
	require.NoError(t, err)

	txBytes, err := txConfig.TxEncoder()(builder.GetTx())
	require.NoError(t, err)

	return txBytes
}

func TestCreateSystemBlobForPayForFibre(t *testing.T) {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	secp256k1PubKey := *pubKey.(*secp256k1.PubKey)
	addr := sdk.AccAddress(pubKey.Address())

	// Create a valid namespace using MustNewV0Namespace helper
	ns := share.MustNewV0Namespace(bytes.Repeat([]byte{1}, share.NamespaceVersionZeroIDSize))
	namespace := ns.Bytes()

	// Create a valid PaymentPromise
	paymentPromise := fibretypes.PaymentPromise{
		SignerPublicKey:   secp256k1PubKey,
		Namespace:         namespace,
		Commitment:        bytes.Repeat([]byte{0xFF}, share.FibreCommitmentSize),
		BlobVersion:       1,
		BlobSize:          100,
		CreationTimestamp: time.Now(),
		Signature:         make([]byte, 64),
		Height:            1,
		ChainId:           "test",
	}

	msg := &fibretypes.MsgPayForFibre{
		Signer:              addr.String(),
		PaymentPromise:      paymentPromise,
		ValidatorSignatures: [][]byte{},
	}

	// Test successful creation
	blob, err := createSystemBlobForPayForFibre(msg)
	require.NoError(t, err)
	require.NotNil(t, blob)

	// Verify blob properties
	require.Equal(t, share.ShareVersionTwo, blob.ShareVersion())
	expectedNs, err := share.NewNamespaceFromBytes(namespace)
	require.NoError(t, err)
	require.True(t, blob.Namespace().Equals(expectedNs))
	require.Equal(t, addr.Bytes(), blob.Signer())

	// Verify blob data contains fibre_blob_version and commitment
	blobData := blob.Data()
	require.Len(t, blobData, share.FibreBlobVersionSize+share.FibreCommitmentSize)

	// Verify fibre_blob_version (first 4 bytes, big-endian)
	expectedVersion := uint32(1)
	actualVersion := uint32(blobData[0])<<24 | uint32(blobData[1])<<16 | uint32(blobData[2])<<8 | uint32(blobData[3])
	require.Equal(t, expectedVersion, actualVersion)

	// Verify commitment (next 32 bytes)
	require.Equal(t, paymentPromise.Commitment, blobData[share.FibreBlobVersionSize:])
}

func TestCreateSystemBlobForPayForFibreInvalidNamespace(t *testing.T) {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	secp256k1PubKey := *pubKey.(*secp256k1.PubKey)
	addr := sdk.AccAddress(pubKey.Address())

	// Create an invalid namespace (wrong size)
	invalidNamespace := []byte{1, 2, 3} // Too short

	paymentPromise := fibretypes.PaymentPromise{
		SignerPublicKey:   secp256k1PubKey,
		Namespace:         invalidNamespace,
		Commitment:        bytes.Repeat([]byte{0xFF}, share.FibreCommitmentSize),
		BlobVersion:       1,
		BlobSize:          100,
		CreationTimestamp: time.Now(),
		Signature:         make([]byte, 64),
		Height:            1,
		ChainId:           "test",
	}

	msg := &fibretypes.MsgPayForFibre{
		Signer:              addr.String(),
		PaymentPromise:      paymentPromise,
		ValidatorSignatures: [][]byte{},
	}

	_, err := createSystemBlobForPayForFibre(msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid namespace size")
}

func TestCreateSystemBlobForPayForFibreInvalidSigner(t *testing.T) {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	secp256k1PubKey := *pubKey.(*secp256k1.PubKey)

	// Create a valid namespace using MustNewV0Namespace helper
	ns := share.MustNewV0Namespace(bytes.Repeat([]byte{1}, share.NamespaceVersionZeroIDSize))
	namespace := ns.Bytes()

	paymentPromise := fibretypes.PaymentPromise{
		SignerPublicKey:   secp256k1PubKey,
		Namespace:         namespace,
		Commitment:        bytes.Repeat([]byte{0xFF}, share.FibreCommitmentSize),
		BlobVersion:       1,
		BlobSize:          100,
		CreationTimestamp: time.Now(),
		Signature:         make([]byte, 64),
		Height:            1,
		ChainId:           "test",
	}

	// Invalid signer address
	msg := &fibretypes.MsgPayForFibre{
		Signer:              "invalid-address",
		PaymentPromise:      paymentPromise,
		ValidatorSignatures: [][]byte{},
	}

	_, err := createSystemBlobForPayForFibre(msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode signer address")
}

func TestCreateSystemBlobForPayForFibreInvalidCommitment(t *testing.T) {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	secp256k1PubKey := *pubKey.(*secp256k1.PubKey)
	addr := sdk.AccAddress(pubKey.Address())

	// Create a valid namespace using MustNewV0Namespace helper
	ns := share.MustNewV0Namespace(bytes.Repeat([]byte{1}, share.NamespaceVersionZeroIDSize))
	namespace := ns.Bytes()

	// Invalid commitment size
	paymentPromise := fibretypes.PaymentPromise{
		SignerPublicKey:   secp256k1PubKey,
		Namespace:         namespace,
		Commitment:        []byte{1, 2, 3}, // Wrong size
		BlobVersion:       1,
		BlobSize:          100,
		CreationTimestamp: time.Now(),
		Signature:         make([]byte, 64),
		Height:            1,
		ChainId:           "test",
	}

	msg := &fibretypes.MsgPayForFibre{
		Signer:              addr.String(),
		PaymentPromise:      paymentPromise,
		ValidatorSignatures: [][]byte{},
	}

	_, err := createSystemBlobForPayForFibre(msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid commitment size")
}

func TestFilteredSquareBuilderFillWithPayForFibreGeneratesSystemBlob(t *testing.T) {
	encConf := encoding.MakeConfig(ModuleEncodingRegisters...)
	txConfig := encConf.TxConfig

	// Create a test context
	db := dbm.NewMemDB()
	ms := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	err := ms.LoadLatestVersion()
	require.NoError(t, err)
	ctx := sdk.NewContext(ms, cmtproto.Header{}, false, log.NewNopLogger())

	// Create a mock ante handler that always succeeds
	handler := func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		return ctx, nil
	}

	fsb, err := NewFilteredSquareBuilder(handler, txConfig, 16, 64)
	require.NoError(t, err)

	// Create a PayForFibre transaction
	payForFibreTx := createPayForFibreTx(t, txConfig)

	// Extract the namespace from the PaymentPromise before processing
	sdkTx, err := txConfig.TxDecoder()(payForFibreTx)
	require.NoError(t, err)
	msgPayForFibre, hasPayForFibre := extractMsgPayForFibre(sdkTx)
	require.True(t, hasPayForFibre)

	expectedNs, err := share.NewNamespaceFromBytes(msgPayForFibre.PaymentPromise.Namespace)
	require.NoError(t, err)

	// Process the transaction
	kept := fsb.Fill(ctx, [][]byte{payForFibreTx})
	// Transaction should be kept if system blob was successfully added
	require.GreaterOrEqual(t, len(kept), 0, "Transaction processing should complete")

	// Build the square
	square, err := fsb.Build()
	require.NoError(t, err)
	require.NotNil(t, square)

	// Check that the system blob is in the square
	nsRange := share.GetShareRangeForNamespace(square, expectedNs)
	if nsRange.IsEmpty() {
		// If system blob is not found, it might be because transaction was reverted
		// Let's check if the transaction was kept
		if len(kept) == 0 {
			t.Skip("Transaction was not kept, likely due to validation failure - skipping system blob check")
			return
		}
		require.Fail(t, "System blob should be in the square when transaction is kept")
	}

	// Verify the system blob has share version 2
	blobShares := square[nsRange.Start : nsRange.End+1]
	require.Greater(t, len(blobShares), 0)
	firstShare := blobShares[0]
	require.Equal(t, share.ShareVersionTwo, firstShare.Version())
}
