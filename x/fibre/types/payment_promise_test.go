package types_test

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestPaymentPromiseSignBytes(t *testing.T) {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("celestia", "celestiapub")
	config.SetBech32PrefixForValidator("celestiavaloper", "celestiavaloperpub")
	config.SetBech32PrefixForConsensusNode("celestiavalcons", "celestiavalconspub")

	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	signerPublicKey, err := codectypes.NewAnyWithValue(pubKey)
	require.NoError(t, err)

	namespace := make([]byte, 29)
	namespace[0] = 0x01 // Set first byte to 0x01 for testing
	blobSize := uint32(1000)
	commitment := make([]byte, 32)
	commitment[0] = 0xFF // Set first byte to 0xFF for testing
	rowVersion := uint32(0)
	creationTimestamp := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	height := int64(100)
	chainId := "test-chain"
	signature := make([]byte, 64)
	signature[0] = 0x02 // Set first byte to 0x02 for testing

	paymentPromise := types.PaymentPromise{
		SignerPublicKey:   signerPublicKey,
		Namespace:         namespace,
		BlobSize:          blobSize,
		Commitment:        commitment,
		RowVersion:        rowVersion,
		CreationTimestamp: creationTimestamp,
		Height:            height,
		ChainId:           chainId,
		Signature:         signature,
	}

	// Get sign bytes using the method
	signBytes, err := paymentPromise.SignBytes()
	require.NoError(t, err)

	// Expected length: chain_id(10) + namespace(29) + blob_size(4) + commitment(32) + row_version(4) + height(8) + creation_timestamp(15) + signer_public_key(20)
	expectedLength := len("test-chain") + 29 + 4 + 32 + 4 + 8 + 15 + 20
	require.Equal(t, expectedLength, len(signBytes))

	offset := 0

	chainIdLen := len("test-chain")
	require.Equal(t, []byte("test-chain"), signBytes[offset:offset+chainIdLen], "Chain ID should match")
	offset += chainIdLen

	require.Equal(t, namespace, signBytes[offset:offset+29], "Namespace should match")
	offset += 29

	expectedBlobSize := make([]byte, 4)
	binary.BigEndian.PutUint32(expectedBlobSize, 1000)
	require.Equal(t, expectedBlobSize, signBytes[offset:offset+4], "Blob size should be big-endian encoded")
	actualBlobSize := binary.BigEndian.Uint32(signBytes[offset : offset+4])
	require.Equal(t, uint32(1000), actualBlobSize, "Decoded blob size should match")
	offset += 4

	require.Equal(t, commitment, signBytes[offset:offset+32], "Commitment should match")
	offset += 32

	expectedRowVersion := make([]byte, 4)
	binary.BigEndian.PutUint32(expectedRowVersion, 0)
	require.Equal(t, expectedRowVersion, signBytes[offset:offset+4], "Row version should be big-endian encoded")
	actualRowVersion := binary.BigEndian.Uint32(signBytes[offset : offset+4])
	require.Equal(t, uint32(0), actualRowVersion, "Decoded row version should match")
	offset += 4

	expectedHeight := make([]byte, 8)
	binary.BigEndian.PutUint64(expectedHeight, 100)
	require.Equal(t, expectedHeight, signBytes[offset:offset+8], "Height should be big-endian encoded")
	actualHeight := binary.BigEndian.Uint64(signBytes[offset : offset+8])
	require.Equal(t, uint64(100), actualHeight, "Decoded height should match")
	offset += 8

	expectedTimestamp, err := creationTimestamp.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, expectedTimestamp, signBytes[offset:offset+15], "Timestamp should match MarshalBinary encoding")

	// Verify we can decode it back
	var decodedTime time.Time
	err = decodedTime.UnmarshalBinary(signBytes[offset : offset+15])
	require.NoError(t, err)
	require.Equal(t, creationTimestamp, decodedTime, "Decoded timestamp should match original")
	offset += 15

	expectedSignerAddr := sdk.AccAddress(pubKey.Address())
	require.Equal(t, expectedSignerAddr.Bytes(), signBytes[offset:offset+20], "Signer address should match")
	require.Len(t, expectedSignerAddr.Bytes(), 20, "Signer address should be 20 bytes")
	offset += 20

	require.Equal(t, len(signBytes), offset)
}

