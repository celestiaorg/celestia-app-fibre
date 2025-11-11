package fibre

import "testing"

func TestPyroscopeConfigApplyDefaults(t *testing.T) {
	cfg := PyroscopeConfig{
		ServerAddress: "http://localhost:4040",
	}

	applied := cfg.applyDefaults()

	if applied.ApplicationName != defaultPyroscopeAppName {
		t.Fatalf("expected default application name %q got %q", defaultPyroscopeAppName, applied.ApplicationName)
	}
	if len(applied.ProfileTypes) == 0 {
		t.Fatalf("expected default profile types to be set")
	}
	if applied.Labels == nil {
		t.Fatalf("expected labels map to be initialized")
	}
}

func TestPyroscopeConfigApplyDefaultsTrimsData(t *testing.T) {
	cfg := PyroscopeConfig{
		ServerAddress:   "  http://pyroscope:4040 ",
		ApplicationName: " fibre-client ",
		ProfileTypes:    []string{" cpu ", "  "},
		Labels: map[string]string{
			" chain_id ": " mocha ",
			"":           "ignored",
		},
	}

	applied := cfg.applyDefaults()

	if applied.ServerAddress != "http://pyroscope:4040" {
		t.Fatalf("expected trimmed server address, got %q", applied.ServerAddress)
	}
	if len(applied.ProfileTypes) != 1 || applied.ProfileTypes[0] != "cpu" {
		t.Fatalf("expected trimmed profile types, got %#v", applied.ProfileTypes)
	}
	if applied.Labels["chain_id"] != "mocha" {
		t.Fatalf("expected trimmed label, got %#v", applied.Labels)
	}
	if _, exists := applied.Labels[""]; exists {
		t.Fatalf("expected empty label key to be removed")
	}
}
