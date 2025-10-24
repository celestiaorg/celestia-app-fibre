package keeper

import (
	"context"
	"net"

	"cosmossdk.io/errors"
	"github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the valaddr MsgServer interface
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

// SetFibreProviderInfo handles the MsgSetFibreProviderInfo message
func (ms msgServer) SetFibreProviderInfo(goCtx context.Context, msg *types.MsgSetFibreProviderInfo) (*types.MsgSetFibreProviderInfoResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	valAddr, err := sdk.ValAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, errors.Wrapf(types.ErrInvalidSigner, "invalid validator address: %v", err)
	}

	validator, err := ms.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		return nil, errors.Wrapf(types.ErrInvalidSigner, "validator not found: %v", err)
	}

	consPubKey, err := validator.ConsPubKey()
	if err != nil {
		return nil, errors.Wrapf(types.ErrInvalidSigner, "failed to get consensus public key: %v", err)
	}
	consAddr := sdk.ConsAddress(consPubKey.Address())

	if len(msg.IpAddress) > types.MaxIpLen {
		return nil, errors.Wrapf(types.ErrInvalidIPAddress, "IP address must be less than 90 characters, got %d", len(msg.IpAddress))
	}
	if net.ParseIP(msg.IpAddress) == nil {
		return nil, errors.Wrapf(types.ErrInvalidIPAddress, "invalid IP address: %s", msg.IpAddress)
	}

	info := types.FibreProviderInfo{
		IpAddress: msg.IpAddress,
	}

	if err := ms.Keeper.SetFibreProviderInfo(goCtx, consAddr, info); err != nil {
		return nil, errors.Wrap(err, "failed to set fibre provider info")
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeSetFibreProviderInfo,
			sdk.NewAttribute(types.AttributeKeyValidatorAddress, consAddr.String()),
			sdk.NewAttribute(types.AttributeKeyIPAddress, msg.IpAddress),
		),
	)

	return &types.MsgSetFibreProviderInfoResponse{}, nil
}
