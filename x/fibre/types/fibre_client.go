package types

import (
	"context"
	"io"

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
