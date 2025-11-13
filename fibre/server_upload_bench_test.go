package fibre_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const (
	benchBufConnSize         = 512 * 1024 * 1024 // 512 MiB in-memory pipe to avoid transport bottlenecks
	benchMaxGRPCMsgSize      = 1 << 30           // 1 GiB to cover worst-case UploadRows payload
	benchInitialStreamWindow = 32 * 1024 * 1024  // 32 MiB per-stream HTTP/2 window
	benchInitialConnWindow   = 64 * 1024 * 1024  // 64 MiB per-connection HTTP/2 window
	benchGRPCReadBufferSize  = 8 * 1024 * 1024
	benchGRPCWriteBufferSize = 8 * 1024 * 1024
)

// BenchmarkServerUploadRowsHighBandwidth exercises the gRPC boundary between client and server
// with tuned buffers to ensure that any throughput limits we observe come from Fibre itself.
func BenchmarkServerUploadRowsHighBandwidth(b *testing.B) {
	testCases := []struct {
		name          string
		blobSize      int
		numValidators int
	}{
		{name: "blob_32MiB/validators_4", blobSize: 32 * 1024 * 1024, numValidators: 4},
		// {name: "blob_128MiB/validators_4", blobSize: 128 * 1024 * 1024, numValidators: 4},
		// {name: "blob_128MiB/validators_4", blobSize: 1024 * 1024 * 1024, numValidators: 4},
	}

	for _, tc := range testCases {
		tc := tc

		b.Run(tc.name+"/serial", func(b *testing.B) {
			env := newServerBenchmarkEnv(b, tc.numValidators, tc.blobSize)
			b.Cleanup(env.Close)

			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(env.bytesPerRequest))
			b.ResetTimer()

			for b.Loop() {
				_, err := env.client.UploadRows(ctx, env.request)
				if err != nil {
					b.Fatalf("UploadRows failed: %v", err)
				}
			}
		})

		b.Run(tc.name+"/parallel_16", func(b *testing.B) {
			env := newServerBenchmarkEnv(b, tc.numValidators, tc.blobSize)
			b.Cleanup(env.Close)

			b.ReportAllocs()
			b.SetBytes(int64(env.bytesPerRequest))
			b.SetParallelism(16)
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				ctx := context.Background()
				for pb.Next() {
					if _, err := env.client.UploadRows(ctx, env.request); err != nil {
						b.Fatalf("UploadRows failed: %v", err)
					}
				}
			})
		})
	}
}

type serverBenchmarkEnv struct {
	client          types.FibreClient
	request         *types.UploadRowsRequest
	bytesPerRequest int

	grpcServer  *grpc.Server
	listener    *bufconn.Listener
	clientConn  *grpc.ClientConn
	serverConn  *grpc.ClientConn
	fibreServer *fibre.Server
}

func newServerBenchmarkEnv(b *testing.B, numValidators, blobSize int) *serverBenchmarkEnv {
	b.Helper()

	validators, privKeys := makeTestValidators(b, numValidators)
	valSet := validator.Set{
		ValidatorSet: core.NewValidatorSet(validators),
		Height:       100,
	}

	privVal := newTestPrivValidator(privKeys[0])
	pubKey, err := privVal.GetPubKey()
	require.NoError(b, err)
	serverValidator, found := valSet.GetByAddress(pubKey.Address())
	require.True(b, found, "server validator must exist in validator set")

	listener := bufconn.Listen(benchBufConnSize)
	grpcServer := grpc.NewServer(tunedServerOptions()...)

	types.RegisterQueryServer(grpcServer, &mockQueryServer{})
	valSetProto, err := valSet.ToProto()
	require.NoError(b, err)
	coregrpc.RegisterBlockAPIServer(grpcServer, &mockBlockAPIServer{
		validatorSetResponse: &coregrpc.ValidatorSetResponse{
			ValidatorSet: valSetProto,
			Height:       int64(valSet.Height),
		},
	})

	bufDialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.Dial()
	}

	serverConn, err := grpc.DialContext(context.Background(), "bufnet", tunedDialOptions(bufDialer)...)
	require.NoError(b, err)

	serverCfg := fibre.DefaultServerConfig()
	serverCfg.StoreConfig.Path = filepath.Join(b.TempDir(), "fibre-store")
	serverCfg.Tracer = trace.NewNoopTracerProvider().Tracer("fibre-server-bench")
	serverCfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))

	fibreServer, err := fibre.NewServerFromGRPC(privVal, grpcServer, serverConn, serverCfg)
	require.NoError(b, err)

	go func() {
		if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			panic(err)
		}
	}()

	clientConn, err := grpc.DialContext(context.Background(), "bufnet", tunedDialOptions(bufDialer)...)
	require.NoError(b, err)

	request := makeUploadRowsRequest(b, valSet, serverValidator, blobSize, nil)

	return &serverBenchmarkEnv{
		client:          types.NewFibreClient(clientConn),
		request:         request,
		bytesPerRequest: rowsPayloadSize(request.Rows),
		grpcServer:      grpcServer,
		listener:        listener,
		clientConn:      clientConn,
		serverConn:      serverConn,
		fibreServer:     fibreServer,
	}
}

func (e *serverBenchmarkEnv) Close() {
	if e.clientConn != nil {
		_ = e.clientConn.Close()
	}
	if e.serverConn != nil {
		_ = e.serverConn.Close()
	}
	if e.fibreServer != nil {
		_ = e.fibreServer.Stop()
	}
	if e.grpcServer != nil {
		e.grpcServer.Stop()
	}
	if e.listener != nil {
		_ = e.listener.Close()
	}
}

func tunedServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(benchMaxGRPCMsgSize),
		grpc.MaxSendMsgSize(benchMaxGRPCMsgSize),
		grpc.ReadBufferSize(benchGRPCReadBufferSize),
		grpc.WriteBufferSize(benchGRPCWriteBufferSize),
		grpc.InitialWindowSize(benchInitialStreamWindow),
		grpc.InitialConnWindowSize(benchInitialConnWindow),
	}
}

func tunedDialOptions(dialer func(context.Context, string) (net.Conn, error)) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithReadBufferSize(benchGRPCReadBufferSize),
		grpc.WithWriteBufferSize(benchGRPCWriteBufferSize),
		grpc.WithInitialWindowSize(benchInitialStreamWindow),
		grpc.WithInitialConnWindowSize(benchInitialConnWindow),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(benchMaxGRPCMsgSize),
			grpc.MaxCallSendMsgSize(benchMaxGRPCMsgSize),
		),
	}
}

func rowsPayloadSize(rows *types.Rows) int {
	if rows == nil {
		return 0
	}

	total := 0
	for _, row := range rows.Rows {
		total += len(row.Data)
		for _, proofChunk := range row.Proof {
			total += len(proofChunk)
		}
	}
	if coeffs := rows.GetCoefficients(); len(coeffs) > 0 {
		total += len(coeffs)
	}
	return total
}
