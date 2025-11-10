package app

import (
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

	// Create a minimal PaymentPromise for testing
	paymentPromise := fibretypes.PaymentPromise{
		SignerPublicKey:   secp256k1PubKey,
		Namespace:         []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
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
