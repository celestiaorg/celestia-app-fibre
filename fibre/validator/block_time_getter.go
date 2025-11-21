package validator

import (
	"context"
	"time"
)

// BlockTimeGetter defines an interface for retrieving the blockchain's current block time.
type BlockTimeGetter interface {
	// GetBlockTime returns the current block time from the blockchain.
	GetBlockTime(context.Context) (time.Time, error)
}
