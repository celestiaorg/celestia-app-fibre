package fibre_test

import (
	"context"
	"log/slog"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/celestiaorg/celestia-app/v6/app"
	"github.com/celestiaorg/celestia-app/v6/app/encoding"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	grpcfibre "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/v6/pkg/user"
	"github.com/celestiaorg/celestia-app/v6/test/util/testnode"
	fibretypes "github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	valtypes "github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	"github.com/celestiaorg/go-square/v3/share"
	"github.com/cometbft/cometbft/privval"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestFibreE2ETestSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fibre Put e2e test in short mode.")
	}
	suite.Run(t, &FibreE2ETestSuite{})
}

type FibreE2ETestSuite struct {
	suite.Suite

	ecfg             encoding.Config
	cctx             testnode.Context
	validatorPrivVal *privval.FilePV

	fibreServer        *fibre.Server
	grpcServer         *grpc.Server
	grpcAddr           string
	fibreStore         *fibre.Store
	hostRegistry       *grpcfibre.HostRegistry
	validatorSetGetter validator.SetGetter

	fibreClient   *fibre.Client
	clientKeyring keyring.Keyring
}

func (s *FibreE2ETestSuite) SetupSuite() {
	t := s.T()

	// Setup testnode with funded accounts
	// We specify fibre.DefaultKeyName as a funded account for the client
	cfg := testnode.DefaultConfig().
		WithFundedAccounts(fibre.DefaultKeyName).
		WithDelayedPrecommitTimeout(time.Millisecond * 500)
	cctx, _, _ := testnode.NewNetwork(t, cfg)

	privValKeyFile := cfg.UniversalTestingConfig.TmConfig.PrivValidatorKeyFile()
	privValStateFile := cfg.UniversalTestingConfig.TmConfig.PrivValidatorStateFile()
	filePV := privval.LoadFilePV(privValKeyFile, privValStateFile)

	s.validatorPrivVal = filePV
	s.cctx = cctx
	s.ecfg = encoding.MakeConfig(app.ModuleEncodingRegisters...)

	// Wait for at least one block to be produced
	_, err := s.cctx.WaitForHeight(1)
	require.NoError(t, err, "failed to wait for first block")

	// Setup the fibre server
	s.setupFibreServer(t)

	// Setup the fibre client
	s.setupFibreClient(t)
}

func (s *FibreE2ETestSuite) setupFibreServer(t *testing.T) {
	// Create a store for the fibre server
	s.fibreStore = fibre.NewMemoryStore(fibre.DefaultStoreConfig())

	// Create a listener for the gRPC server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	// Store the address as localhost:port instead of 127.0.0.1:port
	// This avoids url.Parse() issues in the host registry
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	s.grpcAddr = "localhost:" + port

	// Create query client for fibre module
	queryClient := fibretypes.NewQueryClient(s.cctx.GRPCClient)

	// Create validator set getter using the comet block service client
	blockAPIClient := coregrpc.NewBlockAPIClient(s.cctx.GRPCClient)
	s.validatorSetGetter = grpcfibre.NewSetGetter(blockAPIClient)

	// Create server config
	serverCfg := fibre.DefaultServerConfig()
	serverCfg.ChainID = s.cctx.ChainID
	serverCfg.Log = slog.Default().With("component", "fibre-server")

	s.fibreServer, err = fibre.NewServer(
		s.validatorPrivVal,
		queryClient,
		s.validatorSetGetter,
		s.fibreStore,
		serverCfg,
	)
	require.NoError(t, err)

	// Start the gRPC server with increased max message size
	s.grpcServer = grpc.NewServer(
		grpc.MaxRecvMsgSize(50*1024*1024), // 50 MB
		grpc.MaxSendMsgSize(50*1024*1024), // 50 MB
	)
	fibretypes.RegisterFibreServer(s.grpcServer, s.fibreServer)
	go func() { _ = s.grpcServer.Serve(listener) }()
}

