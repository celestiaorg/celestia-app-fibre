package fibre

import (
	"context"
	"errors"
	"slices"
	"strings"

	otelpyroscope "github.com/grafana/otel-profiling-go"
	"github.com/grafana/pyroscope-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const defaultPyroscopeAppName = "celestia-fibre-client"

var defaultPyroscopeProfiles = []pyroscope.ProfileType{
	pyroscope.ProfileCPU,
	pyroscope.ProfileAllocObjects,
	pyroscope.ProfileAllocSpace,
	pyroscope.ProfileInuseObjects,
	pyroscope.ProfileInuseSpace,
	pyroscope.ProfileGoroutines,
	pyroscope.ProfileMutexCount,
	pyroscope.ProfileMutexDuration,
	pyroscope.ProfileBlockCount,
	pyroscope.ProfileBlockDuration,
}

// PyroscopeConfig configures optional continuous profiling for the Fibre client.
type PyroscopeConfig struct {
	// ServerAddress enables profiling when non-empty and sets the Pyroscope URL.
	ServerAddress string
	// ApplicationName overrides the name shown in Pyroscope (defaults to celestia-fibre-client).
	ApplicationName string
	// ProfileTypes is an optional list of Pyroscope profile identifiers (e.g. cpu, mem:alloc_objects).
	ProfileTypes []string
	// Labels attaches static labels to every profile pushed to Pyroscope.
	Labels map[string]string
	// EnableTracing annotates Pyroscope samples with active OpenTelemetry spans.
	EnableTracing bool
}

// DefaultPyroscopeProfileTypes returns the profile types enabled when none are specified.
func DefaultPyroscopeProfileTypes() []string {
	out := make([]string, len(defaultPyroscopeProfiles))
	for i, p := range defaultPyroscopeProfiles {
		out[i] = string(p)
	}
	return out
}

func (cfg PyroscopeConfig) enabled() bool {
	return strings.TrimSpace(cfg.ServerAddress) != ""
}

func (cfg PyroscopeConfig) clone() PyroscopeConfig {
	clone := PyroscopeConfig{
		ServerAddress:   cfg.ServerAddress,
		ApplicationName: cfg.ApplicationName,
		EnableTracing:   cfg.EnableTracing,
	}
	if len(cfg.ProfileTypes) > 0 {
		clone.ProfileTypes = slices.Clone(cfg.ProfileTypes)
	}
	if len(cfg.Labels) > 0 {
		clone.Labels = mapsClone(cfg.Labels)
	}
	return clone
}

func (cfg PyroscopeConfig) applyDefaults() PyroscopeConfig {
	out := cfg.clone()
	out.ServerAddress = strings.TrimSpace(out.ServerAddress)
	out.ApplicationName = strings.TrimSpace(out.ApplicationName)
	if out.ApplicationName == "" {
		out.ApplicationName = defaultPyroscopeAppName
	}
	if len(out.ProfileTypes) == 0 {
		out.ProfileTypes = DefaultPyroscopeProfileTypes()
	} else {
		trimmed := make([]string, 0, len(out.ProfileTypes))
		for _, profile := range out.ProfileTypes {
			if name := strings.TrimSpace(profile); name != "" {
				trimmed = append(trimmed, name)
			}
		}
		if len(trimmed) == 0 {
			trimmed = DefaultPyroscopeProfileTypes()
		}
		out.ProfileTypes = trimmed
	}
	if len(out.Labels) == 0 {
		out.Labels = make(map[string]string, 2)
	} else {
		clean := make(map[string]string, len(out.Labels))
		for key, value := range out.Labels {
			trimKey := strings.TrimSpace(key)
			trimValue := strings.TrimSpace(value)
			if trimKey == "" || trimValue == "" {
				continue
			}
			clean[trimKey] = trimValue
		}
		out.Labels = clean
	}
	return out
}

type pyroscopeHandle struct {
	profiler       *pyroscope.Profiler
	tracerProvider *sdktrace.TracerProvider
}

func newPyroscopeHandle(cfg PyroscopeConfig) (*pyroscopeHandle, PyroscopeConfig, error) {
	if !cfg.enabled() {
		return nil, PyroscopeConfig{}, nil
	}

	applied := cfg.applyDefaults()

	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: applied.ApplicationName,
		ServerAddress:   applied.ServerAddress,
		Tags:            mapsClone(applied.Labels),
		ProfileTypes:    toProfileTypes(applied.ProfileTypes),
	})
	if err != nil {
		return nil, PyroscopeConfig{}, err
	}

	var tp *sdktrace.TracerProvider
	if applied.EnableTracing {
		tp = sdktrace.NewTracerProvider()
		otel.SetTracerProvider(otelpyroscope.NewTracerProvider(
			tp,
			otelpyroscope.WithAppName(applied.ApplicationName),
			otelpyroscope.WithRootSpanOnly(true),
			otelpyroscope.WithAddSpanName(true),
			otelpyroscope.WithPyroscopeURL(applied.ServerAddress),
			otelpyroscope.WithProfileBaselineLabels(applied.Labels),
			otelpyroscope.WithProfileBaselineURL(true),
			otelpyroscope.WithProfileURL(true),
		))
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	return &pyroscopeHandle{
		profiler:       profiler,
		tracerProvider: tp,
	}, applied, nil
}

func (h *pyroscopeHandle) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}

	var errs error
	if h.profiler != nil {
		if err := h.profiler.Stop(); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	if h.tracerProvider != nil {
		if err := h.tracerProvider.Shutdown(ctx); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	return errs
}

func toProfileTypes(names []string) []pyroscope.ProfileType {
	if len(names) == 0 {
		return slices.Clone(defaultPyroscopeProfiles)
	}

	profiles := make([]pyroscope.ProfileType, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		profiles = append(profiles, pyroscope.ProfileType(name))
	}
	if len(profiles) == 0 {
		return slices.Clone(defaultPyroscopeProfiles)
	}
	return profiles
}

func mapsClone[M ~map[K]V, K comparable, V any](in M) M {
	if in == nil {
		return nil
	}
	out := make(M, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
