package fibre

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	"google.golang.org/grpc"
)

const (
	perFlowGbpsServer   = 4.0                               // usually same as client
	soMaxPacingRateOptS = 46                                // SO_MAX_PACING_RATE
	bytesPerSecondSrv   = int(perFlowGbpsServer * 1e9 / 8.0) // Gbit/s -> bytes/s
	sockBufBytesSrv     = 4 << 20                           // 4 MiB
)

// pacedListen creates a listener with socket-level pacing and buffer tuning
func pacedListen(network, addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			c.Control(func(fd uintptr) {
				if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soMaxPacingRateOptS, bytesPerSecondSrv); e != nil && err == nil {
					err = e
				}
				if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, sockBufBytesSrv); e != nil && err == nil {
					err = e
				}
				if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, sockBufBytesSrv); e != nil && err == nil {
					err = e
				}
			})
			return err
		},
	}
	return lc.Listen(context.Background(), network, addr)
}

// StartPacedGRPCServer starts a gRPC server with socket-level pacing
// This wraps the standard grpc.Server.Serve with a custom listener
func StartPacedGRPCServer(ctx context.Context, addr string, grpcSrv *grpc.Server) error {
	listener, err := pacedListen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to create paced listener on %s: %w", addr, err)
	}

	// Start serving in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- grpcSrv.Serve(listener)
	}()

	// Wait for context cancellation or serve error
	select {
	case <-ctx.Done():
		grpcSrv.GracefulStop()
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}
