package validator

import (
	core "github.com/cometbft/cometbft/types"
)

// Set wraps [*core.ValidatorSet] with the height at which it is valid.
type Set struct {
	*core.ValidatorSet
	Height uint64
}
