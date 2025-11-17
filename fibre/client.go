package fibre

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	fibredrpc "github.com/celestiaorg/celestia-app/v6/fibre/drpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/pkg/drpc/transport"
	"github.com/celestiaorg/celestia-app/v6/pkg/user"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clock "github.com/filecoin-project/go-clock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	grpctypes "google.golang.org/grpc"
)

const (
	// DefaultKeyName is the default key name for the client.
	// Exposed for testing purposes.
	DefaultKeyName = "default-fibre"

	pyroscopeShutdownTimeout = 5 * time.Second
)

var (
	// ErrClientClosed is returned when an operation is attempted on a closed client.
	ErrClientClosed = errors.New("fibre client is closed")
	// ErrKeyNotFound is returned when the configured key is not found in the keyring.
	ErrKeyNotFound = errors.New("key not found in keyring")
)

// txClient captures the subset of transaction client functionality required by [Client].
type txClient interface {
	DefaultAddress() sdk.AccAddress
	BroadcastTx(ctx context.Context, msgs []sdk.Msg, opts ...user.TxOption) (*sdk.TxResponse, error)
	ConfirmTx(ctx context.Context, txHash string) (*user.TxResponse, error)
	GRPCConn() grpctypes.ClientConnInterface
}

// fibreQueryClient defines the fibre-specific queries the client relies on.
type fibreQueryClient interface {
	Params(ctx context.Context, in *types.QueryParamsRequest, opts ...grpctypes.CallOption) (*types.QueryParamsResponse, error)
	EscrowAccount(ctx context.Context, in *types.QueryEscrowAccountRequest, opts ...grpctypes.CallOption) (*types.QueryEscrowAccountResponse, error)
}

var _ txClient = (*user.TxClient)(nil)

// ClientConfig contains configuration options for the Fibre [Client].
type ClientConfig struct {
	// DefaultKeyName is the name of the key in the keyring to use for signing [PaymentPromise]s.
	DefaultKeyName string
	// ChainID is the chain identifier for domain separation in [PaymentPromise] signatures.
	ChainID string

	// BlobConfig contains erasure coding and data handling configuration.
	BlobConfig

	// UploadTargetVotingPower is the fraction (e.g., 2/3) of total voting power required for Upload operations.
	UploadTargetVotingPower cmtmath.Fraction
	// UploadTargetSignaturesCount is the fraction (e.g., 2/3) of total signature count required for Upload operations.
	UploadTargetSignaturesCount cmtmath.Fraction
	// UploadConcurrency is the maximum number of concurrent uploads to validators.
	UploadConcurrency int
	// DownloadConcurrency is the maximum number of concurrent read requests to validators.
	DownloadConcurrency int

	// NewClientFn is the constructor function for creating Fibre clients.
	// If nil, [NewDRPCClientFn] with [fibredrpc.DefaultNewClientFn] will be used (DRPC transport).
	// Use [NewGRPCClientFn] or [NewDRPCClientFn] to wrap transport-specific constructors.
	NewClientFn TransportClientFn
	// MultiplexTransport specifies the multiplexing transport to use for DRPC connections.
	// Valid values: "yamux" (default) or "quic". Only applies when using DRPC transport.
	MultiplexTransport string
	// Log is the logger for the client.
	// If nil, [slog.Default] will be used.
	Log *slog.Logger
	// Tracer is the OpenTelemetry tracer for distributed tracing.
	// If nil, [trace.Default] will be used.
	Tracer trace.Tracer
	// Pyroscope enables continuous profiling when configured.
	Pyroscope *PyroscopeConfig
	// Clock is the clock for time-related operations.
	// If nil, [clock.New] will be used.
	Clock clock.Clock
	// AutoFundEscrow controls whether [Client.Put] automatically ensures the escrow account exists
	// and has sufficient balance prior to submitting a payment.
	AutoFundEscrow bool
}

// DefaultClientConfig returns a [ClientConfig] with the default values.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		DefaultKeyName:              DefaultKeyName,
		ChainID:                     "celestia",
		BlobConfig:                  DefaultBlobConfigV0(),
		UploadTargetVotingPower:     cmtmath.Fraction{Numerator: 2, Denominator: 3},
		UploadTargetSignaturesCount: cmtmath.Fraction{Numerator: 2, Denominator: 3},
		UploadConcurrency:           100, // matches expected number of validators to maximize throughput by default
		DownloadConcurrency:         25,  // 1/4 of validators to match 1/3 erasure coding overhead and request the minimum number of samples to get the data
		AutoFundEscrow:              true,
		MultiplexTransport:          "yamux", // Default to yamux for backward compatibility
	}
}

