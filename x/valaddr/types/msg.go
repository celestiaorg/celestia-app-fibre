package types

import (
	"net"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// EventTypeSetFibreProviderInfo is the event type for setting fibre provider info
	EventTypeSetFibreProviderInfo = "set_fibre_provider_info"

	// AttributeKeyValidatorAddress is the attribute key for consensus address
	AttributeKeyValidatorAddress = "validator_consensus_address"
	// AttributeKeyIPAddress is the attribute key for IP address
	AttributeKeyIPAddress = "ip_address"
)

var _ sdk.Msg = &MsgSetFibreProviderInfo{}

// ValidateBasic performs basic validation of the MsgSetFibreProviderInfo message
func (m *MsgSetFibreProviderInfo) ValidateBasic() error {
	// Validate validator address
	if m.Signer == "" {
		return errorsmod.Wrap(ErrInvalidSigner, "validator address cannot be empty")
	}
	_, err := sdk.ValAddressFromBech32(m.Signer)
	if err != nil {
		return errorsmod.Wrapf(ErrInvalidSigner, "invalid validator address: %v", err)
	}

	// Validate IP address
	if m.IpAddress == "" {
		return errorsmod.Wrap(ErrInvalidIPAddress, "IP address cannot be empty")
	}
	if len(m.IpAddress) > 90 {
		return errorsmod.Wrapf(ErrInvalidIPAddress, "IP address must be less than 90 characters, got %d", len(m.IpAddress))
	}
	if net.ParseIP(m.IpAddress) == nil {
		return errorsmod.Wrapf(ErrInvalidIPAddress, "invalid IP address format: %s", m.IpAddress)
	}

	return nil
}
