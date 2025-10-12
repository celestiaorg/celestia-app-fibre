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
	// Set up SDK config for celestia addresses
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("celestia", "celestiapub")
	config.SetBech32PrefixForValidator("celestiavaloper", "celestiavaloperpub")
	config.SetBech32PrefixForConsensusNode("celestiavalcons", "celestiavalconspub")

	// Create a test payment promise with known values
	privKey := secp256k1.GenPrivKey()
	pubKey := privKey.PubKey()

	// Pack the public key into Any
	pubKeyAny, err := codectypes.NewAnyWithValue(pubKey)
	require.NoError(t, err)

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	testNamespace := make([]byte, 29)
	testNamespace[0] = 0x01 // Set first byte to 1 for testing
	testCommitment := make([]byte, 32)
	testCommitment[0] = 0xFF // Set first byte to 0xFF for testing

	promise := types.PaymentPromise{
		SignerPublicKey:   pubKeyAny,
		Namespace:         testNamespace,
		BlobSize:          1000,
		Commitment:        testCommitment,
		RowVersion:        0,
		CreationTimestamp: testTime,
		Height:            100,
		ChainId:           "test-chain",
		Signature:         make([]byte, 64), // All zeros for simplicity
	}

	// Get sign bytes using the method
	signBytes, err := promise.SignBytes()
	require.NoError(t, err)

	// Verify the structure according to the sdk_module spec
	// Expected length: chain_id(10) + namespace(29) + blob_size(4) + commitment(32) + row_version(4) + height(8) + creation_timestamp(15) + signer_public_key(20)
	expectedLength := len("test-chain") + 29 + 4 + 32 + 4 + 8 + 15 + 20
	require.Equal(t, expectedLength, len(signBytes), "Sign bytes should have expected length")

	// Verify individual components by parsing the sign bytes
	offset := 0

	// chain_id: Raw chain ID bytes (variable length)
	chainIdLen := len("test-chain")
	require.Equal(t, []byte("test-chain"), signBytes[offset:offset+chainIdLen], "Chain ID should match")
	offset += chainIdLen

	// namespace: Raw namespace bytes (fixed 29 bytes)
	require.Equal(t, testNamespace, signBytes[offset:offset+29], "Namespace should match")
	offset += 29

	// blob_size: Big-endian encoded uint32 (4 bytes)
	expectedBlobSize := make([]byte, 4)
	binary.BigEndian.PutUint32(expectedBlobSize, 1000)
	require.Equal(t, expectedBlobSize, signBytes[offset:offset+4], "Blob size should be big-endian encoded")
	actualBlobSize := binary.BigEndian.Uint32(signBytes[offset : offset+4])
	require.Equal(t, uint32(1000), actualBlobSize, "Decoded blob size should match")
	offset += 4

	// commitment: Raw commitment bytes (32 bytes)
	require.Equal(t, testCommitment, signBytes[offset:offset+32], "Commitment should match")
	offset += 32

	// row_version: Big-endian encoded uint32 (4 bytes)
	expectedRowVersion := make([]byte, 4)
	binary.BigEndian.PutUint32(expectedRowVersion, 0)
	require.Equal(t, expectedRowVersion, signBytes[offset:offset+4], "Row version should be big-endian encoded")
	actualRowVersion := binary.BigEndian.Uint32(signBytes[offset : offset+4])
	require.Equal(t, uint32(0), actualRowVersion, "Decoded row version should match")
	offset += 4

	// height: Big-endian encoded int64 (8 bytes)
	expectedHeight := make([]byte, 8)
	binary.BigEndian.PutUint64(expectedHeight, 100)
	require.Equal(t, expectedHeight, signBytes[offset:offset+8], "Height should be big-endian encoded")
	actualHeight := binary.BigEndian.Uint64(signBytes[offset : offset+8])
	require.Equal(t, uint64(100), actualHeight, "Decoded height should match")
	offset += 8

	// creation_timestamp: UTC timestamp encoded using Go's time.Time.MarshalBinary() (15 bytes)
	expectedTimestamp, err := testTime.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, expectedTimestamp, signBytes[offset:offset+15], "Timestamp should match MarshalBinary encoding")
	// Verify we can decode it back
	var decodedTime time.Time
	err = decodedTime.UnmarshalBinary(signBytes[offset : offset+15])
	require.NoError(t, err)
	require.Equal(t, testTime, decodedTime, "Decoded timestamp should match original")
	offset += 15

	// signer_public_key: Raw bytes of signer address secp256k1 (20 bytes)
	expectedSignerAddr := sdk.AccAddress(pubKey.Address())
	require.Equal(t, expectedSignerAddr.Bytes(), signBytes[offset:offset+20], "Signer address should match")
	require.Len(t, expectedSignerAddr.Bytes(), 20, "Signer address should be 20 bytes")
	offset += 20

	// Verify we've consumed all bytes
	require.Equal(t, len(signBytes), offset, "Should have consumed all sign bytes")
}

func TestPaymentPromiseSignBytesWithNilPublicKey(t *testing.T) {
	// Test edge case where SignerPublicKey is nil
	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	promise := types.PaymentPromise{
		SignerPublicKey:   nil, // Nil public key
		Namespace:         make([]byte, 29),
		BlobSize:          1000,
		Commitment:        make([]byte, 32),
		RowVersion:        0,
		CreationTimestamp: testTime,
		Height:            100,
		ChainId:           "test-chain",
		Signature:         make([]byte, 64),
	}

	// Get sign bytes - should not include signer address when public key is nil
	signBytes, err := promise.SignBytes()
	require.NoError(t, err)

	// Expected length without signer_public_key: chain_id(10) + namespace(29) + blob_size(4) + commitment(32) + row_version(4) + height(8) + creation_timestamp(15)
	expectedLength := len("test-chain") + 29 + 4 + 32 + 4 + 8 + 15
	require.Equal(t, expectedLength, len(signBytes), "Sign bytes should not include signer address when public key is nil")
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
