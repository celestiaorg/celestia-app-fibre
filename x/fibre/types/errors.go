package types

import (
	errorsmod "cosmossdk.io/errors"
)

// x/fibre module sentinel errors
var (
	ErrInvalidSigner     = errorsmod.Register(ModuleName, 1, "invalid signer")
	ErrDuplicateSigner   = errorsmod.Register(ModuleName, 2, "duplicate signer")
	ErrInvalidBalance    = errorsmod.Register(ModuleName, 3, "invalid balance")
	ErrInvalidAmount     = errorsmod.Register(ModuleName, 4, "invalid amount")
	ErrInvalidTimestamp  = errorsmod.Register(ModuleName, 5, "invalid timestamp")
	ErrInvalidHash       = errorsmod.Register(ModuleName, 6, "invalid hash")
	ErrDuplicateHash     = errorsmod.Register(ModuleName, 7, "duplicate hash")
	ErrInsufficientFunds = errorsmod.Register(ModuleName, 8, "insufficient funds")
	ErrNotFound          = errorsmod.Register(ModuleName, 9, "not found")
	ErrAlreadyProcessed  = errorsmod.Register(ModuleName, 10, "already processed")
	ErrTimeout           = errorsmod.Register(ModuleName, 11, "timeout")
)
