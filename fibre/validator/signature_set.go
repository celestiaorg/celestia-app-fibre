package validator

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"

	cmtmath "github.com/cometbft/cometbft/libs/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/privval"
	core "github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/libs/protoio"
)

// RawBytesSignBytesPrefix defines a domain separator prefix added to raw bytes to ensure the resulting
// signed message can't be confused with a consensus message, which could lead to double signing
const RawBytesSignBytesPrefix = "COMET::RAW_BYTES::SIGN"

// RawBytesMessageSignBytes returns the canonical bytes for signing raw data messages.
// It requires non-empty chainID, uniqueID, and rawBytes to prevent security issues.
// Returns error if any required parameter is empty or if marshaling fails.
func RawBytesMessageSignBytes(chainID, uniqueID string, rawBytes []byte) ([]byte, error) {
	if chainID == "" {
		return nil, errors.New("chainID cannot be empty")
	}

	if uniqueID == "" {
		return nil, fmt.Errorf("uniqueID cannot be empty")
	}

	if len(rawBytes) == 0 {
		return nil, fmt.Errorf("rawBytes cannot be empty")
	}

	prefix := []byte(RawBytesSignBytesPrefix)

	signRequest := &cmtproto.SignRawBytesRequest{
		ChainId:  chainID,
		RawBytes: rawBytes,
		UniqueId: uniqueID,
	}
	protoBytes, err := protoio.MarshalDelimited(signRequest)
	if err != nil {
		return nil, err
	}
	return append(prefix, protoBytes...), nil
}

// SignatureSet collects and validates signatures from validators.
// It is safe for concurrent use.
type SignatureSet struct {
	chainID             string
	uniqueID            string
	requiredBytesSigned []byte
	minRequiredVotingPower int64
	minRequiredSignatures  int

	mu          sync.Mutex
	votingPower int64
	signatures  [][]byte
}

// NewSignatureSet creates a new [SignatureSet] for collecting and validating signatures.
func (s Set) NewSignatureSet(targetVotingPower, targetValidatorsCount cmtmath.Fraction, chainID, uniqueID string, requiredBytesSigned []byte) *SignatureSet {
	minRequiredVotingPower := s.TotalVotingPower() * int64(targetVotingPower.Numerator) / int64(targetVotingPower.Denominator)
	minRequiredSignatures := s.Size() * int(targetValidatorsCount.Numerator) / int(targetValidatorsCount.Denominator)

	return &SignatureSet{
		chainID:                chainID,
		uniqueID:               uniqueID,
		requiredBytesSigned:    requiredBytesSigned,
		minRequiredVotingPower: minRequiredVotingPower,
		minRequiredSignatures:  minRequiredSignatures,
		signatures:             make([][]byte, 0, s.Size()),
	}
}

// Add validates and adds a signature from the given validator.
// Returns an error if the signature is invalid.
// Returns true if enough signatures have been collected to meet both thresholds.
func (ss *SignatureSet) Add(val *core.Validator, signature []byte) (bool, error) {
	// reconstruct the signed message using RawBytesMessageSignBytes
	signBytes, err := RawBytesMessageSignBytes(ss.chainID, ss.uniqueID, ss.requiredBytesSigned)
	if err != nil {
		return false, fmt.Errorf("failed to reconstruct sign bytes: %w", err)
	}

	// verify signature
	pubKey := val.PubKey.Bytes()
	if !ed25519.Verify(ed25519.PublicKey(pubKey), signBytes, signature) {
		return false, fmt.Errorf("invalid signature from validator %s", val.Address.String())
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// add to collection
	ss.votingPower += val.VotingPower
	ss.signatures = append(ss.signatures, signature)

	// check if thresholds are met
	return len(ss.signatures) >= ss.minRequiredSignatures && ss.votingPower >= ss.minRequiredVotingPower, nil
}

// Signatures returns all collected signatures if thresholds are met.
// Returns [NotEnoughSignaturesError] if either count or voting power threshold is not met.
// The error contains the partially collected signatures and threshold information.
func (ss *SignatureSet) Signatures() ([][]byte, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	countNotMet := len(ss.signatures) < ss.minRequiredSignatures
	powerNotMet := ss.votingPower < ss.minRequiredVotingPower
	if countNotMet || powerNotMet {
		return nil, &NotEnoughSignaturesError{
			Collected:      ss.signatures,
			RequiredCount:  ss.minRequiredSignatures,
			CollectedPower: ss.votingPower,
			RequiredPower:  ss.minRequiredVotingPower,
		}
	}

	return ss.signatures, nil
}

// NotEnoughSignaturesError indicates that signature collection did not meet the required thresholds.
// It contains the partially collected signatures and threshold information.
type NotEnoughSignaturesError struct {
	Collected      [][]byte
	RequiredCount  int
	CollectedPower int64
	RequiredPower  int64
}

func (e *NotEnoughSignaturesError) Error() string {
	switch {
	case len(e.Collected) < e.RequiredCount:
		return fmt.Sprintf("not enough signatures: collected %d, required %d", len(e.Collected), e.RequiredCount)
	case e.CollectedPower < e.RequiredPower:
		return fmt.Sprintf("not enough voting power: collected %d, required %d", e.CollectedPower, e.RequiredPower)
	default:
		panic("unreachable")
	}
}
