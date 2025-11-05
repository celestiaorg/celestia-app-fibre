package fibre

import (
	"fmt"

	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	"google.golang.org/grpc"
)

// SetupServer initializes and registers the Fibre server with the gRPC server
// for a validator node. Returns the Fibre server instance and an error. The
// server should be stopped gracefully during shutdown.
func SetupServer(
	privVal core.PrivValidator,
	grpcServer *grpc.Server,
	grpcClient *grpc.ClientConn,
	serverConfig ServerConfig,
) (*Server, error) {
	queryClient := types.NewQueryClient(grpcClient)
	blockAPIClient := coregrpc.NewBlockAPIClient(grpcClient)
	setGetter := fibregrpc.NewSetGetter(blockAPIClient)

	// Create BadgerDB store in the application home directory
	store, err := NewBadgerStore(serverConfig.StoreConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre store: %w", err)
	}

	// Create Fibre Server
	fibreServer, err := NewServer(privVal, queryClient, setGetter, store, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre server: %w", err)
	}

	// Register Fibre server with gRPC server
	types.RegisterFibreServer(grpcServer, fibreServer)
	return fibreServer, nil
}
