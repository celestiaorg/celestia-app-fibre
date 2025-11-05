package fibre

import (
	"fmt"
	"log/slog"
	"time"

	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cometbft/cometbft/crypto"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// ServerConfig contains configuration options for the Fibre [Server].
type ServerConfig struct {
	// ChainID is the chain identifier for domain separation in [PaymentPromise] validation.
	ChainID string
	// BlockTime is the expected block time for calculating height-based timeouts.
	BlockTime time.Duration

	BlobConfig
	StoreConfig

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
		ChainID:     "celestia",
		BlockTime:   time.Second * 6,
		BlobConfig:  DefaultBlobConfigV0(),
		StoreConfig: DefaultStoreConfig(),
	}
}

// Server implements the Fibre gRPC service for validators.
// It handles upload and download requests from clients.
type Server struct {
	types.UnimplementedFibreServer

	cfg ServerConfig

	privVal core.PrivValidator
	pubKey  crypto.PubKey // cached public key from privVal

	queryClient types.QueryClient
	valGet      validator.SetGetter
	store       *Store

	log    *slog.Logger
	tracer trace.Tracer
}

// NewServer creates a new Fibre [Server] with the provided gRPC infrastructure.
// Returns an error if the validator's public key cannot be retrieved.
func NewServer(
	privVal core.PrivValidator,
	cfg ServerConfig,
	grpcServer *grpc.Server,
	grpcClient *grpc.ClientConn,
) (*Server, error) {
	if grpcServer == nil {
		return nil, fmt.Errorf("grpcServer is required")
	}
	if grpcClient == nil {
		return nil, fmt.Errorf("grpcClient is required")
	}

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

	queryClient := types.NewQueryClient(grpcClient)
	blockAPIClient := coregrpc.NewBlockAPIClient(grpcClient)
	valGet := fibregrpc.NewSetGetter(blockAPIClient)

	store, err := NewBadgerStore(cfg.StoreConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre store: %w", err)
	}

	server := &Server{
		cfg:         cfg,
		privVal:     privVal,
		pubKey:      pubKey,
		queryClient: queryClient,
		valGet:      valGet,
		store:       store,
		log:         cfg.Log,
		tracer:      cfg.Tracer,
	}

	// Register Fibre server with gRPC server
	types.RegisterFibreServer(grpcServer, server)

	return server, nil
}

func (s *Server) Config() ServerConfig {
	return s.cfg
}

// Stop stops the server.
// NOTE: It is not a graceful shutdown as it doesn't await for pending requests to complete.
func (s *Server) Stop() error {
	return s.store.Close()
}