// Client is the Fibre DA client.
type Client struct {
	cfg ClientConfig

	txClient    txClient
	keyring     keyring.Keyring
	valGet      validator.SetGetter
	hostReg     validator.HostRegistry
	queryClient fibreQueryClient

	log            *slog.Logger
	tracer         trace.Tracer
	tracerShutdown func(context.Context) error
	clock          clock.Clock

	clientCache *TransportClientCache
	uploadSem   chan struct{}
	downloadSem chan struct{}

	pyroscope *pyroscopeHandle

	// closeWg tracks subroutines spawned by Upload/Download operations.
	// Close() waits for this WaitGroup to ensure all operations complete before releasing resources.
	// Upload/Download operations don't wait for their spawned goroutines, allowing them to return early for low latency.
	closeWg sync.WaitGroup
	// closed indicates whether Close() has been called.
	closed atomic.Bool
}

// NewClient creates a new [Client] with the provided dependencies.
// Returns an error if the configured key is not found in the keyring.
func NewClient(txClient *user.TxClient, kr keyring.Keyring, valGet validator.SetGetter, hostReg validator.HostRegistry, cfg ClientConfig) (*Client, error) {
	// Verify the key exists in the keyring
	_, err := kr.Key(cfg.DefaultKeyName)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrKeyNotFound, cfg.DefaultKeyName, err)
	}

	if cfg.NewClientFn == nil {
		// Determine transport type from config
		transportType := cfg.MultiplexTransport
		if transportType == "" {
			transportType = "yamux" // Default to yamux
		}
		cfg.NewClientFn = NewDRPCClientFn(fibredrpc.DefaultNewClientFn(hostReg, transport.TransportType(transportType)))
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default().WithGroup("fibre-client")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.New()
	}
	var (
		pyroHandle *pyroscopeHandle
	)

	if cfg.Pyroscope != nil && cfg.Pyroscope.enabled() {
		pyroCfg := cfg.Pyroscope.clone()
		if pyroCfg.Labels == nil {
			pyroCfg.Labels = make(map[string]string, 2)
		}
		pyroCfg.Labels["component"] = "fibre-client"
		if cfg.ChainID != "" {
			pyroCfg.Labels["chain_id"] = cfg.ChainID
		}
		handle, appliedCfg, err := newPyroscopeHandle(pyroCfg)
		if err != nil {
			return nil, fmt.Errorf("configuring pyroscope: %w", err)
		}
		pyroHandle = handle
		cfg.Log.Info("pyroscope profiling enabled",
			"pyroscope_url", appliedCfg.ServerAddress,
			"application", appliedCfg.ApplicationName,
			"enable_tracing", appliedCfg.EnableTracing,
			"profile_types", appliedCfg.ProfileTypes,
		)
	}
	var tracerShutdown func(context.Context) error
	if cfg.Tracer == nil {
		var err error
		cfg.Tracer, tracerShutdown, err = newClientTracer(context.Background(), cfg.Log, cfg.ChainID)
		if err != nil {
			return nil, fmt.Errorf("configuring fibre client tracer: %w", err)
		}
		if cfg.Tracer == nil {
			cfg.Tracer = otel.Tracer("fibre-client")
		}
	}

	var queryClient fibreQueryClient
	if txClient != nil {
		if conn := txClient.GRPCConn(); conn != nil {
			queryClient = types.NewQueryClient(conn)
		}
	}

	return &Client{
		cfg:            cfg,
		txClient:       txClient,
		keyring:        kr,
		valGet:         valGet,
		hostReg:        hostReg,
		queryClient:    queryClient,
		log:            cfg.Log,
		tracer:         cfg.Tracer,
		tracerShutdown: tracerShutdown,
		clock:          cfg.Clock,
		clientCache:    NewTransportClientCache(cfg.NewClientFn, cfg.UploadConcurrency),
		uploadSem:      make(chan struct{}, cfg.UploadConcurrency),
		downloadSem:    make(chan struct{}, cfg.DownloadConcurrency),
		pyroscope:      pyroHandle,
	}, nil
}

// Config returns the [ClientConfig] used by this client.
func (c *Client) Config() ClientConfig {
	return c.cfg
}

// Close closes the client and releases any associated resources.
// It waits for all ongoing [Client.Upload]/[Client.Download] operations to complete before closing.
// After Close is called, subsequent [Client.Upload/Client.Download] calls will return an error.
// Close is idempotent and safe to call multiple times.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.closeWg.Wait()
	var errs error
	if err := c.clientCache.Close(); err != nil {
		errs = errors.Join(errs, err)
	}

	if c.tracerShutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.tracerShutdown(ctx); err != nil {
			errs = errors.Join(errs, fmt.Errorf("shutting down tracer: %w", err))
			c.log.Error("failed to flush fibre client tracer", "error", err)
		}
	}

	if c.pyroscope != nil {
		ctx, cancel := context.WithTimeout(context.Background(), pyroscopeShutdownTimeout)
		defer cancel()
		if err := c.pyroscope.Close(ctx); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	return errs
}
