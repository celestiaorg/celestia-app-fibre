package fibre

import (
	"fmt"
	"os"
	"path/filepath"

	"cosmossdk.io/log"
	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cometbft/cometbft/node"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"google.golang.org/grpc"
)

// SetupServer initializes and registers the Fibre server with the gRPC server for a validator node.
// Returns the Fibre server instance and an error. The server should be stopped gracefully during shutdown.
// If the node is not a validator (no usable PrivValidator), this function does nothing and returns nil, nil.
// If the node is a validator and initialization fails, returns an error (preventing node startup).
func SetupServer(
	cmtNode *node.Node,
	grpcServer *grpc.Server,
	grpcClient *grpc.ClientConn,
	logger log.Logger,
	rootDir string,
	chainID string,
) (*Server, error) {
	// Get PrivValidator from CometBFT node
	privVal := cmtNode.PrivValidator()
	if privVal == nil {
		logger.Info("Node is not a validator, skipping Fibre server startup")
		return nil, nil
	}
	logger.Info("Initializing Fibre server for validator")

	// Create QueryClient from gRPC connection
	if grpcClient == nil {
		return nil, fmt.Errorf("gRPC client is not initialized")
	}
	queryClient := types.NewQueryClient(grpcClient)

	// Create SetGetter using BlockAPI gRPC client
	blockAPIClient := coregrpc.NewBlockAPIClient(grpcClient)
	valGet := fibregrpc.NewSetGetter(blockAPIClient)

	// Create BadgerDB store in the application home directory
	storeConfig := DefaultStoreConfig()
	storePath := filepath.Join(rootDir, "data", "fibre-store")
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create Fibre store directory: %w", err)
	}
	store, err := NewBadgerStore(storePath, storeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre store: %w", err)
	}
	logger.Info("Using Badger store for Fibre server", "path", storePath)

	// Create ServerConfig
	serverConfig := DefaultServerConfig()

	// Get chain ID from config or use default
	if chainID != "" {
		serverConfig.ChainID = chainID
	}

	// Create Fibre Server
	fibreServer, err := NewServer(privVal, queryClient, valGet, store, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre server: %w", err)
	}

	// Register Fibre server with gRPC server
	types.RegisterFibreServer(grpcServer, fibreServer)
	logger.Info("Fibre server registered with gRPC server", "chain-id", serverConfig.ChainID, "block-time", serverConfig.BlockTime)
	return fibreServer, nil
}
