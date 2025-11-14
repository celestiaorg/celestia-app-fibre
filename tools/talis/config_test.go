package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncNodeCountersUsesHighestIndex(t *testing.T) {
	resetNodeCounters()

	cfg := Config{
		Validators: []Instance{{Name: "validator-0"}, {Name: "validator-5"}},
		Bridges:    []Instance{{Name: "bridge-2"}},
		Lights:     []Instance{},
	}

	syncNodeCounters(cfg)

	require.Equal(t, uint32(6), valCount.Load())
	require.Equal(t, uint32(3), nodeCount.Load())
	require.Equal(t, uint32(0), lightCount.Load())
}

func TestNodeNameRespectsSyncedCounters(t *testing.T) {
	resetNodeCounters()

	cfg := Config{
		Experiment: "exp",
		ChainID:    "chain",
		Validators: []Instance{
			{Name: "validator-0", Provider: DigitalOcean},
			{Name: "validator-1", Provider: DigitalOcean},
		},
	}

	syncNodeCounters(cfg)

	cfg = cfg.WithDigitalOceanValidator("nyc3")

	require.Len(t, cfg.Validators, 3)
	require.Equal(t, "validator-2", cfg.Validators[2].Name)
}

func TestNeedsProvision(t *testing.T) {
	inst := Instance{PublicIP: pendingIPPlaceholder}
	require.True(t, inst.NeedsProvision())

	inst.PublicIP = "1.1.1.1"
	require.False(t, inst.NeedsProvision())

	inst.PublicIP = ""
	require.True(t, inst.NeedsProvision())
}
