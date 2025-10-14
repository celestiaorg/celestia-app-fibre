package fibre_test

import (
	"testing"
	"time"

	"github.com/celestiaorg/celestia-app/v6/x/fibre"
	"github.com/celestiaorg/go-square/v3/share"
	secp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"
)

func makePaymentPromise(t *testing.T, privKey *secp256k1.PrivKey) *fibre.PaymentPromise {
	t.Helper()
	namespace := share.MustNewV0Namespace([]byte("test"))
	commitment := fibre.Commitment{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	}
	return &fibre.PaymentPromise{
		SignerKey:         privKey.PubKey().(*secp256k1.PubKey),
		ChainID:           "test-chain-1",
		Namespace:         namespace,
		BlobSize:          1024,
		Commitment:        commitment,
		BlobVersion:       1,
		CreationTimestamp: time.Now().UTC().Truncate(time.Second),
		Height:            12345,
	}
}

func TestPaymentPromise_MarshalUnmarshalBinary(t *testing.T) {
	privKey := secp256k1.GenPrivKey()

	original := makePaymentPromise(t, privKey)
	// use a valid compact signature for testing (64 bytes)
	sig, err := privKey.Sign(make([]byte, 32))
	require.NoError(t, err)
	original.Signature = sig

	data, err := original.MarshalBinary()
	require.NoError(t, err)

	var decoded fibre.PaymentPromise
	require.NoError(t, decoded.UnmarshalBinary(data))

	require.Equal(t, original.SignerKey.Bytes(), decoded.SignerKey.Bytes())
	require.Equal(t, original.ChainID, decoded.ChainID)
	require.Equal(t, original.Namespace.Bytes(), decoded.Namespace.Bytes())
	require.Equal(t, original.BlobSize, decoded.BlobSize)
	require.Equal(t, original.Commitment, decoded.Commitment)
	require.Equal(t, original.BlobVersion, decoded.BlobVersion)
	require.True(t, decoded.CreationTimestamp.Equal(original.CreationTimestamp))
	require.Equal(t, original.Signature, decoded.Signature)
	require.Equal(t, original.Height, decoded.Height)

	data2, err := decoded.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, data, data2)
}

func TestPaymentPromise_SigningRoundtrip(t *testing.T) {
	privKey := secp256k1.GenPrivKey()

	pp := makePaymentPromise(t, privKey)
	require.Equal(t, privKey.PubKey(), pp.SignerKey)

	signBytes, err := pp.SignBytes()
	require.NoError(t, err)

	signature, err := privKey.Sign(signBytes)
	require.NoError(t, err)
	pp.Signature = signature

	data, err := pp.MarshalBinary()
	require.NoError(t, err)

	var decoded fibre.PaymentPromise
	require.NoError(t, decoded.UnmarshalBinary(data))

	require.NoError(t, decoded.Validate())

	// verify signature over deserialized PaymentPromise sign bytes
	decodedSignBytes, err := decoded.SignBytes()
	require.NoError(t, err)
	require.Equal(t, signBytes, decodedSignBytes)
	require.True(t, decoded.SignerKey.VerifySignature(decodedSignBytes, decoded.Signature))
}

func TestPaymentPromise_Validate(t *testing.T) {
	privKey := secp256k1.GenPrivKey()

	pp := makePaymentPromise(t, privKey)

	signBytes, err := pp.SignBytes()
	require.NoError(t, err)

	signature, err := privKey.Sign(signBytes)
	require.NoError(t, err)
	pp.Signature = signature
	require.NoError(t, pp.Validate())

	invalidPP := makePaymentPromise(t, privKey)
	invalidPP.Signature = make([]byte, 64)
	require.EqualError(t, invalidPP.Validate(), "signature verification failed")

	wrongPrivKey := secp256k1.GenPrivKey()

	wrongKeyPP := makePaymentPromise(t, wrongPrivKey)
	wrongKeyPP.Signature = signature
	require.EqualError(t, wrongKeyPP.Validate(), "signature verification failed")

	wrongSignBytes, err := wrongKeyPP.SignBytes()
	require.NoError(t, err)
	wrongSignature, err := wrongPrivKey.Sign(wrongSignBytes)
	require.NoError(t, err)
	wrongKeyPP.Signature = wrongSignature
	require.NoError(t, wrongKeyPP.Validate())
}

func TestCommitment_UnmarshalBinary(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
	}{
		{
			name: "valid",
			input: []byte{
				1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
				17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
			},
			wantErr: false,
		},
		{name: "too short", input: make([]byte, 31), wantErr: true},
		{name: "too long", input: make([]byte, 33), wantErr: true},
		{name: "empty", input: []byte{}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c fibre.Commitment
			err := c.UnmarshalBinary(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.input, c[:])
			}
		})
	}
}
