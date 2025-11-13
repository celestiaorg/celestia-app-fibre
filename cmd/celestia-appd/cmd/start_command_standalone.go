//go:build !multiplexer

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"cosmossdk.io/log"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	pvm "github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	"github.com/cometbft/cometbft/rpc/client/local"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	db "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/api"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servergrpc "github.com/cosmos/cosmos-sdk/server/grpc"
	"github.com/cosmos/cosmos-sdk/server/grpc/gogoreflection"
	reflection "github.com/cosmos/cosmos-sdk/server/grpc/reflection/v2alpha1"
	servercmtlog "github.com/cosmos/cosmos-sdk/server/log"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/hashicorp/yamux"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"storj.io/drpc"
	"storj.io/drpc/drpcmanager"
	"storj.io/drpc/drpcserver"
	"storj.io/drpc/drpcstream"
	"storj.io/drpc/drpcwire"
)

// startCommandHandler is a custom start command handler that wraps the default Cosmos SDK
// start logic and adds Fibre server initialization for validator nodes.
func startCommandHandler(
	svrCtx *server.Context,
	clientCtx client.Context,
	appCreator servertypes.AppCreator,
	withCmt bool,
	opts server.StartCmdOptions,
) error {
	if !withCmt {
		return fmt.Errorf("cannot start app without CometBFT")
	}

	// Create cancellation context and error group FIRST, before starting any services
	// This ensures all services can be gracefully shut down when signals are received
	ctx, cancelFn := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)
	server.ListenForQuitSignals(g, true, cancelFn, svrCtx.Logger)

	// Get server config
	svrCfg, err := serverconfig.GetConfig(svrCtx.Viper)
	if err != nil {
		return fmt.Errorf("failed to get server config: %w", err)
	}

	// Create the application
	// Get DB and trace writer similar to multiplexer pattern
	traceWriter, err := getTraceWriter(svrCtx)
	if err != nil {
		return fmt.Errorf("failed to get trace writer: %w", err)
	}

	home := svrCtx.Config.RootDir
	db, err := openDB(home, server.GetAppDBBackend(svrCtx.Viper))
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	appInstance := appCreator(svrCtx.Logger, db, traceWriter, svrCtx.Viper)

	// Start the CometBFT node
	cmtNode, err := startCometNode(svrCtx, appInstance)
	if err != nil {
		return fmt.Errorf("failed to start CometBFT node: %w", err)
	}

	// Create client context with local client
	clientCtx = clientCtx.WithClient(local.New(cmtNode))

	// Register services if gRPC or API is enabled
	if svrCfg.GRPC.Enable || svrCfg.API.Enable {
		appInstance.RegisterTxService(clientCtx)
		appInstance.RegisterTendermintService(clientCtx)
		appInstance.RegisterNodeService(clientCtx, svrCfg)
	}

	// Start gRPC server if enabled
	var grpcServer *grpc.Server
	var fibreServer *fibre.Server
	var drpcListener net.Listener
	if svrCfg.GRPC.Enable {
		// Create and configure gRPC server (but don't start serving yet)
		var err error
		grpcServer, clientCtx, err = createGRPCServer(svrCtx, clientCtx, appInstance, svrCfg, cmtNode)
		if err != nil {
			return fmt.Errorf("failed to create gRPC server: %w", err)
		}

		// Create Fibre DRPC server BEFORE starting the gRPC server
		serverConfig := fibre.DefaultServerConfig()
		// Get chain ID from genesis (the source of truth) instead of CLI flag
		serverConfig.ChainID = cmtNode.GenesisDoc().ChainID
		serverConfig.Path = filepath.Join(svrCtx.Config.RootDir, "data", "fibre-store")

		// Use DRPC for Fibre service
		var drpcHandler drpc.Handler
		fibreServer, drpcHandler, err = fibre.NewServerFromDRPC(
			cmtNode.PrivValidator(),
			clientCtx.GRPCClient, // Still use gRPC client for queries
			serverConfig,
		)
		if err != nil {
			return fmt.Errorf("failed to create Fibre DRPC server: %w", err)
		}

		// Add graceful shutdown for Fibre server
		g.Go(func() error {
			<-ctx.Done()
			svrCtx.Logger.Info("Stopping Fibre DRPC server")
			if err := fibreServer.Stop(); err != nil {
				svrCtx.Logger.Error("Error stopping Fibre DRPC server", "error", err)
				return err
			}
			return nil
		})

		// Create and configure DRPC server with the handler
		drpcServer := createDRPCServer(svrCtx, drpcHandler)

		// Start DRPC server on port 26658
		drpcPort := "26658" // TODO: Make this configurable
		drpcListener, err = startDRPCServer(ctx, g, svrCtx, drpcServer, drpcPort)
		if err != nil {
			return fmt.Errorf("failed to start DRPC server: %w", err)
		}
		svrCtx.Logger.Info("DRPC server started", "port", drpcPort)

		// Now start the gRPC server (after all services are registered)
		if err := startGRPCServer(ctx, g, svrCtx, svrCfg, grpcServer, cmtNode); err != nil {
			return fmt.Errorf("failed to start gRPC server: %w", err)
		}
	} else {
		svrCtx.Logger.Info("gRPC server is disabled, skipping Fibre server startup")
	}

	// Ensure DRPC listener is closed on shutdown
	if drpcListener != nil {
		g.Go(func() error {
			<-ctx.Done()
			svrCtx.Logger.Info("Closing DRPC listener")
			if err := drpcListener.Close(); err != nil {
				svrCtx.Logger.Error("Error closing DRPC listener", "error", err)
				return err
			}
			return nil
		})
	}

	// Start API server if enabled
	if svrCfg.API.Enable && grpcServer != nil {
		metrics, err := startTelemetry(svrCfg)
		if err != nil {
			return fmt.Errorf("failed to start telemetry: %w", err)
		}

		if err := startAPIServer(ctx, g, svrCtx, clientCtx, appInstance, svrCfg, grpcServer, metrics); err != nil {
			return fmt.Errorf("failed to start API server: %w", err)
		}
	}

	// Wait for signal - all services are now managed by the error group
	return g.Wait()
}

