package types

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/validator"
	core "github.com/cometbft/cometbft/types"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// FibreClientCloser combines [FibreClient] with [io.Closer] to manage the lifecycle
// of both the client and its underlying connection.
type FibreClientCloser interface {
	FibreClient
	io.Closer
}

// FibreClientCloserFn is a constructor function that creates a [FibreClientCloser]
// for a given validator. It should handle host resolution and connection establishment.
type FibreClientCloserFn func(ctx context.Context, val *core.Validator) (FibreClientCloser, error)

// fibreClientCloser wraps a [FibreClient] and [grpc.ClientConn] to implement [FibreClientCloser].
type fibreClientCloser struct {
	FibreClient
	conn *grpc.ClientConn
}

func (f *fibreClientCloser) Close() error {
	return f.conn.Close()
}

// DefaultFibreClientFn returns the default [FibreClientCloserFn] that uses the provided
// [validator.HostRegistry] to resolve validator hosts and establishes insecure gRPC connections
// with OpenTelemetry instrumentation for distributed tracing.
func DefaultFibreClientFn(hostReg validator.HostRegistry) FibreClientCloserFn {
	return func(ctx context.Context, val *core.Validator) (FibreClientCloser, error) {
		host, err := hostReg.GetHost(ctx, val)
		if err != nil {
			return nil, err
		}

		// TODO(@Wondertan): setup secure connection
		conn, err := grpc.NewClient(host.String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		)
		if err != nil {
			return nil, err
		}

		return &fibreClientCloser{
			FibreClient: NewFibreClient(conn),
			conn:        conn,
		}, nil
	}
}

// ClientCache caches [FibreClientCloser]s per validator using the provided constructor function.
// TODO(@Wondertan): Needs cleanup strategy, e.g. LRU
type ClientCache struct {
	newClient FibreClientCloserFn
	mu        sync.Mutex
	clients   map[string]*clientEntry // keyed by validator address string
}

// clientEntry holds a lazily-initialized [FibreClientCloser].
type clientEntry struct {
	sync.Once
	clientCloser FibreClientCloser
	err          error
}

// NewClientCache creates a new [ClientCache] with the given [FibreClientCloserFn].
func NewClientCache(newClient FibreClientCloserFn) *ClientCache {
	return &ClientCache{
		newClient: newClient,
		clients:   make(map[string]*clientEntry),
	}
}

// GetClient returns a cached [FibreClient] for the validator, creating one if needed.
// Uses the constructor function provided to [NewClientCache]. Only one dial per validator will occur.
func (cc *ClientCache) GetClient(ctx context.Context, val *core.Validator) (FibreClient, error) {
	addr := val.Address.String()

	cc.mu.Lock()
	entry, ok := cc.clients[addr]
	if !ok {
		entry = &clientEntry{}
		cc.clients[addr] = entry
	}
	cc.mu.Unlock()

	entry.Do(func() {
		client, err := cc.newClient(ctx, val)
		if err != nil {
			entry.err = err
			return
		}
		entry.clientCloser = client
	})

	return entry.clientCloser, entry.err
}

// Close closes all cached [FibreClientCloser]s.
func (cc *ClientCache) Close() (err error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	for _, entry := range cc.clients {
		if entry.clientCloser != nil {
			err = errors.Join(err, entry.clientCloser.Close())
		}
	}
	cc.clients = make(map[string]*clientEntry)
	return err
}
