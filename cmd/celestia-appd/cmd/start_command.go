//go:build !multiplexer

package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"cosmossdk.io/log"
	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	pvm "github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	"github.com/cometbft/cometbft/rpc/client/local"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/server"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servergrpc "github.com/cosmos/cosmos-sdk/server/grpc"
	servercmtlog "github.com/cosmos/cosmos-sdk/server/log"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	if svrCfg.GRPC.Enable {
		var err error
		grpcServer, clientCtx, err = startGRPCServer(ctx, g, svrCtx, clientCtx, appInstance, svrCfg, cmtNode)
		if err != nil {
			return fmt.Errorf("failed to start gRPC server: %w", err)
		}
	}

	// Check if node is a validator and start Fibre server if needed
	// Fibre server requires gRPC to be enabled
	if svrCfg.GRPC.Enable {
		isValidator := isValidatorNode(svrCtx.Config)
		if err := startFibreServer(
			ctx,
			svrCtx,
			clientCtx,
			cmtNode,
			grpcServer,
			isValidator,
		); err != nil {
			if isValidator {
				// Validator nodes must have Fibre server working
				return fmt.Errorf("failed to start Fibre server (validator node): %w", err)
			}
			// Non-validator nodes can continue without Fibre server
			svrCtx.Logger.Error("failed to start Fibre server (non-validator)", "error", err)
		}
	} else {
		svrCtx.Logger.Info("gRPC server is disabled, skipping Fibre server startup")
	}

	// Start API server if enabled
	// Note: API server requires app instance to register routes
	// For now, we skip it - can be added later if needed
	if svrCfg.API.Enable && grpcServer != nil {
		svrCtx.Logger.Info("API server is enabled but not yet implemented in custom start handler")
		// TODO: Implement API server setup with appInstance.RegisterAPIRoutes
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

// startGRPCServer creates and starts a gRPC server, returning the server and updated client context.
// The ctx parameter is the cancellation context that will be used for graceful shutdown.
// The g parameter is the error group that manages the goroutines.
func startGRPCServer(
	ctx context.Context,
	g *errgroup.Group,
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

	// Create gRPC server
	grpcServer, err := servergrpc.NewGRPCServer(clientCtx, appInstance, svrCfg.GRPC)
	if err != nil {
		return nil, clientCtx, fmt.Errorf("failed to create gRPC server: %w", err)
	}

	// Register BlockAPI on gRPC server (needed for Fibre server's SetGetter)
	coreEnv, err := cmtNode.ConfigureRPC()
	if err != nil {
		return nil, clientCtx, fmt.Errorf("failed to configure RPC for CometBFT node: %w", err)
	}
	blockAPI := coregrpc.NewBlockAPI(coreEnv)
	coregrpc.RegisterBlockAPIServer(grpcServer, blockAPI)

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

	return grpcServer, clientCtx, nil
}

// startAPIServer starts the API server.
func startAPIServer(svrCtx *server.Context, svrCfg serverconfig.Config, grpcServer *grpc.Server) error {
	// API server needs client context - we'll create a minimal one
	// The actual implementation should use the client context from startCommandHandler
	// For now, this is a placeholder - API server setup needs the app instance
	svrCtx.Logger.Info("API server startup not fully implemented yet", "address", svrCfg.API.Address)
	// TODO: Implement full API server setup similar to multiplexer
	return nil
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
