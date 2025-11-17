package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"

	quic "github.com/quic-go/quic-go"
)

// quicTransport wraps a quic.Conn to implement the Transport interface.
type quicTransport struct {
	conn *quic.Conn
}

func (q *quicTransport) OpenStream(ctx context.Context) (net.Conn, error) {
	stream, err := q.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return &quicStreamConn{stream: stream, conn: q.conn}, nil
}

func (q *quicTransport) AcceptStream() (net.Conn, error) {
	stream, err := q.conn.AcceptStream(context.Background())
	if err != nil {
		return nil, err
	}
	return &quicStreamConn{stream: stream, conn: q.conn}, nil
}

func (q *quicTransport) RemoteAddr() net.Addr {
	return q.conn.RemoteAddr()
}

func (q *quicTransport) Close() error {
	return q.conn.CloseWithError(0, "closing transport")
}

// quicStreamConn adapts a *quic.Stream to net.Conn interface.
type quicStreamConn struct {
	stream *quic.Stream
	conn   *quic.Conn
}

func (q *quicStreamConn) Read(b []byte) (n int, err error) {
	return q.stream.Read(b)
}

func (q *quicStreamConn) Write(b []byte) (n int, err error) {
	return q.stream.Write(b)
}

func (q *quicStreamConn) Close() error {
	return q.stream.Close()
}

func (q *quicStreamConn) LocalAddr() net.Addr {
	return q.conn.LocalAddr()
}

func (q *quicStreamConn) RemoteAddr() net.Addr {
	return q.conn.RemoteAddr()
}

func (q *quicStreamConn) SetDeadline(t time.Time) error {
	return q.stream.SetDeadline(t)
}

func (q *quicStreamConn) SetReadDeadline(t time.Time) error {
	return q.stream.SetReadDeadline(t)
}

func (q *quicStreamConn) SetWriteDeadline(t time.Time) error {
	return q.stream.SetWriteDeadline(t)
}

// quicDialer implements TransportDialer for QUIC.
type quicDialer struct {
	tlsConfig *tls.Config
}

// NewQUICDialer creates a new QUIC TransportDialer with insecure settings for testing.
func NewQUICDialer() TransportDialer {
	return &quicDialer{
		tlsConfig: &tls.Config{
			InsecureSkipVerify: true, // For testing without proper certificates
			NextProtos:         []string{"drpc-over-quic"},
		},
	}
}

func (d *quicDialer) Dial(ctx context.Context, addr string) (Transport, error) {
	// Create UDP connection with large buffers
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve UDP address %s: %w", addr, err)
	}

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("failed to create UDP connection: %w", err)
	}

	// Set large UDP buffer sizes (10 MiB) to avoid packet drops
	if err := udpConn.SetReadBuffer(10 * 1024 * 1024); err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to set read buffer: %w", err)
	}
	if err := udpConn.SetWriteBuffer(10 * 1024 * 1024); err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to set write buffer: %w", err)
	}

	// Dial using the prepared connection
	conn, err := quic.Dial(ctx, udpConn, udpAddr, d.tlsConfig, highThroughputQUICConfig())
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to dial QUIC host %s: %w", addr, err)
	}

	return &quicTransport{conn: conn}, nil
}

// quicListener implements TransportListener for QUIC.
type quicListener struct {
	listener *quic.Listener
}

// NewQUICListener creates a new QUIC TransportListener with auto-generated self-signed certificates.
func NewQUICListener(addr string) (TransportListener, error) {
	// Generate self-signed certificate for testing
	tlsConfig, err := generateSelfSignedTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to generate TLS config: %w", err)
	}

	// Create UDP listener with large buffers
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve UDP address %s: %w", addr, err)
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create UDP listener: %w", err)
	}

	// Set large UDP buffer sizes (10 MiB) to avoid packet drops
	if err := udpConn.SetReadBuffer(10 * 1024 * 1024); err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to set read buffer: %w", err)
	}
	if err := udpConn.SetWriteBuffer(10 * 1024 * 1024); err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to set write buffer: %w", err)
	}

	// Create QUIC listener using the prepared connection
	listener, err := quic.Listen(udpConn, tlsConfig, highThroughputQUICConfig())
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to create QUIC listener on %s: %w", addr, err)
	}

	return &quicListener{listener: listener}, nil
}

func (l *quicListener) Accept() (Transport, error) {
	conn, err := l.listener.Accept(context.Background())
	if err != nil {
		return nil, err
	}

	return &quicTransport{conn: conn}, nil
}

func (l *quicListener) Addr() net.Addr {
	return l.listener.Addr()
}

func (l *quicListener) Close() error {
	return l.listener.Close()
}

// highThroughputQUICConfig returns a QUIC config optimized for high throughput.
// Tuned to match yamux performance for 10Gbps networks with large flow control windows.
func highThroughputQUICConfig() *quic.Config {
	return &quic.Config{
		// Connection lifetime
		MaxIdleTimeout:  5 * time.Minute,  // Keep connections alive longer (reuse is good)
		KeepAlivePeriod: 30 * time.Second, // Less frequent keepalives

		// Stream concurrency
		MaxIncomingStreams:    256, // Allow many concurrent streams (2x yamux)
		MaxIncomingUniStreams: 0,   // We don't use unidirectional streams

		// === CRITICAL FOR PERFORMANCE: Flow Control Windows ===
		// Per-stream limits (how much data one RPC can buffer)
		InitialStreamReceiveWindow: 16 * 1024 * 1024, // 16 MiB per stream (matches yamux InitialStreamWindowSize)
		MaxStreamReceiveWindow:     64 * 1024 * 1024, // 64 MiB per stream (matches yamux MaxStreamWindowSize)

		// Connection-wide limits (total across ALL streams)
		InitialConnectionReceiveWindow: 128 * 1024 * 1024, // 128 MiB total
		MaxConnectionReceiveWindow:     512 * 1024 * 1024, // 512 MiB total

		// Disable features we don't use
		EnableDatagrams:         false, // Not using datagrams
		DisablePathMTUDiscovery: false, // Enable PMTU discovery for efficiency
	}
}

// generateSelfSignedTLSConfig generates a self-signed TLS certificate for local testing.
// Uses ECDSA P-256 for faster handshakes than RSA.
// This is NOT secure for production use - it's only for development/testing without proper certificates.
func generateSelfSignedTLSConfig() (*tls.Config, error) {
	// Generate ECDSA private key (much faster than RSA for TLS handshake)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour) // Valid for 1 year

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Celestia DRPC Testing"},
			CommonName:   "localhost",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Create TLS certificate
	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}

	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"drpc-over-quic"},
	}, nil
}

// Ensure quicStreamConn implements net.Conn
var _ net.Conn = (*quicStreamConn)(nil)

// Ensure quic types implement Transport interfaces
var _ Transport = (*quicTransport)(nil)
var _ TransportDialer = (*quicDialer)(nil)
var _ TransportListener = (*quicListener)(nil)

// Ensure io.EOF can be returned
var _ = io.EOF
