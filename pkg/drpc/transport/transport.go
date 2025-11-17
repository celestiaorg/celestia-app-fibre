package transport

import (
	"context"
	"io"
	"net"
)

// Transport represents a multiplexed connection that can open and accept streams.
// This abstraction allows us to use different underlying transports (yamux, QUIC, etc.)
// while keeping the DRPC protocol logic unchanged.
type Transport interface {
	io.Closer

	// OpenStream opens a new bidirectional stream on the transport.
	// This is used by clients to initiate RPC calls.
	OpenStream(ctx context.Context) (net.Conn, error)

	// AcceptStream accepts the next incoming stream on the transport.
	// This is used by servers to handle incoming RPC calls.
	// Returns io.EOF when the transport is closed.
	AcceptStream() (net.Conn, error)

	// RemoteAddr returns the remote address of the transport connection.
	RemoteAddr() net.Addr
}

// TransportDialer creates client-side transports by dialing a remote address.
type TransportDialer interface {
	// Dial establishes a connection to the given address and returns a Transport.
	Dial(ctx context.Context, addr string) (Transport, error)
}

// TransportListener listens for incoming transport connections.
type TransportListener interface {
	io.Closer

	// Accept waits for and returns the next Transport connection.
	Accept() (Transport, error)

	// Addr returns the listener's network address.
	Addr() net.Addr
}

// TransportType represents the type of multiplexing transport to use.
type TransportType string

const (
	TransportYamux TransportType = "yamux"
	TransportQUIC  TransportType = "quic"
)

// NewDialer creates a TransportDialer for the specified transport type.
func NewDialer(transportType TransportType) (TransportDialer, error) {
	switch transportType {
	case TransportYamux:
		return NewYamuxDialer(), nil
	case TransportQUIC:
		return NewQUICDialer(), nil
	default:
		return nil, ErrUnsupportedTransport
	}
}

// NewListener creates a TransportListener for the specified transport type.
func NewListener(transportType TransportType, addr string) (TransportListener, error) {
	switch transportType {
	case TransportYamux:
		return NewYamuxListener(addr)
	case TransportQUIC:
		return NewQUICListener(addr)
	default:
		return nil, ErrUnsupportedTransport
	}
}
