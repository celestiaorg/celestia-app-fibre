//go:build multiplexer

package abci

import (
	"os"
	"path/filepath"
	"testing"

	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidatorNode(t *testing.T) {
	t.Run("returns true when PrivValidatorKeyFile exists", func(t *testing.T) {
		// Create a temporary directory
		tmpDir := t.TempDir()
		cfg := cmtcfg.DefaultConfig()
		cfg.SetRoot(tmpDir)

		// Create the priv_validator_key.json file
		pvKeyFile := cfg.PrivValidatorKeyFile()
		err := os.MkdirAll(filepath.Dir(pvKeyFile), 0755)
		require.NoError(t, err)

		err = os.WriteFile(pvKeyFile, []byte(`{"address":"test","pub_key":{"type":"tendermint/PubKeyEd25519","value":"test"},"priv_key":{"type":"tendermint/PrivKeyEd25519","value":"test"}}`), 0644)
		require.NoError(t, err)

		// Test
		result := isValidatorNode(cfg)
		assert.True(t, result, "should return true when PrivValidatorKeyFile exists")
	})

	t.Run("returns false when PrivValidatorKeyFile does not exist", func(t *testing.T) {
		// Create a temporary directory
		tmpDir := t.TempDir()
		cfg := cmtcfg.DefaultConfig()
		cfg.SetRoot(tmpDir)

		// Don't create the priv_validator_key.json file

		// Test
		result := isValidatorNode(cfg)
		assert.False(t, result, "should return false when PrivValidatorKeyFile does not exist")
	})

	t.Run("returns false when PrivValidatorKeyFile path is invalid", func(t *testing.T) {
		// Create a config with invalid root
		cfg := cmtcfg.DefaultConfig()
		cfg.SetRoot("/nonexistent/path/that/does/not/exist")

		// Test
		result := isValidatorNode(cfg)
		assert.False(t, result, "should return false when PrivValidatorKeyFile path is invalid")
	})
}
