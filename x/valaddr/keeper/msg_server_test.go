package keeper_test

import (
	"errors"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/app"
	testutil "github.com/celestiaorg/celestia-app/v6/test/util"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/keeper"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestMsgSetFibreProviderInfo(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)

	validators, err := testApp.StakingKeeper.GetBondedValidatorsByPower(ctx)
	require.NoError(t, err)
	require.Greater(t, len(validators), 0)

	valAddrStr := validators[0].GetOperator()

	consPubKey, err := validators[0].ConsPubKey()
	require.NoError(t, err)
	consAddr := sdk.ConsAddress(consPubKey.Address())

	msg := &types.MsgSetFibreProviderInfo{
		Signer:    valAddrStr,
		IpAddress: "192.168.1.1",
	}

	err = msg.ValidateBasic()
	require.NoError(t, err)

	msgServer := keeper.NewMsgServerImpl(testApp.ValaddrKeeper)
	_, err = msgServer.SetFibreProviderInfo(ctx, msg)
	require.NoError(t, err)

	retrievedInfo, found := testApp.ValaddrKeeper.GetFibreProviderInfo(ctx, consAddr)
	require.True(t, found)
	require.Equal(t, msg.IpAddress, retrievedInfo.IpAddress)
}

func TestMsgSetFibreProviderInfoInvalidIP(t *testing.T) {
	valAddr := sdk.ValAddress("validator1")

	msg := &types.MsgSetFibreProviderInfo{
		Signer:    valAddr.String(),
		IpAddress: "invalid-ip",
	}

	err := msg.ValidateBasic()
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidIPAddress))
}

func TestMsgSetFibreProviderInfoEmptyIP(t *testing.T) {
	valAddr := sdk.ValAddress("validator1")

	msg := &types.MsgSetFibreProviderInfo{
		Signer:    valAddr.String(),
		IpAddress: "",
	}

	err := msg.ValidateBasic()
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidIPAddress))
}

func TestMsgSetFibreProviderInfoNonValidator(t *testing.T) {
	testApp, _ := testutil.SetupTestAppWithGenesisValSet(app.DefaultConsensusParams())
	ctx := testApp.NewContext(true)

	// Create an arbitrary validator address (not a real validator)
	arbitraryValAddr := sdk.ValAddress("arbitrary_val_addr")

	msg := &types.MsgSetFibreProviderInfo{
		Signer:    arbitraryValAddr.String(),
		IpAddress: "192.168.1.1",
	}

	err := msg.ValidateBasic()
	require.NoError(t, err)

	// Call the message server handler - should fail (validator not found)
	msgServer := keeper.NewMsgServerImpl(testApp.ValaddrKeeper)
	_, err = msgServer.SetFibreProviderInfo(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrIncorrectValidator))
}

func TestMsgSetFibreProviderInfoTooLongIP(t *testing.T) {
	valAddr := sdk.ValAddress([]byte("validator1"))

	// Create an IP address longer than 90 characters
	longIP := "2001:0db8:85a3:0000:0000:8a2e:0370:7334:2001:0db8:85a3:0000:0000:8a2e:0370:7334:extra:data:here"

	msg := &types.MsgSetFibreProviderInfo{
		Signer:    valAddr.String(),
		IpAddress: longIP,
	}

	err := msg.ValidateBasic()
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidIPAddress))
}
