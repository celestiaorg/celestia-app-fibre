package grpc

import (
	"context"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	core "github.com/cometbft/cometbft/types"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	perFlowGbps        = 4.0                          // tune this
	soMaxPacingRateOpt = 46                           // SO_MAX_PACING_RATE
	bytesPerSecond     = int(perFlowGbps * 1e9 / 8.0) // Gbit/s -> bytes/s
	sockBufBytes       = 4 << 20                      // 4 MiB
)

func pacedDialer(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			c.Control(func(fd uintptr) {
				// pacing
				if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soMaxPacingRateOpt, bytesPerSecond); e != nil && err == nil {
					err = e
				}
				// caps for autotuning
				if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, sockBufBytes); e != nil && err == nil {
					err = e
				}
				if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, sockBufBytes); e != nil && err == nil {
					err = e
				}
			})
			return err
		},
	}
	return d.DialContext(ctx, "tcp", addr)
}

// Client combines [FibreClient] with [io.Closer] to manage the lifecycle
// of both the client and its underlying connection.
type Client interface {
	types.FibreClient
	io.Closer
}

// NewClientFn is a constructor function that creates a [Client]
// for a given validator. It should handle host resolution and connection establishment.
type NewClientFn func(ctx context.Context, val *core.Validator) (Client, error)

// fibreClientCloser wraps a [FibreClient] and [grpclib.ClientConn] to implement [Client].
type fibreClientCloser struct {
	types.FibreClient
	conn *grpclib.ClientConn
}

func (f *fibreClientCloser) Close() error {
	return f.conn.Close()
}

// DefaultNewClientFn returns the default [NewClientFn] that uses the provided
// [validator.HostRegistry] to resolve validator hosts and establishes insecure gRPC connections
// with OpenTelemetry instrumentation for distributed tracing.
// The maxMsgSize parameter sets the maximum gRPC message size for send and receive operations.
func DefaultNewClientFn(hostReg validator.HostRegistry, maxMsgSize int) NewClientFn {
	return func(ctx context.Context, val *core.Validator) (Client, error) {
		host, err := hostReg.GetHost(ctx, val)
		if err != nil {
			return nil, err
		}

		// TODO(@Wondertan): setup secure connection
		conn, err := grpclib.NewClient(host.String(),
			grpclib.WithTransportCredentials(insecure.NewCredentials()),
			grpclib.WithContextDialer(pacedDialer),
			grpclib.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpclib.WithDefaultCallOptions(
				grpclib.MaxCallRecvMsgSize(maxMsgSize),
				grpclib.MaxCallSendMsgSize(maxMsgSize),
			),
			grpclib.WithWriteBufferSize(2*1024*1024),
			grpclib.WithInitialConnWindowSize(1024*1024*1024),
			grpclib.WithInitialWindowSize(64*1024*1024),
		)
		if err != nil {
			return nil, err
		}

		return &fibreClientCloser{
			FibreClient: types.NewFibreClient(conn),
			conn:        conn,
		}, nil
	}
}
