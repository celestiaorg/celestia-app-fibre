package types

import (
	"cosmossdk.io/errors"
)

var (
	ErrInvalidHostAddress = errors.Register(ModuleName, 1, "invalid address")
	ErrInvalidValidator   = errors.Register(ModuleName, 2, "invalid validator")
)
