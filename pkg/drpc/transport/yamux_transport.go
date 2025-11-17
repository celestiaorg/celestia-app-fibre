package transport

import (
	"context"
	"fmt"
	"net"

	drpcpkg "github.com/celestiaorg/celestia-app/v6/pkg/drpc"
	"github.com/libp2p/go-yamux/v5"
)

// yamuxTransport wraps a yamux.Session to implement the Transport interface.
type yamuxTransport struct {
	session    *yamux.Session
	underlying net.Conn
}

func (y *yamuxTransport) OpenStream(ctx context.Context) (net.Conn, error) {
	return y.session.Open(ctx)
}

func (y *yamuxTransport) AcceptStream() (net.Conn, error) {
	return y.session.Accept()
}

func (y *yamuxTransport) RemoteAddr() net.Addr {
	return y.session.RemoteAddr()
}

func (y *yamuxTransport) Close() error {
	var err error
	if y.session != nil {
		err = y.session.Close()
	}
	if y.underlying != nil {
		if closeErr := y.underlying.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

// yamuxDialer implements TransportDialer for yamux.
type yamuxDialer struct{}

// NewYamuxDialer creates a new yamux TransportDialer.
func NewYamuxDialer() TransportDialer {
	return &yamuxDialer{}
}

func (d *yamuxDialer) Dial(ctx context.Context, addr string) (Transport, error) {
	// Establish underlying TCP connection
	var dialer net.Dialer
	tcpConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial TCP host %s: %w", addr, err)
	}

	// Create yamux client session on top of TCP connection
	yamuxSess, err := yamux.Client(tcpConn, drpcpkg.YamuxCfg, nil)
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("failed to create yamux session: %w", err)
	}

	return &yamuxTransport{
		session:    yamuxSess,
		underlying: tcpConn,
	}, nil
}

// yamuxListener implements TransportListener for yamux.
type yamuxListener struct {
	listener net.Listener
}

// NewYamuxListener creates a new yamux TransportListener.
func NewYamuxListener(addr string) (TransportListener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create TCP listener on %s: %w", addr, err)
	}

	return &yamuxListener{listener: listener}, nil
}

func (l *yamuxListener) Accept() (Transport, error) {
	// Accept TCP connection
	tcpConn, err := l.listener.Accept()
	if err != nil {
		return nil, err
	}

	// Create yamux server session on the TCP connection
	yamuxSess, err := yamux.Server(tcpConn, drpcpkg.YamuxCfg, nil)
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("failed to create yamux session: %w", err)
	}

	return &yamuxTransport{
		session:    yamuxSess,
		underlying: tcpConn,
	}, nil
}

func (l *yamuxListener) Addr() net.Addr {
	return l.listener.Addr()
}

func (l *yamuxListener) Close() error {
	return l.listener.Close()
}
