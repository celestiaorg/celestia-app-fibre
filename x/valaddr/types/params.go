package types

import (
	"fmt"
)

// DefaultMissingInfoCheckHeight is the default height at which to check for missing validator info
// after the upgrade (if upgrade was at height X, then we check at X + missing_info_check_height)
const DefaultMissingInfoCheckHeight int64 = 100_000

// DefaultParams returns the default parameters for the valaddr module
func DefaultParams() Params {
	return Params{
		MissingInfoCheckHeight: DefaultMissingInfoCheckHeight,
	}
}

// Validate performs validation of the Params
func (p Params) Validate() error {
	if p.MissingInfoCheckHeight < 0 {
		return fmt.Errorf("missing_info_check_height must be non-negative, got %d", p.MissingInfoCheckHeight)
	}
	return nil
}
