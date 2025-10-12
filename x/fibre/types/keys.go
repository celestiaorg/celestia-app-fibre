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
	// PaymentPromiseKeyPrefix is the prefix for processed payment promise keys
	PaymentPromiseKeyPrefix = []byte{0x03}
	// ParamsKeyPrefix is the prefix for params
	ParamsKeyPrefix = []byte{0x04}
)

// EscrowAccountKey returns the store key for an escrow account
func EscrowAccountKey(signer string) []byte {
	return append(EscrowAccountKeyPrefix, []byte(signer)...)
}

// WithdrawalKey returns the store key for a withdrawal TODO: should we add a
// unique ID to the withdrawal key instead of keying based on requested
// timetstamp?
func WithdrawalKey(signer string, requestedTimestamp time.Time) []byte {
	key := WithdrawalsBySignerPrefix(signer)
	key = append(key, []byte("/")...)
	timestampBytes := sdk.FormatTimeBytes(requestedTimestamp)
	return append(key, timestampBytes...)
}

// WithdrawalsBySignerPrefix returns the prefix for all withdrawals by a signer
func WithdrawalsBySignerPrefix(signer string) []byte {
	return append(WithdrawalKeyPrefix, []byte(signer)...)
}

// PaymentPromiseKey returns the store key for a payment promise. Note: all
// payment promises that are stored in the SDK module state have already been
// processed.
func PaymentPromiseKey(hash []byte) []byte {
	return append(PaymentPromiseKeyPrefix, hash...)
}