func TestPaymentPromiseSignBytesWithNilPublicKey(t *testing.T) {
	paymentPromise := getPaymentPromise(t)
	paymentPromise.SignerPublicKey = nil

	signBytes, err := paymentPromise.SignBytes()
	require.Error(t, err)
	require.Empty(t, signBytes)
}

func TestPaymentPromiseSignBytesWithDifferentValues(t *testing.T) {
	// Set up SDK config for celestia addresses
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("celestia", "celestiapub")

	// Test with different values to ensure encoding is correct
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	pubKeyAny, err := codectypes.NewAnyWithValue(pubKey)
	require.NoError(t, err)

	testCases := []struct {
		name       string
		chainId    string
		blobSize   uint32
		height     int64
		rowVersion uint32
	}{
		{
			name:       "small values",
			chainId:    "a",
			blobSize:   1,
			height:     1,
			rowVersion: 0,
		},
		{
			name:       "large values",
			chainId:    "very-long-chain-id-for-testing-purposes",
			blobSize:   4294967295,          // Max uint32
			height:     9223372036854775807, // Max int64
			rowVersion: 4294967295,          // Max uint32
		},
		{
			name:       "celestia mainnet",
			chainId:    "celestia",
			blobSize:   1048576, // 1MB
			height:     1000000,
			rowVersion: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

			promise := types.PaymentPromise{
				SignerPublicKey:   pubKeyAny,
				Namespace:         make([]byte, 29),
				BlobSize:          tc.blobSize,
				Commitment:        make([]byte, 32),
				RowVersion:        tc.rowVersion,
				CreationTimestamp: testTime,
				Height:            tc.height,
				ChainId:           tc.chainId,
				Signature:         make([]byte, 64),
			}

			signBytes, err := promise.SignBytes()
			require.NoError(t, err)
			require.NotEmpty(t, signBytes, "Sign bytes should not be empty")

			// Verify we can extract and decode the numeric fields correctly
			offset := len(tc.chainId) + 29 // Skip chain_id and namespace

			// Verify blob_size encoding
			actualBlobSize := binary.BigEndian.Uint32(signBytes[offset : offset+4])
			require.Equal(t, tc.blobSize, actualBlobSize, "Blob size should be correctly encoded")
			offset += 4 + 32 // Skip blob_size and commitment

			// Verify row_version encoding
			actualRowVersion := binary.BigEndian.Uint32(signBytes[offset : offset+4])
			require.Equal(t, tc.rowVersion, actualRowVersion, "Row version should be correctly encoded")
			offset += 4 // Skip row_version

			// Verify height encoding
			actualHeight := binary.BigEndian.Uint64(signBytes[offset : offset+8])
			require.Equal(t, uint64(tc.height), actualHeight, "Height should be correctly encoded")
		})
	}
}

func getPaymentPromise(t *testing.T) types.PaymentPromise {
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()
	signerPublicKey, err := codectypes.NewAnyWithValue(pubKey)
	require.NoError(t, err)

	namespace := make([]byte, 29)
	namespace[0] = 0x01 // Set first byte to 0x01 for testing
	blobSize := uint32(1000)
	commitment := make([]byte, 32)
	commitment[0] = 0xFF // Set first byte to 0xFF for testing
	rowVersion := uint32(0)
	creationTimestamp := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	height := int64(100)
	chainId := "test-chain"
	signature := make([]byte, 64)
	signature[0] = 0x02 // Set first byte to 0x02 for testing

	return types.PaymentPromise{
		SignerPublicKey:   signerPublicKey,
		Namespace:         namespace,
		BlobSize:          blobSize,
		Commitment:        commitment,
		RowVersion:        rowVersion,
		CreationTimestamp: creationTimestamp,
		Height:            height,
		ChainId:           chainId,
		Signature:         signature,
	}
}
