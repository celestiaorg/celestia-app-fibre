package fibre

import (
	"fmt"
	"os"
	"path/filepath"

	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	"google.golang.org/grpc"
)

// SetupServer initializes and registers the Fibre server with the gRPC server for a validator node.
// Returns the Fibre server instance and an error. The server should be stopped gracefully during shutdown.
// If the node is not a validator (no usable PrivValidator), this function does nothing and returns nil, nil.
// If the node is a validator and initialization fails, returns an error (preventing node startup).
func SetupServer(
	privVal core.PrivValidator,
	grpcServer *grpc.Server,
	grpcClient *grpc.ClientConn,
	rootDir string,
	serverConfig ServerConfig,
) (*Server, error) {
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

	// Create Fibre Server
	fibreServer, err := NewServer(privVal, queryClient, valGet, store, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre server: %w", err)
	}

	// Register Fibre server with gRPC server
	types.RegisterFibreServer(grpcServer, fibreServer)
	return fibreServer, nil
}
