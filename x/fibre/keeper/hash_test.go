package keeper_test

import (
	"crypto/sha256"
	"encoding/binary"
	"time"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

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
