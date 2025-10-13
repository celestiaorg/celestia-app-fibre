package validator

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sync"

	cmtmath "github.com/cometbft/cometbft/libs/math"
	core "github.com/cometbft/cometbft/types"
)

// ErrNotEnoughSignatures indicates that not enough signatures with total sufficient voting power were accumulated.
var ErrNotEnoughSignatures = fmt.Errorf("not enough signatures with voting power")

// SignatureSet collects and validates signatures from validators until thresholds are met.
// It is safe for concurrent use.
type SignatureSet struct {
	totalCount          int
	requiredBytesSigned []byte
	requiredVotingPower int64
	requiredCount       int

	mu             sync.Mutex
	votingPower    int64
	validatorCount int
	signatures     [][]byte
	done           chan struct{}
}

// NewSignatureSet creates a new [SignatureSet] for collecting and validating signatures.
func (s Set) NewSignatureSet(targetVotingPower, targetValidatorsCount cmtmath.Fraction, requiredBytesSigned []byte) *SignatureSet {
	// follows arithmetic logic from cometbft for calculating required voting power
	requiredVotingPower := s.TotalVotingPower() * int64(targetVotingPower.Numerator) / int64(targetVotingPower.Denominator)
	requiredCount := s.Size() * int(targetValidatorsCount.Numerator) / int(targetValidatorsCount.Denominator)

	return &SignatureSet{
		totalCount:          s.Size(),
		requiredBytesSigned: requiredBytesSigned,
		requiredVotingPower: requiredVotingPower,
		requiredCount:       requiredCount,
		signatures:          make([][]byte, 0, s.Size()),
		done:                make(chan struct{}),
	}
}

// Add validates and adds a signature from the given validator.
// Returns an error if the signature is invalid or the validator was already added.
// When both voting power and count thresholds are met, the done channel is closed.
func (ss *SignatureSet) Add(val *core.Validator, signature []byte) error {
	// verify signature if not missing
	pubKey := val.PubKey.Bytes()
	if !ed25519.Verify(ed25519.PublicKey(pubKey), ss.requiredBytesSigned, signature) {
		return fmt.Errorf("invalid signature from validator %s", val.Address.String())
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// add to collection
	ss.validatorCount++
	ss.votingPower += val.VotingPower
	ss.signatures = append(ss.signatures, signature)

	// check if thresholds are met and close done channel if not already closed
	if ss.votingPower < ss.requiredVotingPower || ss.validatorCount < ss.requiredCount {
		return nil
	}

	select {
	case <-ss.done:
	default:
		close(ss.done)
	}
	return nil
}

// Miss marks the validator as not responding without adding a signature.
func (ss *SignatureSet) Miss() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	ss.validatorCount++
	if ss.validatorCount < ss.totalCount {
		return
	}

	select {
	case <-ss.done:
	default:
		close(ss.done)
	}
}

// Await waits for the signature collection thresholds to be met or for the context to be cancelled.
// Returns nil if thresholds are met or error if not enough signatures with voting power accumulated or
// context errors.
func (ss *SignatureSet) Await(ctx context.Context) error {
	select {
	case <-ss.done:
		ss.mu.Lock()
		defer ss.mu.Unlock()
		if ss.votingPower < ss.requiredVotingPower {
			return ErrNotEnoughSignatures
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Signatures returns all collected signatures.
func (ss *SignatureSet) Signatures() [][]byte {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.signatures
}
