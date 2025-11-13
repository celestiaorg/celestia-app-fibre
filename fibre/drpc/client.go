package drpc

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	core "github.com/cometbft/cometbft/types"
	"storj.io/drpc/drpcconn"
	"storj.io/drpc/drpcmanager"
	"storj.io/drpc/drpcstream"
	"storj.io/drpc/drpcwire"
)

// Client combines [DRPCFibreClient] with [io.Closer] to manage the lifecycle
// of both the client and its underlying connection.
type Client interface {
	types.DRPCFibreClient
	io.Closer
}

// NewClientFn is a constructor function that creates a [Client]
// for a given validator. It should handle host resolution and connection establishment.
type NewClientFn func(ctx context.Context, val *core.Validator) (Client, error)

// fibreClientCloser wraps a [DRPCFibreClient] and [drpcconn.Conn] to implement [Client].
type fibreClientCloser struct {
	types.DRPCFibreClient
	conn *drpcconn.Conn
}

func (f *fibreClientCloser) Close() error {
	return f.conn.Close()
}

// DefaultNewClientFn returns the default [NewClientFn] that uses the provided
// [validator.HostRegistry] to resolve validator hosts and establishes DRPC connections.
func DefaultNewClientFn(hostReg validator.HostRegistry) NewClientFn {
	return func(ctx context.Context, val *core.Validator) (Client, error) {
		host, err := hostReg.GetHost(ctx, val)
		if err != nil {
			return nil, err
		}

		// DRPC uses raw TCP connections
		rawConn, err := net.Dial("tcp", host.String())
		if err != nil {
			return nil, fmt.Errorf("failed to dial DRPC host %s: %w", host.String(), err)
		}

		// Wrap the raw connection in a DRPC connection with large message size limits (256 MB)
		const maxMessageSize = 256 * 1024 * 1024 // 256 MB
		conn := drpcconn.NewWithOptions(rawConn, drpcconn.Options{
			Manager: drpcmanager.Options{
				Reader: drpcwire.ReaderOptions{MaximumBufferSize: maxMessageSize},
				Stream: drpcstream.Options{MaximumBufferSize: maxMessageSize},
			},
		})

		return &fibreClientCloser{
			DRPCFibreClient: types.NewDRPCFibreClient(conn),
			conn:            conn,
		}, nil
	}
}
