package fibre

import (
	"context"
	"log/slog"
	"time"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	core "github.com/cometbft/cometbft/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// ServerConfig contains configuration options for the Fibre [Server].
type ServerConfig struct {
	// ChainID is the chain identifier for domain separation in [PaymentPromise] validation.
	ChainID string

	CodingConfig

	// MaxClockDrift is the maximum allowed time difference between the server's clock and the payment promise timestamp.
	// Promises with timestamps older than (now - MaxClockDrift) will be rejected.
	MaxClockDrift time.Duration
	// DataRetentionDuration defines how long uploaded blob data is retained before garbage collection.
	// Data older than (now - DataRetentionDuration) will be deleted by the GC.
	DataRetentionDuration time.Duration
	// PaymentPromiseTimeout is how long to wait before checking if a payment promise was fulfilled.
	// Unfulfilled promises older than (now - PaymentPromiseTimeout) will be deleted by the GC.
	PaymentPromiseTimeout time.Duration

	// Log is the logger for the server.
	// If nil, slog.Default() will be used.
	Log *slog.Logger
	// Tracer is the OpenTelemetry tracer for distributed tracing.
	// If nil, otel.Tracer("fibre-server") will be used.
	Tracer trace.Tracer
}

// DefaultServerConfig returns a [ServerConfig] with default values.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		ChainID:               "celestia",
		CodingConfig:          DefaultCodingConfig(),
		MaxClockDrift:         10 * time.Second,
		DataRetentionDuration: 24 * time.Hour,
		PaymentPromiseTimeout: 1 * time.Hour,
	}
}

// Server implements the Fibre gRPC service for validators.
// It handles upload and download requests from clients.
type Server struct {
	cfg ServerConfig

	privVal core.PrivValidator
	valGet  validator.SetGetter
	store   *Store

	log    *slog.Logger
	tracer trace.Tracer

	gcCancel context.CancelFunc
}

// NewServer creates a new Fibre [Server] with the provided dependencies.
func NewServer(privVal core.PrivValidator, valGet validator.SetGetter, store *Store, cfg ServerConfig) *Server {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Tracer == nil {
		cfg.Tracer = otel.Tracer("fibre-server")
	}

	return &Server{
		cfg:     cfg,
		privVal: privVal,
		valGet:  valGet,
		store:   store,
		log:     cfg.Log,
		tracer:  cfg.Tracer,
	}
}


// Stop gracefully stops the server by:
// 1. Canceling the garbage collection goroutine
// 2. Closing the underlying store
//
// This method should be called when shutting down the server.
func (s *Server) Stop() error {
	if s.gcCancel != nil {
		s.gcCancel()
	}
	return s.store.Close()
}
