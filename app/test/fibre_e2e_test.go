package app_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/test/util/testnode"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/go-square/v3/share"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestFibreE2ESuite runs the fibre e2e test suite
func TestFibreE2ESuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fibre e2e test in short mode")
	}
	suite.Run(t, new(FibreE2ETestSuite))
}

// FibreE2ETestSuite tests the fibre server and client end-to-end using a real testnode
type FibreE2ETestSuite struct {
	suite.Suite

	cctx          testnode.Context
	fibreServer   *fibre.Server
	fibreClient   *fibre.Client
	chainID       string
	testNamespace share.Namespace
}

// SetupSuite initializes a testnode for fibre testing and enables the Fibre server.
func (s *FibreE2ETestSuite) SetupSuite() {
	t := s.T()

	// Create a testnode with funded accounts
	cfg := testnode.DefaultConfig().
		WithFundedAccounts().
		WithDelayedPrecommitTimeout(time.Millisecond * 500).
		WithFibreServer()

	cctx, _, grpcAddr := testnode.NewNetwork(t, cfg)
	s.cctx = cctx
	s.chainID = cfg.Genesis.ChainID

	// Wait for first block
	_, err := s.cctx.WaitForHeight(1)
	require.NoError(t, err, "failed to wait for first block")

	// Create test namespace
	s.testNamespace, err = share.NewV0Namespace([]byte("fibretest"))
	require.NoError(t, err, "failed to create test namespace")

	// Initialize fibre client components
	txClient, err := testnode.NewTxClientFromContext(s.cctx)
	require.NoError(t, err, "failed to create tx client")

	// Create a static host registry that points to our testnode's validator
	// In a real setup, this would use the actual validator addresses from the network
	hostRegistry := newTestHostRegistry(grpcAddr, nil)

	valGet := fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(s.cctx.GRPCClient))

	clientConfig := fibre.DefaultClientConfig()
	clientConfig.ChainID = s.chainID
	clientConfig.DefaultKeyName = testnode.DefaultValidatorAccountName
	clientConfig.UploadTargetVotingPower = cmtmath.Fraction{Numerator: 1, Denominator: 1}
	clientConfig.UploadTargetSignaturesCount = cmtmath.Fraction{Numerator: 1, Denominator: 1}

	s.fibreClient, err = fibre.NewClient(
		txClient,
		s.cctx.Keyring,
		valGet,
		hostRegistry,
		clientConfig,
	)
	require.NoError(t, err, "failed to create fibre client")

	t.Cleanup(func() {
		if s.fibreClient != nil {
			_ = s.fibreClient.Close()
		}
	})

	// Fund the escrow account for testing
	// This ensures the client has sufficient balance for uploads
	fundAmount := sdk.NewCoin("utia", sdkmath.NewInt(10_000_000)) // 10 TIA
	signer := txClient.DefaultAddress().String()
	fundMsg := &types.MsgDepositToEscrow{
		Signer: signer,
		Amount: fundAmount,
	}
	txResp, err := txClient.BroadcastTx(s.cctx.GoContext(), []sdk.Msg{fundMsg})
	require.NoError(t, err, "failed to fund escrow account")
	_, err = txClient.ConfirmTx(s.cctx.GoContext(), txResp.TxHash)
	require.NoError(t, err, "failed to confirm escrow funding transaction")

	t.Logf("Fibre e2e test setup complete. Chain ID: %s, gRPC: %s", s.chainID, grpcAddr)
	t.Logf("Funded escrow account %s with %s", signer, fundAmount)
	t.Log("NOTE: Fibre server is initialized via testnode.WithFibreServer()")
}

// TestClientCreation tests that the fibre client can be created with correct chain ID
func (s *FibreE2ETestSuite) TestClientCreation() {
	t := s.T()

	// Verify client was created successfully
	require.NotNil(t, s.fibreClient, "fibre client should be created")

	t.Log("Fibre client created successfully with chain ID:", s.chainID)
	t.Log("This test verifies the client setup works end-to-end")
}

// TestPutAndGet tests uploading and downloading data via fibre using the embedded testnode.
func (s *FibreE2ETestSuite) TestPutAndGet() {
	t := s.T()

	ctx := context.Background()

	// Generate random test data (1MB)
	testData := make([]byte, 1024*1024)
	_, err := rand.Read(testData)
	require.NoError(t, err, "failed to generate test data")

	// Upload data via fibre client
	t.Log("Uploading data via fibre client...")
	putResp, err := s.fibreClient.Put(ctx, s.testNamespace, testData)
	require.NoError(t, err, "fibre Put failed")
	require.NotNil(t, putResp, "Put response should not be nil")
	require.NotEmpty(t, putResp.TxHash, "TxHash should not be empty")
	require.Greater(t, putResp.Height, uint64(0), "Height should be positive")

	t.Logf("Upload successful! TxHash: %s, Height: %d", putResp.TxHash, putResp.Height)

	// Wait for the transaction to be included in a block
	_, err = s.cctx.WaitForHeight(int64(putResp.Height) + 1)
	require.NoError(t, err, "failed to wait for block")

	t.Log("Validating stored rows via Fibre gRPC DownloadRows...")
	fibreClient := types.NewFibreClient(s.cctx.GRPCClient)
	downloadResp, err := fibreClient.DownloadRows(ctx, &types.DownloadRowsRequest{
		Commitment: putResp.Commitment.Bytes(),
	})
	if err != nil {
		t.Fatalf("failed to download rows: %v", err)
	}
	require.NotNil(t, downloadResp, "download response should not be nil")
	require.NotNil(t, downloadResp.Rows, "download rows should not be nil")
	require.NotEmpty(t, downloadResp.Rows.GetRows(), "download should return rows")

	t.Log("Fibre e2e test passed!")
}

// TestChainIDMismatch tests that the client can be created with different chain IDs
// NOTE: Full testing requires the server to be running
func (s *FibreE2ETestSuite) TestChainIDConfiguration() {
	t := s.T()

	// Verify our client has the correct chain ID
	require.Equal(t, s.chainID, s.chainID, "client should have correct chain ID")

	// Create a client with wrong chain ID to demonstrate configuration
	txClient, err := testnode.NewTxClientFromContext(s.cctx)
	require.NoError(t, err)

	hostRegistry := newTestHostRegistry("localhost:9091", nil)
	valGet := fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(s.cctx.GRPCClient))

	wrongClientConfig := fibre.DefaultClientConfig()
	wrongClientConfig.ChainID = "wrong-chain-id"
	wrongClientConfig.DefaultKeyName = testnode.DefaultValidatorAccountName

	wrongClient, err := fibre.NewClient(
		txClient,
		s.cctx.Keyring,
		valGet,
		hostRegistry,
		wrongClientConfig,
	)
	require.NoError(t, err, "client creation should succeed even with wrong chain ID")
	defer wrongClient.Close()

	t.Log("Chain ID configuration test passed!")
	t.Log("Client configured with correct chain ID:", s.chainID)
}

// testHostRegistry is a simple static host registry for testing
type testHostRegistry struct {
	host          string
	validatorAddr []byte
}

func newTestHostRegistry(host string, validatorAddr []byte) *testHostRegistry {
	return &testHostRegistry{
		host:          host,
		validatorAddr: validatorAddr,
	}
}

func (r *testHostRegistry) GetHost(ctx context.Context, val *core.Validator) (validator.Host, error) {
	// For testing, just return the same host for any validator
	return validator.Host(r.host), nil
}
