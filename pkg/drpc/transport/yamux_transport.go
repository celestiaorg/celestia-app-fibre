package transport

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"syscall"
	"time"

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

const (
	perFlowGbps        = 4.0                          // tune this
	soMaxPacingRateOpt = 46                           // SO_MAX_PACING_RATE
	bytesPerSecond     = int(perFlowGbps * 1e9 / 8.0) // Gbit/s -> bytes/s
	sockBufBytes       = 4 << 20                      // 4 MiB
)

func (d *yamuxDialer) Dial(ctx context.Context, addr string) (Transport, error) {
	// Establish underlying TCP connection
	dialer := net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			c.Control(func(fd uintptr) {
				// TCP_NODELAY - disable Nagle's algorithm for low latency (works on all platforms)
				if e := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1); e != nil && err == nil {
					err = e
				}

				// SO_REUSEADDR - allow quick socket reuse (works on all platforms)
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)

				// SO_MAX_PACING_RATE - Linux only, skip on other platforms
				if runtime.GOOS == "linux" {
					syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soMaxPacingRateOpt, bytesPerSecond)
				}

				// Buffer sizes - increase for better throughput (works on all platforms)
				// Don't fail on these, they're just hints to the kernel
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, sockBufBytes)
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, sockBufBytes)
			})
			return err
		},
	}
	res := strings.Split(addr, ":")
	tcpConn, err := dialer.DialContext(ctx, "tcp", "127.0.0.1"+":"+res[1])
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

	// Optimize accepted connection
	if tc, ok := tcpConn.(*net.TCPConn); ok {
		// Disable Nagle's algorithm for low latency
		tc.SetNoDelay(true)
		// Increase buffer sizes for better throughput
		tc.SetReadBuffer(sockBufBytes)
		tc.SetWriteBuffer(sockBufBytes)
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
