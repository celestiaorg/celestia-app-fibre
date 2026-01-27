//go:build !multiplexer

package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetGenDocProvider(t *testing.T) {
	// Create a temporary directory for the test
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	// Create a genesis file in SDK AppGenesis format (with InitialHeight as integer)
	genesisFile := filepath.Join(configDir, "genesis.json")
	// This is the format that the SDK's init command generates
	genesisContent := `{
		"app_name": "celestia-appd",
		"app_version": "test",
		"genesis_time": "2024-01-01T00:00:00Z",
		"chain_id": "test-chain",
		"initial_height": 1,
		"app_hash": null,
		"app_state": {},
		"consensus": {
			"validators": [],
			"params": {
				"block": {"max_bytes": "22020096", "max_gas": "-1"},
				"evidence": {"max_age_num_blocks": "100000", "max_age_duration": "172800000000000", "max_bytes": "1048576"},
				"validator": {"pub_key_types": ["ed25519"]},
				"version": {"app": "0"},
				"abci": {"vote_extensions_enable_height": "0"}
			}
		}
	}`
	require.NoError(t, os.WriteFile(genesisFile, []byte(genesisContent), 0o644))

	// Create a CometBFT config pointing to the temp directory
	cfg := cmtcfg.DefaultConfig()
	cfg.SetRoot(tempDir)

	// Get the genesis doc provider and call it
	provider := getGenDocProvider(cfg)
	genDoc, err := provider()

	// Verify no error occurred (this would fail with the old DefaultGenesisDocProviderFunc
	// because it can't handle InitialHeight as an integer)
	require.NoError(t, err)

	// Verify the genesis doc was parsed correctly
	assert.Equal(t, "test-chain", genDoc.ChainID)
	assert.Equal(t, int64(1), genDoc.InitialHeight)
	assert.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), genDoc.GenesisTime)
}

// TestGetGenDocProvider_ChainID verifies that the chain ID can be correctly
// extracted from the genesis file. This is important because the Fibre server
// needs the chain ID from genesis (not from a flag/config) to properly identify
// the chain it's serving.
func TestGetGenDocProvider_ChainID(t *testing.T) {
	testCases := []struct {
		name            string
		chainID         string
		expectedChainID string
	}{
		{
			name:            "standard chain ID",
			chainID:         "celestia",
			expectedChainID: "celestia",
		},
		{
			name:            "test chain ID",
			chainID:         "test",
			expectedChainID: "test",
		},
		{
			name:            "mocha testnet",
			chainID:         "mocha-4",
			expectedChainID: "mocha-4",
		},
		{
			name:            "arabica testnet",
			chainID:         "arabica-11",
			expectedChainID: "arabica-11",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			configDir := filepath.Join(tempDir, "config")
			require.NoError(t, os.MkdirAll(configDir, 0o755))

			genesisFile := filepath.Join(configDir, "genesis.json")
			genesisContent := `{
				"app_name": "celestia-appd",
				"app_version": "test",
				"genesis_time": "2024-01-01T00:00:00Z",
				"chain_id": "` + tc.chainID + `",
				"initial_height": 1,
				"app_hash": null,
				"app_state": {},
				"consensus": {
					"validators": [],
					"params": {
						"block": {"max_bytes": "22020096", "max_gas": "-1"},
						"evidence": {"max_age_num_blocks": "100000", "max_age_duration": "172800000000000", "max_bytes": "1048576"},
						"validator": {"pub_key_types": ["ed25519"]},
						"version": {"app": "0"},
						"abci": {"vote_extensions_enable_height": "0"}
					}
				}
			}`
			require.NoError(t, os.WriteFile(genesisFile, []byte(genesisContent), 0o644))

			cfg := cmtcfg.DefaultConfig()
			cfg.SetRoot(tempDir)

			provider := getGenDocProvider(cfg)
			genDoc, err := provider()

			require.NoError(t, err)
			assert.Equal(t, tc.expectedChainID, genDoc.ChainID)
		})
	}
}

func TestGetGenDocProvider_FileNotFound(t *testing.T) {
	// Create a CometBFT config pointing to a non-existent directory
	cfg := cmtcfg.DefaultConfig()
	cfg.SetRoot("/non/existent/path")

	// Get the genesis doc provider and call it
	provider := getGenDocProvider(cfg)
	_, err := provider()

	// Verify an error occurred
	assert.Error(t, err)
}
