package validator_test

import (
	"crypto/sha256"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/validator"
	"github.com/celestiaorg/rsema1d"
	"github.com/cometbft/cometbft/crypto/ed25519"
	core "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
)

func TestSet_Assign(t *testing.T) {
	commitment := rsema1d.Commitment{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	}

	t.Run("EmptySet", func(t *testing.T) {
		valSet := makeValidatorSet(0)

		val := valSet.Assign(commitment, 0)
		require.Nil(t, val)
	})

	t.Run("SingleValidator", func(t *testing.T) {
		valSet := makeValidatorSet(1)

		for i := range 10 {
			assignedVal := valSet.Assign(commitment, i)
			require.NotNil(t, assignedVal)
			require.Equal(t, valSet.Validators[0].Address.String(), assignedVal.Address.String())
		}
	})

	t.Run("Distribution", func(t *testing.T) {
		valSet := makeValidatorSet(100)

		hasher := sha256.New()
		hasher.Write([]byte("distribution-test"))
		var distCommitment rsema1d.Commitment
		copy(distCommitment[:], hasher.Sum(nil))

		counts := make(map[string]int)
		totalRows := 16384

		for i := range totalRows {
			val := valSet.Assign(distCommitment, i)
			counts[val.Address.String()]++
		}

		expectedPerValidator := totalRows / len(valSet.Validators)
		require.Equal(t, len(valSet.Validators), len(counts))

		lowest, highest := totalRows, 0
		for _, count := range counts {
			if count < lowest {
				lowest = count
			}
			if count > highest {
				highest = count
			}
			require.GreaterOrEqual(t, count, expectedPerValidator)
			require.LessOrEqual(t, count, expectedPerValidator+1)
		}
		t.Logf("Lowest assignments: %d, Highest assignments: %d", lowest, highest)
	})

	t.Run("Determinism", func(t *testing.T) {
		valSet := makeValidatorSet(3)

		numRows := 50
		firstRun := make([]*core.Validator, numRows)
		for i := range numRows {
			firstRun[i] = valSet.Assign(commitment, i)
		}

		for i := range numRows {
			val := valSet.Assign(commitment, i)
			require.Equal(t, firstRun[i].Address.String(), val.Address.String())
		}
	})
}

func makeValidatorSet(n int) validator.Set {
	validators := make([]*core.Validator, n)
	for i := range n {
		privKey := ed25519.GenPrivKey()
		validators[i] = core.NewValidator(privKey.PubKey(), 1)
	}
	return validator.Set{
		ValidatorSet: core.NewValidatorSet(validators),
		Height:       100,
	}
}
