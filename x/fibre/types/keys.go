package types

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// ModuleName defines the module name
	ModuleName = "fibre"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// RouterKey defines the module's message routing key
	RouterKey = ModuleName

	// ParamsKey defines the key used for storing module parameters
	ParamsKey = "params"
)

// Store key prefixes
var (
	// EscrowAccountKeyPrefix is the prefix for escrow account keys
	EscrowAccountKeyPrefix = []byte{0x01}

	// WithdrawalKeyPrefix is the prefix for withdrawal keys
	WithdrawalKeyPrefix = []byte{0x02}

	// ProcessedPaymentPromiseKeyPrefix is the prefix for processed payment promise keys
	ProcessedPaymentPromiseKeyPrefix = []byte{0x03}

	// ParamsKeyPrefix is the prefix for params
	ParamsKeyPrefix = []byte{0x04}
)

// EscrowAccountKey returns the store key for an escrow account
func EscrowAccountKey(signer string) []byte {
	return append(EscrowAccountKeyPrefix, []byte(signer)...)
}

// WithdrawalKey returns the store key for a withdrawal
func WithdrawalKey(signer string, requestedTimestamp time.Time) []byte {
	key := append(WithdrawalKeyPrefix, []byte(signer)...)
	key = append(key, []byte("/")...)
	// Use timestamp as unique identifier since there's no ID field
	timestampBytes := sdk.FormatTimeBytes(requestedTimestamp)
	return append(key, timestampBytes...)
}

// WithdrawalsBySignerPrefix returns the prefix for all withdrawals by a signer
func WithdrawalsBySignerPrefix(signer string) []byte {
	return append(WithdrawalKeyPrefix, []byte(signer)...)
}

// ProcessedPaymentPromiseKey returns the store key for a processed payment promise
func ProcessedPaymentPromiseKey(hash []byte) []byte {
	return append(ProcessedPaymentPromiseKeyPrefix, hash...)
}
