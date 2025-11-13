package drpc

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	core "github.com/cometbft/cometbft/types"
	"github.com/hashicorp/yamux"
	"storj.io/drpc"
	"storj.io/drpc/drpcconn"
	"storj.io/drpc/drpcmanager"
	"storj.io/drpc/drpcstream"
	"storj.io/drpc/drpcwire"
)

// Client manages a yamux session and provides a DoDrpc method for executing
// DRPC calls over on-demand yamux streams.
type Client interface {
	io.Closer
	// DoDrpc opens a new yamux stream, creates a DRPC connection on it,
	// executes the provided function, and closes the stream.
	DoDrpc(ctx context.Context, do func(conn drpc.Conn) error) error
}

// NewClientFn is a constructor function that creates a [Client]
// for a given validator. It should handle host resolution and connection establishment.
type NewClientFn func(ctx context.Context, val *core.Validator) (Client, error)

// fibreClientCloser implements [Client] with yamux-based connection multiplexing.
// It holds the yamux session and underlying TCP connection for proper cleanup.
// Each RPC call opens a new yamux stream on demand.
type fibreClientCloser struct {
	yamuxSess  *yamux.Session
	underlying net.Conn
}

func (f *fibreClientCloser) Close() error {
	var err error
	if f.yamuxSess != nil {
		err = f.yamuxSess.Close()
	}
	if f.underlying != nil {
		if connErr := f.underlying.Close(); connErr != nil && err == nil {
			err = connErr
		}
	}
	return err
}

// DoDrpc opens a new yamux stream, creates a DRPC connection on it,
// executes the provided function, and closes the stream.
func (f *fibreClientCloser) DoDrpc(ctx context.Context, do func(conn drpc.Conn) error) error {
	// Open a new yamux stream for this RPC
	stream, err := f.yamuxSess.Open()
	if err != nil {
		return fmt.Errorf("failed to open yamux stream: %w", err)
	}
	defer stream.Close()

	// Wrap the yamux stream in a DRPC connection with large message size limits (256 MB)
	const maxMessageSize = 256 * 1024 * 1024 // 256 MB
	conn := drpcconn.NewWithOptions(stream, drpcconn.Options{
		Manager: drpcmanager.Options{
			SoftCancel: true,
			Reader:     drpcwire.ReaderOptions{MaximumBufferSize: maxMessageSize},
			Stream:     drpcstream.Options{MaximumBufferSize: maxMessageSize},
		},
	})
	defer conn.Close()

	// Execute the provided function with the DRPC connection
	return do(conn)
}

// DefaultNewClientFn returns the default [NewClientFn] that uses the provided
// [validator.HostRegistry] to resolve validator hosts and establishes yamux sessions
// over TCP connections. Each RPC will open a new yamux stream on demand.
func DefaultNewClientFn(hostReg validator.HostRegistry) NewClientFn {
	return func(ctx context.Context, val *core.Validator) (Client, error) {
		host, err := hostReg.GetHost(ctx, val)
		if err != nil {
			return nil, err
		}

		// Establish underlying TCP connection
		tcpConn, err := net.Dial("tcp", host.String())
		if err != nil {
			return nil, fmt.Errorf("failed to dial TCP host %s: %w", host.String(), err)
		}

		// Create yamux client session on top of TCP connection
		// Yamux streams will be opened on demand for each RPC call
		yamuxSess, err := yamux.Client(tcpConn, nil)
		if err != nil {
			tcpConn.Close()
			return nil, fmt.Errorf("failed to create yamux session: %w", err)
		}

		return &fibreClientCloser{
			yamuxSess:  yamuxSess,
			underlying: tcpConn,
		}, nil
	}
}
