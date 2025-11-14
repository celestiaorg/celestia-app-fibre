package fibre

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/rsema1d"
	"github.com/cometbft/cometbft/crypto"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	"github.com/cosmos/gogoproto/grpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
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

// verifyRowsResult contains the result of row verification including the RLC root.
type verifyRowsResult struct {
	rlcRoot [32]byte
	err     error
}

// verifyRowsWork represents a complete row verification task submitted to the worker pool.
type verifyRowsWork struct {
	rows       *types.Rows
	promise    *PaymentPromise
	cfg        BlobConfig
	resultChan chan<- verifyRowsResult
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

	log            *slog.Logger
	tracer         trace.Tracer
	tracerShutdown func(context.Context) error

	// worker pool for row verification
	verifyWorkChan chan verifyRowsWork
	workerWg       sync.WaitGroup
	workerCtx      context.Context
	workerCancel   context.CancelFunc
}

// NewServer creates a new Fibre [Server] with the provided dependencies.
// Returns an error if the validator's public key cannot be retrieved.
func NewServer(
	privVal core.PrivValidator,
	queryClient types.QueryClient,
	valGet validator.SetGetter,
	cfg ServerConfig,
) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default().WithGroup("fibre-server")
	}

	var (
		tracer         trace.Tracer
		tracerShutdown func(context.Context) error
	)
	if cfg.Tracer != nil {
		tracer = cfg.Tracer
	} else {
		var err error
		tracer, tracerShutdown, err = newServerTracer(context.Background(), cfg.Log, cfg.ChainID)
		if err != nil {
			return nil, fmt.Errorf("configuring fibre tracer: %w", err)
		}
		if tracer == nil {
			tracer = otel.Tracer(tracerName)
		}
	}

	// cache the validator's public key in case the implementation does IO internally
	pubKey, err := privVal.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("getting validator public key: %w", err)
	}

	store, err := NewBadgerStore(cfg.StoreConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre store: %w", err)
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())

	server := &Server{
		cfg:            cfg,
		privVal:        privVal,
		pubKey:         pubKey,
		queryClient:    queryClient,
		valGet:         valGet,
		store:          store,
		log:            cfg.Log,
		tracer:         tracer,
		tracerShutdown: tracerShutdown,
		verifyWorkChan: make(chan verifyRowsWork, cfg.CodingWorkers), // buffer one work item per worker
		workerCtx:      workerCtx,
		workerCancel:   workerCancel,
	}

	// start worker pool
	server.startVerificationWorkers()

	return server, nil
}

// NewServerFromGRPC creates a new Fibre [Server] from a gRPC server and client.
// It registers the server with the gRPC server and returns the Fibre [Server].
func NewServerFromGRPC(
	privVal core.PrivValidator,
	grpcServer grpc.Server,
	grpcClient grpc.ClientConn,
	cfg ServerConfig,
) (*Server, error) {
	queryClient := types.NewQueryClient(grpcClient)
	valGet := fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(grpcClient))

	server, err := NewServer(privVal, queryClient, valGet, cfg)
	if err != nil {
		return nil, err
	}
	types.RegisterFibreServer(grpcServer, server)
	return server, nil
}

// NewInMemoryServer creates a new Fibre [Server] with an in-memory store backend.
func NewInMemoryServer(
	privVal core.PrivValidator,
	queryClient types.QueryClient,
	valGet validator.SetGetter,
	cfg ServerConfig,
) (*Server, error) {
	memStore := NewMemoryStore(cfg.StoreConfig)
	srv, err := NewServer(privVal, queryClient, valGet, cfg)
	if err != nil {
		return nil, err
	}
	srv.store = memStore
	return srv, err
}

func (s *Server) Config() ServerConfig {
	return s.cfg
}

// Store returns the server's store.
func (s *Server) Store() *Store {
	return s.store
}

// startVerificationWorkers starts the persistent worker pool for row verification.
func (s *Server) startVerificationWorkers() {
	for i := 0; i < s.cfg.CodingWorkers; i++ {
		s.workerWg.Add(1)
		go s.verificationWorker()
	}
}

// verificationWorker is the worker goroutine that processes row verification tasks.
func (s *Server) verificationWorker() {
	defer s.workerWg.Done()

	for {
		select {
		case <-s.workerCtx.Done():
			return
		case work := <-s.verifyWorkChan:
			result := s.executeRowVerification(work)
			work.resultChan <- result
		}
	}
}

// executeRowVerification performs the CPU-heavy verification work including context creation.
func (s *Server) executeRowVerification(work verifyRowsWork) verifyRowsResult {
	rowSize, err := parseRowSize(work.rows.Rows)
	if err != nil {
		return verifyRowsResult{err: err}
	}

	// validate upload size matches the row size
	expectedUploadSize := rowSize * work.cfg.OriginalRows
	if int(work.promise.UploadSize) != expectedUploadSize {
		return verifyRowsResult{err: fmt.Errorf("upload size mismatch: promise has %d, but row size %d * %d original rows = %d",
			work.promise.UploadSize, rowSize, work.cfg.OriginalRows, expectedUploadSize)}
	}

	rlcCoeffs, err := parseRLCCoeffs(work.rows.GetCoefficients(), work.cfg.OriginalRows)
	if err != nil {
		return verifyRowsResult{err: err}
	}

	// CPU-heavy operation: create verification context
	verificationCtx, rlcRoot, err := rsema1d.CreateVerificationContext(rlcCoeffs, &rsema1d.Config{
		K:           work.cfg.OriginalRows,
		N:           work.cfg.ParityRows,
		RowSize:     rowSize,
		WorkerCount: 1, // single-threaded within each worker
	})
	if err != nil {
		return verifyRowsResult{err: fmt.Errorf("creating verification context: %w", err)}
	}

	totalRows := work.cfg.OriginalRows + work.cfg.ParityRows

	// verify each row
	for _, rowPb := range work.rows.Rows {
		row, err := parseRow(rowPb, totalRows)
		if err != nil {
			return verifyRowsResult{err: err}
		}

		if err := rsema1d.VerifyRowWithContext(row, rsema1d.Commitment(work.promise.Commitment), verificationCtx); err != nil {
			return verifyRowsResult{err: fmt.Errorf("verification failed for row %d: %w", row.Index, err)}
		}

		runtime.Gosched()
	}

	return verifyRowsResult{rlcRoot: rlcRoot}
}

// Stop stops the server.
// NOTE: It is not a graceful shutdown as it doesn't await for pending requests to complete.
func (s *Server) Stop() error {
	var err error

	// signal workers to stop
	s.workerCancel()
	// close work channel to unblock any workers waiting on it
	close(s.verifyWorkChan)
	// wait for all workers to finish
	s.workerWg.Wait()

	if s.tracerShutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := s.tracerShutdown(ctx); shutdownErr != nil {
			err = errors.Join(err, fmt.Errorf("shutting down tracer: %w", shutdownErr))
			s.log.Error("failed to flush fibre tracer", "error", shutdownErr)
		}
	}
	if storeErr := s.store.Close(); storeErr != nil {
		err = errors.Join(err, storeErr)
	}
	return err
}
