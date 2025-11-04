//go:build !multiplexer

package cmd

import (
	"testing"

	"github.com/cometbft/cometbft/crypto"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtprivval "github.com/cometbft/cometbft/privval"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/assert"
)

// mockNode is a minimal mock implementation of nodeWithPrivValidator for testing
type mockNode struct {
	privVal cmttypes.PrivValidator
}

func (m *mockNode) PrivValidator() cmttypes.PrivValidator {
	return m.privVal
}

func TestIsValidatorNode(t *testing.T) {
	t.Run("returns true when PrivValidator exists and has valid public key", func(t *testing.T) {
		// Create a FilePV with a valid key
		privKey := ed25519.GenPrivKey()
		filePV := cmtprivval.NewFilePV(privKey, "", "")

		mockNode := &mockNode{privVal: filePV}

		// Test
		result := isValidatorNode(mockNode)
		assert.True(t, result, "should return true when PrivValidator exists and has valid public key")
	})

	t.Run("returns false when PrivValidator is nil", func(t *testing.T) {
		mockNode := &mockNode{privVal: nil}

		// Test
		result := isValidatorNode(mockNode)
		assert.False(t, result, "should return false when PrivValidator is nil")
	})

	t.Run("returns false when PrivValidator cannot get public key", func(t *testing.T) {
		// Create a mock PrivValidator that returns error on GetPubKey
		mockPV := &mockPrivValidator{getPubKeyErr: assert.AnError}
		mockNode := &mockNode{privVal: mockPV}

		// Test
		result := isValidatorNode(mockNode)
		assert.False(t, result, "should return false when PrivValidator cannot get public key")
	})
}

// mockPrivValidator is a minimal mock implementation of cmttypes.PrivValidator for testing
type mockPrivValidator struct {
	getPubKeyErr error
}

func (m *mockPrivValidator) GetPubKey() (crypto.PubKey, error) {
	if m.getPubKeyErr != nil {
		return nil, m.getPubKeyErr
	}
	return ed25519.GenPrivKey().PubKey(), nil
}

func (m *mockPrivValidator) SignVote(chainID string, vote *cmttypes.Vote) error {
	panic("not implemented")
}

func (m *mockPrivValidator) SignProposal(chainID string, proposal *cmttypes.Proposal) error {
	panic("not implemented")
}
