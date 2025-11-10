package testnode

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"cosmossdk.io/log"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/rpc/client/local"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/server/api"
	srvconfig "github.com/cosmos/cosmos-sdk/server/config"
	srvgrpc "github.com/cosmos/cosmos-sdk/server/grpc"
	srvtypes "github.com/cosmos/cosmos-sdk/server/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// noOpCleanup is a function that conforms to the cleanup function signature and
// performs no operation.
var noOpCleanup = func() error { return nil }

// StartNode starts the Comet node along with a local core RPC client. The
// RPC is returned via the client.Context. The function returned should be
// called during cleanup to teardown the node, core client, along with canceling
// the internal context.Context in the returned Context.
func StartNode(cometNode *node.Node, cctx Context) (Context, func() error, error) {
	if err := cometNode.Start(); err != nil {
		return cctx, noOpCleanup, err
	}
	client := local.New(cometNode)
	cctx.Context = cctx.WithClient(client)
	cleanup := func() error {
		err := cometNode.Stop()
		if err != nil {
			return err
		}
		cometNode.Wait()
		if err = removeDir(path.Join([]string{cctx.HomeDir, "config"}...)); err != nil {
			return err
		}
		return removeDir(path.Join([]string{cctx.HomeDir, cometNode.Config().DBPath}...))
	}

	return cctx, cleanup, nil
}

// StartGRPCServer starts the GRPC server using the provided application and
// config. A GRPC client connection to that server is also added to the client
// context. The returned function should be used to shutdown the server.
func StartGRPCServer(
	logger log.Logger,
	app srvtypes.Application,
	appCfg *srvconfig.Config,
	cctx Context,
	tmNode *node.Node,
	cfg *Config,
) (*grpc.Server, Context, func() error, error) {
	emptycleanup := func() error { return nil }
	// Add the tx service in the gRPC router.
	app.RegisterTxService(cctx.Context)

	// Add the tendermint queries service in the gRPC router.
	app.RegisterTendermintService(cctx.Context)

	app.RegisterNodeService(cctx.Context, *appCfg)

	grpcSrv, err := srvgrpc.NewGRPCServer(cctx.Context, app, appCfg.GRPC)
	if err != nil {
		return nil, Context{}, emptycleanup, err
	}

	coreEnv, err := tmNode.ConfigureRPC()
	if err != nil {
		return nil, Context{}, emptycleanup, err
	}

	blockAPI := coregrpc.NewBlockAPI(coreEnv)
	coregrpc.RegisterBlockAPIServer(grpcSrv, blockAPI)

	nodeGRPCAddr := strings.Replace(appCfg.GRPC.Address, "0.0.0.0", "localhost", 1)
	conn, err := grpc.NewClient(
		nodeGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.ForceCodec(codec.NewProtoCodec(cctx.InterfaceRegistry).GRPCCodec()),
			grpc.MaxCallSendMsgSize(math.MaxInt32),
			grpc.MaxCallRecvMsgSize(math.MaxInt32),
		),
	)
	if err != nil {
		return nil, Context{}, emptycleanup, err
	}

	cctx.Context = cctx.WithGRPCClient(conn)

	var fibreServer *fibre.Server
	if cfg != nil && cfg.EnableFibreServer {
		if !appCfg.GRPC.Enable {
			return nil, Context{}, emptycleanup, fmt.Errorf("gRPC server must be enabled to start Fibre server")
		}
		serverCfg := fibre.DefaultServerConfig()
		if cfg.Genesis != nil && cfg.Genesis.ChainID != "" {
			serverCfg.ChainID = cfg.Genesis.ChainID
		} else if cctx.ChainID != "" {
			serverCfg.ChainID = cctx.ChainID
		}
		storeRoot := filepath.Join(cctx.HomeDir, "data", "fibre-store")
		if cfg.TmConfig != nil && cfg.TmConfig.RootDir != "" {
			storeRoot = filepath.Join(cfg.TmConfig.RootDir, "data", "fibre-store")
		}
		if err := os.MkdirAll(storeRoot, 0o755); err != nil {
			return nil, Context{}, emptycleanup, fmt.Errorf("creating fibre store dir: %w", err)
		}
		serverCfg.Path = storeRoot
		if cfg.TmConfig != nil {
			if blockTime := cfg.TmConfig.Consensus.TimeoutCommit; blockTime > 0 {
				serverCfg.BlockTime = blockTime
			}
		}

		fibreServer, err = fibre.NewServerFromGRPC(tmNode.PrivValidator(), grpcSrv, cctx.GRPCClient, serverCfg)
		if err != nil {
			return nil, Context{}, emptycleanup, err
		}
	}

	go blockAPI.StartNewBlockEventListener(cctx.goContext) //nolint:errcheck

	// Create a goroutine-safe logger for the gRPC server to prevent
	// "Log in goroutine after test has completed" panics when using test loggers
	grpcLogger := log.NewNopLogger()
	if logger != nil {
		// Use a simple stdout logger that won't become invalid when tests complete
		grpcLogger = log.NewLogger(os.Stdout)
	}

	go func() {
		// StartGRPCServer is a blocking function, we need to run it in a go routine.
		if err := srvgrpc.StartGRPCServer(cctx.goContext, grpcLogger, appCfg.GRPC, grpcSrv); err != nil {
			panic(err)
		}
	}()

	return grpcSrv, cctx, func() error {
		if fibreServer != nil {
			if err := fibreServer.Stop(); err != nil {
				return fmt.Errorf("stopping Fibre server: %w", err)
			}
		}
		grpcSrv.Stop()
		return nil
	}, nil
}

func StartAPIServer(app srvtypes.Application, appCfg srvconfig.Config, cctx Context, grpcSrv *grpc.Server) (*api.Server, error) {
	apiSrv := api.New(cctx.Context, log.NewNopLogger(), grpcSrv)
	app.RegisterAPIRoutes(apiSrv, appCfg.API)
	errCh := make(chan error)
	go func() {
		if err := apiSrv.Start(cctx.goContext, appCfg); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return nil, err
	case <-time.After(500 * time.Millisecond): // assume server started successfully
	}

	return apiSrv, nil
}
