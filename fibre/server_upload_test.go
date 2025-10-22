package fibre_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/go-square/v3/share"
	"github.com/celestiaorg/rsema1d"
	"github.com/celestiaorg/rsema1d/field"
	"github.com/cometbft/cometbft/crypto"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	core "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	txsigning "github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/stretchr/testify/require"
)

func TestServerUploadRows(t *testing.T) {
	tests := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"SuccessfulUpload", testServerUploadRowsSuccess},
		{"InvalidPaymentPromise", testServerUploadRowsInvalidPromise},
		{"WrongChainID", testServerUploadRowsWrongChainID},
		{"TimestampTooOld", testServerUploadRowsTimestampTooOld},
		{"InvalidRowAssignment", testServerUploadRowsInvalidAssignment},
		{"InvalidRowProof", testServerUploadRowsInvalidProof},
		{"MissingRows", testServerUploadRowsMissingRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func testServerUploadRowsSuccess(t *testing.T) {
	// setup server and test data
	server, valSet, serverValidator, blob, namespace, blobCfg := setupServerTest(t)

	// create valid upload request for server's validator
	req := createValidUploadRequest(t, blob, namespace, valSet, serverValidator, blobCfg)

	// upload should succeed
	resp, err := server.UploadRows(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.ValidatorSignature)
	require.Len(t, resp.ValidatorSignature, ed25519.SignatureSize)
}

func testServerUploadRowsInvalidPromise(t *testing.T) {
	// setup server
	server, valSet, serverValidator, blob, namespace, blobCfg := setupServerTest(t)

	// create request with invalid promise (no signature)
	req := createValidUploadRequest(t, blob, namespace, valSet, serverValidator, blobCfg)
	req.Promise.Signature = nil

	// upload should fail
	_, err := server.UploadRows(t.Context(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "payment promise validation failed")
}

func testServerUploadRowsWrongChainID(t *testing.T) {
	// setup server
	server, valSet, serverValidator, blob, namespace, blobCfg := setupServerTest(t)

	// create request with wrong chain ID
	req := createValidUploadRequest(t, blob, namespace, valSet, serverValidator, blobCfg)
	req.Promise.ChainId = "wrong-chain"

	// upload should fail
	_, err := server.UploadRows(t.Context(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "chain ID mismatch")
}

func testServerUploadRowsTimestampTooOld(t *testing.T) {
	// setup server
	server, valSet, serverValidator, blob, namespace, blobCfg := setupServerTest(t)

	// create request with timestamp older than MaxClockDrift (default 10s)
	req := createValidUploadRequest(t, blob, namespace, valSet, serverValidator, blobCfg)

	// set timestamp to 15 seconds ago (exceeds default 10s MaxClockDrift)
	oldTimestamp := time.Now().Add(-15 * time.Second)

	// recreate the payment promise with old timestamp and re-sign
	keyring := makeTestKeyring(t)
	key, err := keyring.Key(fibre.DefaultKeyName)
	require.NoError(t, err)
	pubKey, err := key.GetPubKey()
	require.NoError(t, err)

	promise := &fibre.PaymentPromise{
		ChainID:           "celestia",
		Height:            100,
		Namespace:         namespace,
		BlobSize:          uint32(blob.Size()),
		BlobVersion:       0,
		Commitment:        blob.Commitment(),
		CreationTimestamp: oldTimestamp,
		SignerKey:         pubKey.(*secp256k1.PubKey),
	}

	signBytes, err := promise.SignBytes()
	require.NoError(t, err)
	signature, _, err := keyring.Sign(fibre.DefaultKeyName, signBytes, txsigning.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	promise.Signature = signature

	req.Promise = promise.ToProto()

	// upload should fail
	_, err = server.UploadRows(t.Context(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timestamp too old")
}

func testServerUploadRowsInvalidAssignment(t *testing.T) {
	// setup server
	server, valSet, serverValidator, blob, namespace, blobCfg := setupServerTest(t)

	// create request with rows assigned to a different validator
	req := createValidUploadRequest(t, blob, namespace, valSet, serverValidator, blobCfg)

	// get another validator's rows using the same config as the server
	totalRows := blobCfg.OriginalRows + blobCfg.ParityRows
	shardMap := valSet.Assign(rsema1d.Commitment(blob.Commitment()), totalRows)
	for val, indices := range shardMap {
		if val.Address.String() != serverValidator.Address.String() && len(indices) > 0 {
			// replace with another validator's rows
			req.Rows.Rows[0].Index = uint32(indices[0])
			break
		}
	}

	// upload should fail
	_, err := server.UploadRows(t.Context(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "row assignment verification failed")
}

func testServerUploadRowsInvalidProof(t *testing.T) {
	// setup server
	server, valSet, serverValidator, blob, namespace, blobCfg := setupServerTest(t)

	// create valid request
	req := createValidUploadRequest(t, blob, namespace, valSet, serverValidator, blobCfg)

	// corrupt the proof
	req.Rows.Rows[0].Proof[0] = []byte("invalid proof")

	// upload should fail
	_, err := server.UploadRows(t.Context(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "verification failed")
}

func testServerUploadRowsMissingRows(t *testing.T) {
	// setup server
	server, valSet, serverValidator, blob, namespace, blobCfg := setupServerTest(t)

	// create request with empty rows
	req := createValidUploadRequest(t, blob, namespace, valSet, serverValidator, blobCfg)
	req.Rows.Rows = nil

	// upload should fail
	_, err := server.UploadRows(t.Context(), req)
	require.Error(t, err)
}

// setupServerTest creates a server with all necessary test infrastructure.
func setupServerTest(t *testing.T) (*fibre.Server, validator.Set, *core.Validator, *fibre.Blob, share.Namespace, fibre.BlobConfig) {
	t.Helper()

	// create validator set (use enough validators for good distribution)
	validators, privKeys := makeTestValidators(t, 100)
	valSet := validator.Set{
		ValidatorSet: core.NewValidatorSet(validators),
		Height:       100,
	}

	// create server with memory store
	store := fibre.NewMemoryStore()
	cfg := fibre.DefaultServerConfig()

	// use first validator as the server's identity
	privVal := newTestPrivValidator(privKeys[0])

	// Find the server validator in the ValidatorSet by matching the address
	// Note: core.NewValidatorSet may reorder validators, so we can't assume validators[0] == privKeys[0]
	serverPubKey, err := privVal.GetPubKey()
	require.NoError(t, err)
	serverAddress := serverPubKey.Address()

	serverValidator, found := valSet.GetByAddress(serverAddress)
	require.True(t, found, "server validator not found in validator set")
	require.NotNil(t, serverValidator, "server validator is nil")

	// create server
	server := fibre.NewServer(
		privVal,
		&mockValidatorSetGetter{set: valSet},
		store,
		cfg,
	)

	// create test blob
	data := make([]byte, 256*1024) // 256 KiB
	_, err = rand.Read(data)
	require.NoError(t, err)

	blobCfg := fibre.DefaultBlobConfig()
	blob, err := fibre.NewBlob(data, blobCfg)
	require.NoError(t, err)

	namespace := share.MustNewV0Namespace([]byte("testns"))

	return server, valSet, serverValidator, blob, namespace, blobCfg
}

// createValidUploadRequest creates a valid UploadRowsRequest for the given validator.
func createValidUploadRequest(
	t *testing.T,
	blob *fibre.Blob,
	namespace share.Namespace,
	valSet validator.Set,
	serverValidator *core.Validator,
	blobCfg fibre.BlobConfig,
) *types.UploadRowsRequest {
	t.Helper()

	// create and sign payment promise
	keyring := makeTestKeyring(t)
	key, err := keyring.Key(fibre.DefaultKeyName)
	require.NoError(t, err)
	pubKey, err := key.GetPubKey()
	require.NoError(t, err)

	promise := &fibre.PaymentPromise{
		ChainID:           "celestia",
		Height:            100,
		Namespace:         namespace,
		BlobSize:          uint32(blob.Size()),
		BlobVersion:       0,
		Commitment:        blob.Commitment(),
		CreationTimestamp: time.Now(),
		SignerKey:         pubKey.(*secp256k1.PubKey),
	}

	signBytes, err := promise.SignBytes()
	require.NoError(t, err)
	signature, _, err := keyring.Sign(fibre.DefaultKeyName, signBytes, txsigning.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	promise.Signature = signature

	// get row assignment for server validator using the same config as the server
	totalRows := blobCfg.OriginalRows + blobCfg.ParityRows
	shardMap := valSet.Assign(rsema1d.Commitment(blob.Commitment()), totalRows)

	rowIndices := shardMap[serverValidator]
	require.NotEmpty(t, rowIndices, "server validator has no rows assigned")

	// create rows with proofs
	rows := make([]*types.Row, len(rowIndices))
	for i, rowIndex := range rowIndices {
		rowProof, err := blob.Row(rowIndex)
		require.NoError(t, err)
		rows[i] = &types.Row{
			Index: uint32(rowIndex),
			Data:  rowProof.Row,
			Proof: rowProof.RowProof.RowProof,
		}
	}

	// flatten RLC coefficients
	rlcCoeffs := blob.RLCCoeffs()
	rlcCoeffsBytes := make([]byte, len(rlcCoeffs)*16)
	for i, coeff := range rlcCoeffs {
		b := field.ToBytes128(coeff)
		copy(rlcCoeffsBytes[i*16:(i+1)*16], b[:])
	}

	return &types.UploadRowsRequest{
		Promise: promise.ToProto(),
		Rows: &types.Rows{
			Rows: rows,
			Rlc:  &types.Rows_Coefficients{Coefficients: rlcCoeffsBytes},
		},
	}
}

// testPrivValidator is a simple mock PrivValidator for testing.
type testPrivValidator struct {
	privKey crypto.PrivKey
}

func newTestPrivValidator(privKey crypto.PrivKey) *testPrivValidator {
	return &testPrivValidator{privKey: privKey}
}

func (m *testPrivValidator) GetPubKey() (crypto.PubKey, error) {
	return m.privKey.PubKey(), nil
}

func (m *testPrivValidator) SignRawBytes(chainID, uniqueID string, rawBytes []byte) ([]byte, error) {
	return m.privKey.Sign(rawBytes)
}

func (m *testPrivValidator) SignVote(chainID string, vote *cmtproto.Vote) error {
	return nil
}

func (m *testPrivValidator) SignProposal(chainID string, proposal *cmtproto.Proposal) error {
	return nil
}

func (m *testPrivValidator) GetAddress() core.Address {
	return m.privKey.PubKey().Address()
}
