package types

import (
	"cosmossdk.io/errors"
)

// x/valaddr module sentinel errors
var (
	ErrInvalidIPAddress = errors.Register(ModuleName, 1, "invalid IP address format")
	ErrInvalidSigner    = errors.Register(ModuleName, 2, "invalid signer address")
	ErrNotValidator     = errors.Register(ModuleName, 3, "signer is not a validator")
)
