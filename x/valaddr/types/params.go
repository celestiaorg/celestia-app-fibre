package types

import (
	"fmt"
)

// DefaultMissingInfoCheckHeight is the default height at which to check for missing validator info
// 0 means not set
const DefaultMissingInfoCheckHeight int64 = 0

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