// startCometNode creates and starts a CometBFT node.
func startCometNode(svrCtx *server.Context, appInstance servertypes.Application) (*node.Node, error) {
	cfg := svrCtx.Config

	// Load or generate node key
	nodeKey, err := p2p.LoadOrGenNodeKey(cfg.NodeKeyFile())
	if err != nil {
		return nil, fmt.Errorf("failed to load or generate node key: %w", err)
	}

	// Load or generate private validator
	privVal := pvm.LoadOrGenFilePV(cfg.PrivValidatorKeyFile(), cfg.PrivValidatorStateFile())

	// Create CometBFT ABCI wrapper
	cmtApp := server.NewCometABCIWrapper(appInstance)

	// Create node
	cmtNode, err := node.NewNode(
		cfg,
		privVal,
		nodeKey,
		proxy.NewLocalClientCreator(cmtApp),
		node.DefaultGenesisDocProviderFunc(cfg),
		cmtcfg.DefaultDBProvider,
		node.DefaultMetricsProvider(cfg.Instrumentation),
		servercmtlog.CometLoggerWrapper{Logger: svrCtx.Logger},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CometBFT node: %w", err)
	}

	// Start node
	if err := cmtNode.Start(); err != nil {
		return nil, fmt.Errorf("failed to start CometBFT node: %w", err)
	}

	svrCtx.Logger.Info("CometBFT node started", "node_id", string(nodeKey.ID()))

	return cmtNode, nil
}

