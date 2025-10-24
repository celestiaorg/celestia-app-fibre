package types

import (
	"cosmossdk.io/errors"
)

var (
	ErrInvalidIPAddress = errors.Register(ModuleName, 1, "invalid IP address format")
	ErrInvalidSigner    = errors.Register(ModuleName, 2, "invalid signer address")
)
