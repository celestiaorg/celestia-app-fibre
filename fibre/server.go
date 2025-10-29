package fibre

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cometbft/cometbft/crypto"
	core "github.com/cometbft/cometbft/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// ServerConfig contains configuration options for the Fibre [Server].
type ServerConfig struct {
	// ChainID is the chain identifier for domain separation in [PaymentPromise] validation.
	ChainID string

	BlobConfig

	// MaxClockDrift is the maximum allowed time difference between the server's clock and the payment promise timestamp.
	// Promises with timestamps older than (now - MaxClockDrift) will be rejected.
	MaxClockDrift time.Duration
	// MaxHeightDrift is the maximum allowed height difference between the current chain height and the payment promise height.
	// Promises with heights less than (currentHeight - MaxHeightDrift) will be rejected.
	MaxHeightDrift int64
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
		BlobConfig:            DefaultBlobConfigV0(),
		MaxClockDrift:         10 * time.Second,
		MaxHeightDrift:        3,
		DataRetentionDuration: 24 * time.Hour,
		PaymentPromiseTimeout: 1 * time.Hour,
	}
}

// Server implements the Fibre gRPC service for validators.
// It handles upload and download requests from clients.
type Server struct {
	cfg ServerConfig

	privVal core.PrivValidator
	pubKey  crypto.PubKey // cached public key from privVal

	queryClient types.QueryClient
	valGet      validator.SetGetter
	store       *Store

	log    *slog.Logger
	tracer trace.Tracer

	gcCancel context.CancelFunc
}

// NewServer creates a new Fibre [Server] with the provided dependencies.
// Returns an error if the validator's public key cannot be retrieved.
func NewServer(
	privVal core.PrivValidator,
	queryClient types.QueryClient,
	valGet validator.SetGetter,
	store *Store,
	cfg ServerConfig,
) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default().WithGroup("fibre-server")
	}
	if cfg.Tracer == nil {
		cfg.Tracer = otel.Tracer("fibre-server")
	}

	// cache the validator's public key in case the implementation does IO internally
	pubKey, err := privVal.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("getting validator public key: %w", err)
	}

	return &Server{
		cfg:         cfg,
		privVal:     privVal,
		pubKey:      pubKey,
		queryClient: queryClient,
		valGet:      valGet,
		store:       store,
		log:         cfg.Log,
		tracer:      cfg.Tracer,
	}, nil
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