// createGRPCServer creates and configures the gRPC server with OpenTelemetry support
// but does not start serving. This allows services (like Fibre) to be registered before the server starts.
func createGRPCServer(
	svrCtx *server.Context,
	clientCtx client.Context,
	appInstance servertypes.Application,
	svrCfg serverconfig.Config,
	cmtNode *node.Node,
) (*grpc.Server, client.Context, error) {
	// Validate gRPC address
	_, _, err := net.SplitHostPort(svrCfg.GRPC.Address)
	if err != nil {
		return nil, clientCtx, fmt.Errorf("invalid gRPC address: %w", err)
	}

	// Create gRPC client for gateway
	grpcClient, err := grpc.NewClient(
		svrCfg.GRPC.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, clientCtx, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	clientCtx = clientCtx.WithGRPCClient(grpcClient)

	// Determine max message sizes
	maxSendMsgSize := svrCfg.GRPC.MaxSendMsgSize
	if maxSendMsgSize == 0 {
		maxSendMsgSize = serverconfig.DefaultGRPCMaxSendMsgSize
	}

	maxRecvMsgSize := svrCfg.GRPC.MaxRecvMsgSize
	if maxRecvMsgSize == 0 {
		maxRecvMsgSize = serverconfig.DefaultGRPCMaxRecvMsgSize
	}

	// Create gRPC server with OpenTelemetry instrumentation
	grpcServer := grpc.NewServer(
		grpc.ForceServerCodec(codec.NewProtoCodec(clientCtx.InterfaceRegistry).GRPCCodec()),
		grpc.MaxSendMsgSize(maxSendMsgSize),
		grpc.MaxRecvMsgSize(maxRecvMsgSize),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)

	// Register application gRPC services
	appInstance.RegisterGRPCServer(grpcServer)

	// Register reflection services for dynamic clients
	err = reflection.Register(grpcServer, reflection.Config{
		SigningModes: func() map[string]int32 {
			supportedModes := clientCtx.TxConfig.SignModeHandler().SupportedModes()
			modes := make(map[string]int32, len(supportedModes))
			for _, m := range supportedModes {
				modes[m.String()] = (int32)(m)
			}
			return modes
		}(),
		ChainID:           clientCtx.ChainID,
		SdkConfig:         sdk.GetConfig(),
		InterfaceRegistry: clientCtx.InterfaceRegistry,
	})
	if err != nil {
		return nil, clientCtx, fmt.Errorf("failed to register reflection service: %w", err)
	}

	// Register gogo reflection
	gogoreflection.Register(grpcServer)

	// Register BlockAPI on gRPC server (needed for Fibre server's SetGetter)
	coreEnv, err := cmtNode.ConfigureRPC()
	if err != nil {
		return nil, clientCtx, fmt.Errorf("failed to configure RPC for CometBFT node: %w", err)
	}
	blockAPI := coregrpc.NewBlockAPI(coreEnv)
	coregrpc.RegisterBlockAPIServer(grpcServer, blockAPI)

	svrCtx.Logger.Info("gRPC server created and configured with OpenTelemetry", "address", svrCfg.GRPC.Address)

	return grpcServer, clientCtx, nil
}

// startGRPCServer starts the gRPC server and BlockAPI event listener.
// The server must have all services registered before this is called.
func startGRPCServer(
	ctx context.Context,
	g *errgroup.Group,
	svrCtx *server.Context,
	svrCfg serverconfig.Config,
	grpcServer *grpc.Server,
	cmtNode *node.Node,
) error {
	// Configure RPC for BlockAPI event listener
	coreEnv, err := cmtNode.ConfigureRPC()
	if err != nil {
		return fmt.Errorf("failed to configure RPC for CometBFT node: %w", err)
	}
	blockAPI := coregrpc.NewBlockAPI(coreEnv)

	// Start BlockAPI event listener using the cancellation context
	// This ensures it can be gracefully shut down when signals are received
	g.Go(func() error {
		return blockAPI.StartNewBlockEventListener(ctx)
	})

	// Start gRPC server using the cancellation context
	// This ensures it can be gracefully shut down when signals are received
	g.Go(func() error {
		return servergrpc.StartGRPCServer(
			ctx,
			svrCtx.Logger.With(log.ModuleKey, "grpc-server"),
			svrCfg.GRPC,
			grpcServer,
		)
	})

	svrCtx.Logger.Info("gRPC server started", "address", svrCfg.GRPC.Address)

	return nil
}

// startAPIServer initializes and starts the API server, setting up routes, telemetry, and running it within an error group.
func startAPIServer(
	ctx context.Context,
	g *errgroup.Group,
	svrCtx *server.Context,
	clientCtx client.Context,
	appInstance servertypes.Application,
	svrCfg serverconfig.Config,
	grpcServer *grpc.Server,
	metrics *telemetry.Metrics,
) error {
	// Set home directory in client context
	clientCtx = clientCtx.WithHomeDir(svrCtx.Config.RootDir)

	// Create API server
	apiSrv := api.New(clientCtx, svrCtx.Logger.With(log.ModuleKey, "api-server"), grpcServer)

	// Register API routes from the application
	appInstance.RegisterAPIRoutes(apiSrv, svrCfg.API)

	// Set telemetry if enabled
	if svrCfg.Telemetry.Enabled {
		apiSrv.SetTelemetry(metrics)
	}

	// Start API server in a goroutine using the cancellation context
	// This ensures it can be gracefully shut down when signals are received
	svrCtx.Logger.Info("Starting API server", "address", svrCfg.API.Address)
	g.Go(func() error {
		return apiSrv.Start(ctx, svrCfg)
	})

	return nil
}

// startTelemetry initializes telemetry metrics if telemetry is enabled in the configuration.
func startTelemetry(cfg serverconfig.Config) (*telemetry.Metrics, error) {
	return telemetry.New(cfg.Telemetry)
}

// getTraceWriter gets the trace writer from server context, similar to multiplexer.
func getTraceWriter(svrCtx *server.Context) (io.WriteCloser, error) {
	// Check for trace-store flag
	traceWriterFile := svrCtx.Viper.GetString("trace-store")
	return openTraceWriter(traceWriterFile)
}

// openTraceWriter opens a trace writer for the given file.
// If the file is empty, it returns no writer and no error.
func openTraceWriter(traceWriterFile string) (io.WriteCloser, error) {
	if traceWriterFile == "" {
		return nil, nil
	}
	return os.OpenFile(
		traceWriterFile,
		os.O_WRONLY|os.O_APPEND|os.O_CREATE,
		0o666,
	)
}

// openDB opens the application database.
func openDB(rootDir string, backendType db.BackendType) (db.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return db.NewDB("application", backendType, dataDir)
}

// createDRPCServer creates and configures the DRPC server with the provided handler.
// The handler should have all DRPC services registered.
func createDRPCServer(svrCtx *server.Context, handler drpc.Handler) *drpcserver.Server {
	// Create DRPC server with the provided handler (mux with registered services)
	// Configure with large message size limits for Fibre data transfers (256 MB)
	const maxMessageSize = 256 * 1024 * 1024 // 256 MB
	drpcSrv := drpcserver.NewWithOptions(handler, drpcserver.Options{
		Manager: drpcmanager.Options{
			Reader: drpcwire.ReaderOptions{MaximumBufferSize: maxMessageSize},
			Stream: drpcstream.Options{MaximumBufferSize: maxMessageSize},
		},
	})

	svrCtx.Logger.Info("DRPC server created", "max_message_size", maxMessageSize)
	return drpcSrv
}

// startDRPCServer starts the DRPC server on the specified port.
// The server must have all services registered before this is called.
// Uses yamux for connection multiplexing over TCP.
// Returns the net.Listener so it can be closed on shutdown.
func startDRPCServer(
	ctx context.Context,
	g *errgroup.Group,
	svrCtx *server.Context,
	drpcSrv *drpcserver.Server,
	port string,
) (net.Listener, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%s", port))
	if err != nil {
		return nil, fmt.Errorf("failed to create DRPC listener on port %s: %w", port, err)
	}

	g.Go(func() error {
		svrCtx.Logger.Info("DRPC server started with yamux", "address", listener.Addr().String())

		for {
			select {
			case <-ctx.Done():
				// Context cancelled, stop accepting connections
				svrCtx.Logger.Info("DRPC server context cancelled")
				return nil
			default:
				// Accept new TCP connection
				tcpConn, err := listener.Accept()
				if err != nil {
					// Check if we're shutting down
					select {
					case <-ctx.Done():
						return nil
					default:
						svrCtx.Logger.Error("TCP accept error", "error", err)
						continue
					}
				}

				// Handle TCP connection in separate goroutine
				// Each TCP connection will have a yamux server session
				go handleYamuxConnection(ctx, svrCtx, tcpConn, drpcSrv)
			}
		}
	})

	return listener, nil
}

// handleYamuxConnection handles a single TCP connection by creating a yamux server session
// and serving DRPC on each yamux stream.
func handleYamuxConnection(ctx context.Context, svrCtx *server.Context, tcpConn net.Conn, drpcSrv *drpcserver.Server) {
	svrCtx.Logger.Info("handleYamuxConnection: TCP connection received", "remote_addr", tcpConn.RemoteAddr())

	// Create yamux server session on the TCP connection
	yamuxSess, err := yamux.Server(tcpConn, nil)
	if err != nil {
		svrCtx.Logger.Error("failed to create yamux session", "error", err)
		tcpConn.Close()
		return
	}

	svrCtx.Logger.Info("yamux session established successfully", "remote_addr", tcpConn.RemoteAddr())

	// Accept streams from the yamux session and serve DRPC on each stream
	streamCount := 0
	for {
		svrCtx.Logger.Info("yamux: waiting to accept stream...", "stream_count", streamCount)
		stream, err := yamuxSess.Accept()
		if err != nil {
			// Session closed or error
			if err != io.EOF {
				svrCtx.Logger.Warn("yamux accept error", "error", err, "stream_count", streamCount)
			} else {
				svrCtx.Logger.Info("yamux session closed (EOF)", "stream_count", streamCount)
			}
			yamuxSess.Close()
			tcpConn.Close()
			return
		}

		streamCount++
		svrCtx.Logger.Info("yamux: stream accepted", "stream_id", streamCount, "remote_addr", stream.RemoteAddr())

		// Serve DRPC on this yamux stream
		// Note: We don't close the stream here; yamux handles stream lifecycle
		go func(s net.Conn, id int) {
			svrCtx.Logger.Info("DRPC: starting ServeOne", "stream_id", id)
			if err := drpcSrv.ServeOne(ctx, s); err != nil && !errors.Is(err, io.EOF) {
				svrCtx.Logger.Error("DRPC serve error", "error", err, "stream_id", id)
			} else {
				svrCtx.Logger.Info("DRPC: ServeOne completed successfully", "stream_id", id)
			}
		}(stream, streamCount)
	}
}
