package grpc_test

import (
	"testing"
	"time"

	"github.com/celestiaorg/celestia-app/v6/app"
	"github.com/celestiaorg/celestia-app/v6/app/encoding"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator/grpc"
	"github.com/celestiaorg/celestia-app/v6/test/util/testnode"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	core "github.com/cometbft/cometbft/types"
	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestIntegrationTestSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping grpc host registry test in short mode.")
	}
	suite.Run(t, &IntegrationTestSuite{})
}

type IntegrationTestSuite struct {
	suite.Suite

	ecfg         encoding.Config
	cctx         testnode.Context
	hostRegistry *grpc.HostRegistry
}

func (s *IntegrationTestSuite) SetupSuite() {
	t := s.T()

	cfg := testnode.DefaultConfig().WithFundedAccounts().WithDelayedPrecommitTimeout(time.Millisecond * 500)
	cctx, _, _ := testnode.NewNetwork(t, cfg)

	s.cctx = cctx
	s.ecfg = encoding.MakeConfig(app.ModuleEncodingRegisters...)

	s.hostRegistry = grpc.NewHostRegistry(types.NewQueryClient(s.cctx.GRPCClient))
}

func (s *IntegrationTestSuite) TestGetHost() {
	t := s.T()

	// Wait for at least one block to be produced before querying
	_, err := s.cctx.WaitForHeight(1)
	require.NoError(t, err, "failed to wait for first block")

	// Use the GRPCClient to setup a cmtservice query client and query the validator set
	tmserviceClient := cmtservice.NewServiceClient(s.cctx.GRPCClient)
	valSetResp, err := tmserviceClient.GetLatestValidatorSet(s.cctx.GoContext(), &cmtservice.GetLatestValidatorSetRequest{})
	require.NoError(t, err)
	require.NotNil(t, valSetResp)
	require.NotEmpty(t, valSetResp.Validators, "validator set should not be empty")

	// Test GetHost for each validator in the set
	// In a fresh testnode, validators won't have fibre provider info registered yet,
	// so we expect GetHost to return an error for each validator
	for i, cmtVal := range valSetResp.Validators {
		// Convert cmtservice.Validator address (Bech32 string) to core.Validator
		// The address is in Bech32 format (e.g., celestiavalcons...)
		consAddr, err := sdk.ConsAddressFromBech32(cmtVal.Address)
		require.NoError(t, err, "failed to decode validator consensus address for validator %d", i)

		// Create a core.Validator with the address
		coreVal := &core.Validator{
			Address:     consAddr.Bytes(),
			VotingPower: cmtVal.VotingPower,
		}

		// Get the host for this validator
		// In a fresh testnode, validators won't have fibre provider info registered,
		// so we expect an error here
		host, err := s.hostRegistry.GetHost(s.cctx.GoContext(), coreVal)
		if err != nil {
			// Expected error: host not found for validator
			require.ErrorContains(t, err, "host not found for validator")
			t.Logf("Validator %d with address %s does not have fibre provider info registered (expected)", i, cmtVal.Address)
		} else {
			// If host is found (e.g., if fibre provider info was registered), verify it's not empty
			require.NotEmpty(t, host.String(), "host should not be empty for validator %d", i)
			t.Logf("Validator %d with address %s has host: %s", i, cmtVal.Address, host.String())
		}
	}

	// Now try with a fake validator (not in state), should error
	fakeAddr := make([]byte, 20) // Standard address length
	for i := range fakeAddr {
		fakeAddr[i] = 0xFF // Set to all FFs
	}
	fakeVal := &core.Validator{
		Address:     fakeAddr,
		VotingPower: 100,
	}
	_, err = s.hostRegistry.GetHost(s.cctx.GoContext(), fakeVal)
	require.Error(t, err, "should error when getting host for non-existent validator")
}