func (s *FibreE2ETestSuite) setupFibreClient(t *testing.T) {
	// Use the testnode's keyring which has the funded account we specified
	s.clientKeyring = s.cctx.Keyring

	// Create a TxClient for the fibre client using the funded account
	txClient, err := user.SetupTxClient(
		s.cctx.GoContext(),
		s.clientKeyring,
		s.cctx.GRPCClient,
		s.ecfg,
		user.WithDefaultAccount(fibre.DefaultKeyName),
	)
	require.NoError(t, err)

	// Create host registry using valaddr query client
	valtypeQueryClient := valtypes.NewQueryClient(s.cctx.GRPCClient)
	s.hostRegistry = grpcfibre.NewHostRegistry(valtypeQueryClient)

	// Create client config with custom NewClientFn that has increased message size limits
	clientCfg := fibre.DefaultClientConfig()
	clientCfg.ChainID = s.cctx.ChainID
	clientCfg.Log = slog.Default().With("component", "fibre-client")
	clientCfg.NewClientFn = func(ctx context.Context, val *core.Validator) (grpcfibre.Client, error) {
		host, err := s.hostRegistry.GetHost(ctx, val)
		if err != nil {
			return nil, err
		}

		conn, err := grpc.NewClient(host.String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(50*1024*1024), // 50 MB
				grpc.MaxCallSendMsgSize(50*1024*1024), // 50 MB
			),
		)
		if err != nil {
			return nil, err
		}

		return &fibreClientCloser{
			FibreClient: fibretypes.NewFibreClient(conn),
			conn:        conn,
		}, nil
	}

	// Create the fibre client
	s.fibreClient, err = fibre.NewClient(
		txClient,
		s.clientKeyring,
		s.validatorSetGetter,
		s.hostRegistry,
		clientCfg,
	)
	require.NoError(t, err)
}

// fibreClientCloser wraps a FibreClient and grpc.ClientConn to implement grpcfibre.Client
type fibreClientCloser struct {
	fibretypes.FibreClient
	conn *grpc.ClientConn
}

func (f *fibreClientCloser) Close() error {
	return f.conn.Close()
}

func (s *FibreE2ETestSuite) TearDownSuite() {
	if s.grpcServer != nil {
		s.grpcServer.Stop()
	}
	if s.fibreServer != nil {
		_ = s.fibreServer.Stop()
	}
	if s.fibreClient != nil {
		_ = s.fibreClient.Close()
	}
	if s.fibreStore != nil {
		_ = s.fibreStore.Close()
	}
}

func (s *FibreE2ETestSuite) Test01RegisterValidator() {
	t := s.T()
	ctx := s.cctx.GoContext()

	// Get the validator's consensus address and operator address
	stakingClient := stakingtypes.NewQueryClient(s.cctx.GRPCClient)
	validatorsResp, err := stakingClient.Validators(ctx, &stakingtypes.QueryValidatorsRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, validatorsResp.Validators, "staking validators should not be empty")

	// In a single validator testnode, just use the first (and only) validator
	validator := validatorsResp.Validators[0]
	valOperatorAddr := validator.OperatorAddress

	// Get the consensus address from the validator's consensus pubkey
	var consPubKey cryptotypes.PubKey
	err = s.ecfg.InterfaceRegistry.UnpackAny(validator.ConsensusPubkey, &consPubKey)
	require.NoError(t, err, "failed to unpack consensus pubkey")
	require.NotNil(t, consPubKey, "consensus pubkey should not be nil")
	consAddr := sdk.ConsAddress(consPubKey.Address())
	t.Logf("Using validator operator address: %s, consensus address: %s", valOperatorAddr, consAddr.String())

	// Create a TxClient to submit the transaction
	txClient, err := testnode.NewTxClientFromContext(s.cctx)
	require.NoError(t, err, "failed to create tx client")

	// Create and submit MsgSetFibreProviderInfo
	// Use the gRPC address of our test server (host:port format, no scheme)
	testHost := s.grpcAddr
	msg := &valtypes.MsgSetFibreProviderInfo{
		Signer: valOperatorAddr,
		Host:   testHost,
	}

	// Submit the transaction
	txResp, err := txClient.SubmitTx(ctx, []sdk.Msg{msg}, user.SetGasLimit(200000), user.SetFee(5000))
	require.NoError(t, err, "failed to submit transaction")
	require.Equal(t, uint32(0), txResp.Code, "transaction failed with code %d", txResp.Code)
	t.Logf("Validator registered successfully. TxHash: %s, Height: %d", txResp.TxHash, txResp.Height)

	// Wait for the transaction to be processed
	_, err = s.cctx.WaitForHeight(int64(txResp.Height + 1))
	require.NoError(t, err)

	// Verify the host was registered by querying the valaddr module
	valtypeQueryClient := valtypes.NewQueryClient(s.cctx.GRPCClient)
	queryResp, err := valtypeQueryClient.FibreProviderInfo(ctx, &valtypes.QueryFibreProviderInfoRequest{
		ValidatorConsensusAddress: consAddr.String(),
	})
	require.NoError(t, err, "failed to query fibre provider info")
	require.NotNil(t, queryResp)
	require.True(t, queryResp.Found, "validator should be found")
	require.Equal(t, testHost, queryResp.Info.Host, "registered host should match")

	t.Logf("Verified validator fibre provider info: Host=%s", queryResp.Info.Host)
}

