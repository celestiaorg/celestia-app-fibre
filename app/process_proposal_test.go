package app

import (
	"bytes"
	"testing"
	"time"

	"github.com/celestiaorg/celestia-app-fibre/v6/app/encoding"
	fibretypes "github.com/celestiaorg/celestia-app-fibre/v6/x/fibre/types"
	squarev4 "github.com/celestiaorg/go-square/v4"
	blobv4 "github.com/celestiaorg/go-square/v4/proto/blob/v4"
	"github.com/celestiaorg/go-square/v4/share"
	"github.com/celestiaorg/go-square/v4/tx"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestValidateTxOrdering(t *testing.T) {
	encConf := encoding.MakeConfig(ModuleEncodingRegisters...)
	txConfig := encConf.TxConfig

	// Create test transactions
	normalTx := createNormalTxForTest(t, txConfig)
	blobTx := createBlobTxForTest(t)
	payForFibreTx := createPayForFibreTxForTest(t, txConfig)

	tests := []struct {
		name          string
		txs           [][]byte
		wantError     bool
		errorContains string
	}{
		{
			name:      "empty transactions - valid",
			txs:       [][]byte{},
			wantError: false,
		},
		{
			name:      "only normal transactions - valid",
			txs:       [][]byte{normalTx, normalTx},
			wantError: false,
		},
		{
			name:      "only blob transactions - valid",
			txs:       [][]byte{blobTx, blobTx},
			wantError: false,
		},
		{
			name:      "normal before blob - valid",
			txs:       [][]byte{normalTx, normalTx, blobTx, blobTx},
			wantError: false,
		},
		{
			name:          "normal after blob - invalid",
			txs:           [][]byte{blobTx, normalTx},
			wantError:     true,
			errorContains: "cannot be appended after blob tx",
		},
		{
			name:          "normal after blob with multiple blobs - invalid",
			txs:           [][]byte{blobTx, blobTx, normalTx},
			wantError:     true,
			errorContains: "cannot be appended after blob tx",
		},
		{
			name:          "normal after blob with multiple normals first - invalid",
			txs:           [][]byte{normalTx, normalTx, blobTx, normalTx},
			wantError:     true,
			errorContains: "cannot be appended after blob tx",
		},
		{
			name:          "PayForFibre before blob - invalid (blob must come before pay-for-fibre if both exist)",
			txs:           [][]byte{payForFibreTx, blobTx},
			wantError:     true,
			errorContains: "cannot be appended after pay-for-fibre tx",
		},
		{
			name:      "PayForFibre after blob - valid",
			txs:       [][]byte{blobTx, payForFibreTx},
			wantError: false,
		},
		{
			name:      "mixed valid ordering - normal, blob, PayForFibre",
			txs:       [][]byte{normalTx, blobTx, payForFibreTx},
			wantError: false,
		},
		{
			name:          "invalid blob tx - unmarshalling error (no blobs)",
			txs:           [][]byte{createMalformedBlobTxForTest(t)},
			wantError:     true,
			errorContains: "unmarshalling blob tx",
		},
		{
			name:      "invalid normal tx - no error (Construct doesn't validate decoding)",
			txs:       [][]byte{[]byte("invalid normal tx bytes")},
			wantError: false,
		},
		{
			name:          "invalid normal tx after blob - reports ordering error",
			txs:           [][]byte{blobTx, []byte("invalid normal tx bytes")},
			wantError:     true,
			errorContains: "cannot be appended after blob tx",
		},
		{
			name:          "invalid blob tx in middle - should report unmarshalling error",
			txs:           [][]byte{normalTx, createMalformedBlobTxForTest(t), normalTx},
			wantError:     true,
			errorContains: "unmarshalling blob tx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewPayForFibreHandler(txConfig)
			_, err := squarev4.Construct(tt.txs, 64, 64, handler)

			if tt.wantError {
				require.Error(t, err)
				if tt.errorContains != "" {
					require.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Helper functions for creating test transactions

func createNormalTxForTest(t *testing.T, txConfig client.TxConfig) []byte {
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

func createBlobTxForTest(t *testing.T) []byte {
	ns := share.MustNewV0Namespace([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	blob, err := share.NewBlob(ns, []byte("test blob data"), share.ShareVersionZero, nil)
	require.NoError(t, err)

	blobTx, err := tx.MarshalBlobTx(nil, blob)
	require.NoError(t, err)

	return blobTx
}

func createPayForFibreTxForTest(t *testing.T, txConfig client.TxConfig) []byte {
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

func createMalformedBlobTxForTest(t *testing.T) []byte {
	// Create a BlobTx with empty blobs slice - this will be recognized as a blob tx
	// but will fail validation with "no blobs provided"
	bTx := &blobv4.BlobTx{
		Tx:     []byte("dummy tx"),
		Blobs:  []*blobv4.BlobProto{}, // Empty blobs - this triggers the error
		TypeId: "BLOB",
	}
	blobTxBytes, err := proto.Marshal(bTx)
	require.NoError(t, err)
	return blobTxBytes
}
