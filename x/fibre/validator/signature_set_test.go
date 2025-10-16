package validator_test

import (
	"sync"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/validator"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	core "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
)

// makeValidators creates n validators with the given voting power each
func makeValidators(n int, votingPower int64) ([]*core.Validator, []ed25519.PrivKey) {
	validators := make([]*core.Validator, n)
	privKeys := make([]ed25519.PrivKey, n)
	for i := 0; i < n; i++ {
		privKeys[i] = ed25519.GenPrivKey()
		validators[i] = core.NewValidator(privKeys[i].PubKey(), votingPower)
	}
	return validators, privKeys
}

type testSetup struct {
	validators []*core.Validator
	privKeys   []ed25519.PrivKey
	valSet     validator.Set
	sigSet     *validator.SignatureSet
	signBytes  []byte
}

func setupSignatureSet(numVals int, votingPower int64, votingPowerFrac, countFrac cmtmath.Fraction) *testSetup {
	signBytes := []byte("test message to sign")
	validators, privKeys := makeValidators(numVals, votingPower)
	valSet := validator.Set{
		ValidatorSet: core.NewValidatorSet(validators),
		Height:       100,
	}
	sigSet := valSet.NewSignatureSet(votingPowerFrac, countFrac, signBytes)

	return &testSetup{
		validators: validators,
		privKeys:   privKeys,
		valSet:     valSet,
		sigSet:     sigSet,
		signBytes:  signBytes,
	}
}

func TestSignatureSet(t *testing.T) {
	twoThirds := cmtmath.Fraction{Numerator: 2, Denominator: 3}
	half := cmtmath.Fraction{Numerator: 1, Denominator: 2}

	t.Run("NotEnoughVotingPower", func(t *testing.T) {
		s := setupSignatureSet(5, 10, twoThirds, twoThirds)

		// 5 validators * 10 voting power = 50 total
		// 2/3 of 50 = 33 requiredVotingPower
		// 2/3 of 5 = 3 requiredCount
		// Add 3 signatures (30 voting power, meets count threshold of 3 but not voting power threshold of 33)
		for i := 0; i < 3; i++ {
			signature, err := s.privKeys[i].Sign(s.signBytes)
			require.NoError(t, err)
			require.NoError(t, s.sigSet.Add(s.validators[i], signature))
		}

		sigs, err := s.sigSet.Signatures()
		require.ErrorIs(t, err, validator.ErrNotEnoughVotingPower)
		require.Nil(t, sigs)
	})

	t.Run("NotEnoughSignatures", func(t *testing.T) {
		s := setupSignatureSet(5, 10, twoThirds, twoThirds)

		// Add only 2 signatures (requiredCount = 3)
		for i := 0; i < 2; i++ {
			signature, err := s.privKeys[i].Sign(s.signBytes)
			require.NoError(t, err)
			require.NoError(t, s.sigSet.Add(s.validators[i], signature))
		}

		sigs, err := s.sigSet.Signatures()
		require.ErrorIs(t, err, validator.ErrNotEnoughSignatures)
		require.Nil(t, sigs)
	})

	t.Run("SuccessSequential", func(t *testing.T) {
		s := setupSignatureSet(5, 10, twoThirds, twoThirds)

		// Add 4 signatures (40 voting power, meets both thresholds)
		for i := 0; i < 4; i++ {
			signature, err := s.privKeys[i].Sign(s.signBytes)
			require.NoError(t, err)
			require.NoError(t, s.sigSet.Add(s.validators[i], signature))
		}

		// Check that Done() is closed
		select {
		case <-s.sigSet.Done():
		default:
			t.Fatal("Done() should be closed when thresholds are met")
		}

		sigs, err := s.sigSet.Signatures()
		require.NoError(t, err)
		require.Len(t, sigs, 4)
	})

	t.Run("SuccessConcurrent", func(t *testing.T) {
		s := setupSignatureSet(10, 10, twoThirds, twoThirds)

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				signature, err := s.privKeys[idx].Sign(s.signBytes)
				require.NoError(t, err)
				require.NoError(t, s.sigSet.Add(s.validators[idx], signature))
			}(i)
		}
		wg.Wait()

		// Check that Done() is closed
		select {
		case <-s.sigSet.Done():
		default:
			t.Fatal("Done() should be closed when thresholds are met")
		}

		sigs, err := s.sigSet.Signatures()
		require.NoError(t, err)
		require.Len(t, sigs, 10)
	})

	t.Run("InvalidSignature", func(t *testing.T) {
		s := setupSignatureSet(3, 10, half, half)

		wrongSignBytes := []byte("wrong message")
		signature, err := s.privKeys[0].Sign(wrongSignBytes)
		require.NoError(t, err)

		err = s.sigSet.Add(s.validators[0], signature)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid signature")
	})

	t.Run("MixedMissAndValid", func(t *testing.T) {
		s := setupSignatureSet(5, 10, half, half)

		// Add 3 valid signatures (30 voting power, meets threshold of 25)
		for i := 0; i < 3; i++ {
			signature, err := s.privKeys[i].Sign(s.signBytes)
			require.NoError(t, err)
			require.NoError(t, s.sigSet.Add(s.validators[i], signature))
		}

		// Check that Done() is closed
		select {
		case <-s.sigSet.Done():
		default:
			t.Fatal("Done() should be closed when thresholds are met")
		}

		sigs, err := s.sigSet.Signatures()
		require.NoError(t, err)
		require.Len(t, sigs, 3)
	})
}