func (s *FibreE2ETestSuite) Test02FundEscrowAccount() {
	t := s.T()
	ctx := s.cctx.GoContext()

	// Get the client's address from the keyring (same keyring used by fibre client)
	clientAddr := s.fibreClient.Config().DefaultKeyName
	clientAccAddr, err := s.clientKeyring.Key(clientAddr)
	require.NoError(t, err)
	clientAccAddrBech32, err := clientAccAddr.GetAddress()
	require.NoError(t, err)
	clientAccAddrStr := clientAccAddrBech32.String()
	t.Logf("Client address: %s", clientAccAddrStr)

	// Create a TxClient using the same keyring as the fibre client
	txClient, err := user.SetupTxClient(
		s.cctx.GoContext(),
		s.clientKeyring,
		s.cctx.GRPCClient,
		s.ecfg,
		user.WithDefaultAccount(fibre.DefaultKeyName),
	)
	require.NoError(t, err)

	// Query the escrow account before funding (should not exist)
	fibreQueryClient := fibretypes.NewQueryClient(s.cctx.GRPCClient)
	queryResp, err := fibreQueryClient.EscrowAccount(ctx, &fibretypes.QueryEscrowAccountRequest{
		Signer: clientAccAddrStr,
	})
	require.NoError(t, err)
	require.False(t, queryResp.Found, "escrow account should not exist yet")
	t.Log("Verified escrow account does not exist before funding")

	// Create and submit MsgDepositToEscrow
	depositAmount := sdk.NewInt64Coin(appconsts.BondDenom, 50_000_000) // 50 TIA
	depositMsg := &fibretypes.MsgDepositToEscrow{
		Signer: clientAccAddrStr,
		Amount: depositAmount,
	}

	// Submit the deposit transaction
	depositResp, err := txClient.SubmitTx(ctx, []sdk.Msg{depositMsg}, user.SetGasLimit(200000), user.SetFee(5000))
	require.NoError(t, err, "failed to submit deposit transaction")
	require.Equal(t, uint32(0), depositResp.Code, "deposit transaction failed with code %d", depositResp.Code)
	t.Logf("Deposited %s to escrow account. TxHash: %s, Height: %d", depositAmount, depositResp.TxHash, depositResp.Height)

	// Wait for the deposit to be processed
	_, err = s.cctx.WaitForHeight(int64(depositResp.Height + 1))
	require.NoError(t, err)

	// Query the escrow account after funding and verify the balance
	queryResp, err = fibreQueryClient.EscrowAccount(ctx, &fibretypes.QueryEscrowAccountRequest{
		Signer: clientAccAddrStr,
	})
	require.NoError(t, err, "failed to query escrow account")
	require.True(t, queryResp.Found, "escrow account should exist after funding")
	require.NotNil(t, queryResp.EscrowAccount, "escrow account should not be nil")

	// Verify the balance matches the deposit amount
	require.Equal(t, clientAccAddrStr, queryResp.EscrowAccount.Signer, "signer should match")
	require.Equal(t, depositAmount, queryResp.EscrowAccount.Balance, "balance should equal deposit amount")
	require.Equal(t, depositAmount, queryResp.EscrowAccount.AvailableBalance, "available balance should equal deposit amount")

	t.Logf("Escrow account balance verified:")
	t.Logf("  Balance: %s", queryResp.EscrowAccount.Balance)
	t.Logf("  Available Balance: %s", queryResp.EscrowAccount.AvailableBalance)

	// Test: Deposit additional funds and verify balance is updated
	additionalDeposit := sdk.NewInt64Coin(appconsts.BondDenom, 25_000_000) // 25 TIA
	depositMsg2 := &fibretypes.MsgDepositToEscrow{
		Signer: clientAccAddrStr,
		Amount: additionalDeposit,
	}

	depositResp2, err := txClient.SubmitTx(ctx, []sdk.Msg{depositMsg2}, user.SetGasLimit(200000), user.SetFee(5000))
	require.NoError(t, err, "failed to submit second deposit transaction")
	require.Equal(t, uint32(0), depositResp2.Code, "second deposit transaction failed")
	t.Logf("Deposited additional %s to escrow account. TxHash: %s", additionalDeposit, depositResp2.TxHash)

	// Wait for the second deposit to be processed
	_, err = s.cctx.WaitForHeight(int64(depositResp2.Height + 1))
	require.NoError(t, err)

	// Query the escrow account and verify the updated balance
	queryResp3, err := fibreQueryClient.EscrowAccount(ctx, &fibretypes.QueryEscrowAccountRequest{
		Signer: clientAccAddrStr,
	})
	require.NoError(t, err, "failed to query escrow account after second deposit")
	require.True(t, queryResp3.Found, "escrow account should still exist")

	expectedBalance := depositAmount.Add(additionalDeposit)
	require.Equal(t, expectedBalance, queryResp3.EscrowAccount.Balance, "balance should equal total deposits")
	require.Equal(t, expectedBalance, queryResp3.EscrowAccount.AvailableBalance, "available balance should equal total deposits")

	t.Logf("Updated escrow account balance verified:")
	t.Logf("  Balance: %s", queryResp3.EscrowAccount.Balance)
	t.Logf("  Available Balance: %s", queryResp3.EscrowAccount.AvailableBalance)
	t.Log("Escrow account funding test completed successfully!")
}

