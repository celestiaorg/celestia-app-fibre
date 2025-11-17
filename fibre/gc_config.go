package fibre

import (
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
)

// GCConfig holds garbage collection configuration
type GCConfig struct {
	// GCPercent sets the garbage collection target percentage.
	// A value of 50 means GC will run when heap grows 50% beyond live objects.
	// Lower values = more aggressive GC (more CPU, less memory).
	// Default: 100 (Go standard)
	// Recommended aggressive: 25-50
	GCPercent int

	// MemoryLimitGB sets a soft memory limit in GB.
	// Go will try to keep memory usage below this limit.
	// 0 means no limit (use GOGC only).
	// Recommended: 70-80% of available system RAM
	MemoryLimitGB int
}

// DefaultGCConfig returns recommended GC settings for validator nodes
func DefaultGCConfig() GCConfig {
	return GCConfig{
		GCPercent:     50,  // More aggressive GC (triggers at 50% heap growth)
		MemoryLimitGB: 300, // 300GB memory limit
	}
}


// ApplyGCConfig applies the GC configuration to the runtime
func ApplyGCConfig(cfg GCConfig) error {
	log := slog.Default()

	// Set GC percentage
	old := debug.SetGCPercent(cfg.GCPercent)
	log.Info("GC configuration applied",
		"gogc", cfg.GCPercent,
		"previous_gogc", old)

	// Set memory limit if specified
	if cfg.MemoryLimitGB > 0 {
		limitBytes := int64(cfg.MemoryLimitGB) * 1024 * 1024 * 1024
		oldLimit := debug.SetMemoryLimit(limitBytes)
		log.Info("GC memory limit set",
			"limit_gb", cfg.MemoryLimitGB,
			"previous_limit_mb", oldLimit/(1024*1024))
	}

	// Log initial GC stats for visibility
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Info("GC initial stats",
		"heap_mb", m.HeapAlloc/(1024*1024),
		"sys_mb", m.Sys/(1024*1024),
		"next_gc_mb", m.NextGC/(1024*1024),
		"num_gc", m.NumGC)

	return nil
}

// AutoConfigureGC automatically configures GC based on environment
// Checks GOGC and GOMEMLIMIT env vars first, then applies defaults
func AutoConfigureGC() error {
	log := slog.Default()
	cfg := DefaultGCConfig()

	// Check if GOGC is set in environment
	if gogcEnv := os.Getenv("GOGC"); gogcEnv != "" {
		if gogcEnv == "off" {
			cfg.GCPercent = -1 // Disable automatic GC
		} else if val, err := strconv.Atoi(gogcEnv); err == nil {
			cfg.GCPercent = val
		}
		log.Info("GC: Using GOGC from environment", "gogc", gogcEnv)
		return ApplyGCConfig(cfg)
	}

	// Check if GOMEMLIMIT is set
	if memLimitEnv := os.Getenv("GOMEMLIMIT"); memLimitEnv != "" {
		log.Info("GC: Using GOMEMLIMIT from environment",
			"gomemlimit", memLimitEnv,
			"gogc", cfg.GCPercent)
		// GOMEMLIMIT is already applied by runtime, just set GOGC
		old := debug.SetGCPercent(cfg.GCPercent)
		log.Info("GC configuration applied", "gogc", cfg.GCPercent, "previous_gogc", old)
		return nil
	}

	// Apply defaults
	log.Info("GC: Applying default aggressive configuration", "gogc", cfg.GCPercent)
	return ApplyGCConfig(cfg)
}
