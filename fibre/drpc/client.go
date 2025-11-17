package drpc

import (
	"context"
	"fmt"
	"io"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/pkg/drpc/transport"
	core "github.com/cometbft/cometbft/types"
	"storj.io/drpc"
	"storj.io/drpc/drpcconn"
	"storj.io/drpc/drpcmanager"
	"storj.io/drpc/drpcstream"
	"storj.io/drpc/drpcwire"
)

// Client manages a multiplexed transport (yamux or QUIC) and provides a DoDrpc method
// for executing DRPC calls over on-demand streams.
type Client interface {
	io.Closer
	// DoDrpc opens a new stream on the transport, creates a DRPC connection on it,
	// executes the provided function, and closes the stream.
	DoDrpc(ctx context.Context, do func(conn drpc.Conn) error) error
}

// NewClientFn is a constructor function that creates a [Client]
// for a given validator. It should handle host resolution and connection establishment.
type NewClientFn func(ctx context.Context, val *core.Validator) (Client, error)

// fibreClientCloser implements [Client] with transport-based connection multiplexing.
// It holds the transport session (yamux, QUIC, etc.) for proper cleanup.
// Each RPC call opens a new stream on demand.
type fibreClientCloser struct {
	transport transport.Transport
}

func (f *fibreClientCloser) Close() error {
	if f.transport != nil {
		return f.transport.Close()
	}
	return nil
}

// DoDrpc opens a new stream on the transport, creates a DRPC connection on it,
// executes the provided function, and closes the stream.
func (f *fibreClientCloser) DoDrpc(ctx context.Context, do func(conn drpc.Conn) error) error {
	// Open a new stream for this RPC
	stream, err := f.transport.OpenStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	// Wrap the stream in a DRPC connection with large message size limits (256 MB)
	const maxMessageSize = 256 * 1024 * 1024 // 256 MB
	conn := drpcconn.NewWithOptions(stream, drpcconn.Options{
		Manager: drpcmanager.Options{
			Reader: drpcwire.ReaderOptions{MaximumBufferSize: maxMessageSize},
			Stream: drpcstream.Options{MaximumBufferSize: maxMessageSize},
		},
	})
	defer conn.Close()

	// Execute the provided function with the DRPC connection
	return do(conn)
}

// DefaultNewClientFn returns the default [NewClientFn] that uses the provided
// [validator.HostRegistry] to resolve validator hosts and establishes transport sessions.
// Each RPC will open a new stream on demand.
// The transportType parameter determines which transport to use (yamux or quic).
func DefaultNewClientFn(hostReg validator.HostRegistry, transportType transport.TransportType) NewClientFn {
	return func(ctx context.Context, val *core.Validator) (Client, error) {
		host, err := hostReg.GetHost(ctx, val)
		if err != nil {
			return nil, err
		}

		// Create the appropriate transport dialer
		dialer, err := transport.NewDialer(transportType)
		if err != nil {
			return nil, fmt.Errorf("failed to create transport dialer: %w", err)
		}

		// Dial the host using the transport
		tr, err := dialer.Dial(ctx, host.String())
		if err != nil {
			return nil, fmt.Errorf("failed to dial host %s: %w", host.String(), err)
		}

		return &fibreClientCloser{
			transport: tr,
		}, nil
	}
}