func (s *FibreE2ETestSuite) Test03Put() {
	t := s.T()
	ctx := s.cctx.GoContext()

	// Note: This test assumes the escrow account has been funded (see Test02FundEscrowAccount)
	// and the validator has been registered (see Test01RegisterValidator)

	// Wait for a new block to ensure blockchain timestamp is current
	// This avoids clock skew issues with payment promise validation
	currentHeight, err := s.cctx.LatestHeight()
	require.NoError(t, err)
	_, err = s.cctx.WaitForHeight(currentHeight + 1)
	require.NoError(t, err)
	t.Log("Waited for new block to sync timestamps")

	// Create test data (using smaller size for faster test execution)
	blobSize := 4 * 1024 // 4 KiB
	testData := make([]byte, blobSize)
	_, err = rand.Read(testData)
	require.NoError(t, err)

	// Create a test namespace
	ns := share.MustNewV0Namespace([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x00})

	// Call Put method
	t.Log("Calling Put method...")
	putResult, err := s.fibreClient.Put(ctx, ns, testData)
	require.NoError(t, err, "Put operation failed")

	// Verify the result
	require.NotEmpty(t, putResult.Commitment.String(), "commitment should not be empty")
	require.NotEmpty(t, putResult.ValidatorSignatures, "validator signatures should not be empty")
	require.NotEmpty(t, putResult.TxHash, "transaction hash should not be empty")
	require.Greater(t, putResult.Height, uint64(0), "height should be greater than 0")

	t.Logf("Put operation successful!")
	t.Logf("  Commitment: %s", putResult.Commitment.String())
	t.Logf("  Validator Signatures: %d", len(putResult.ValidatorSignatures))
	t.Logf("  TxHash: %s", putResult.TxHash)
	t.Logf("  Height: %d", putResult.Height)

	// Verify the data was stored in the server's store
	storedRows, err := s.fibreStore.Get(ctx, putResult.Commitment)
	require.NoError(t, err, "failed to get stored rows")
	require.NotNil(t, storedRows, "stored rows should not be nil")
	require.NotEmpty(t, storedRows.Rows, "stored rows should not be empty")

	t.Logf("Verified %d rows stored in fibre server", len(storedRows.Rows))

	// Verify the PayForFibre transaction was included
	_, err = s.cctx.WaitForHeight(int64(putResult.Height + 1))
	require.NoError(t, err)

	t.Log("Put end-to-end test completed successfully!")
}
