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

const (
	// StoreTypeMemory represents an in-memory store (ephemeral, non-persistent)
	StoreTypeMemory = "memory"
	// StoreTypeBadger represents a BadgerDB store (persistent on disk)
	StoreTypeBadger = "badger"
)

// ServerSetupConfig contains all the dependencies needed to set up a Fibre server.
// This allows sharing the setup logic between multiplexer and non-multiplexer builds.
type ServerSetupConfig struct {
	// Node is the CometBFT node instance
	Node *node.Node
	// GRPCServer is the gRPC server to register the Fibre service on
	GRPCServer *grpc.Server
	// GRPCClient is the gRPC client connection for creating query clients
	GRPCClient *grpc.ClientConn
	// Logger is used for logging
	Logger log.Logger
	// RootDir is the root directory for default store paths
	RootDir string
	// Enabled indicates whether the Fibre server should be started
	Enabled bool
	// StoreType is the store type: StoreTypeMemory or StoreTypeBadger
	StoreType string
	// StorePath is the path for the badger store (only used if StoreType is StoreTypeBadger)
	StorePath string
	// ChainID is the chain ID
	ChainID string
}

// SetupServer initializes and registers the Fibre server with the gRPC server for a validator node.
// Returns the Fibre server instance and an error. The server should be stopped gracefully during shutdown.
// If the node is not a validator (no usable PrivValidator), this function does nothing and returns nil, nil.
// If the node is a validator and initialization fails, returns an error (preventing node startup).
func SetupServer(config ServerSetupConfig) (*Server, error) {
	if !config.Enabled {
		config.Logger.Info("Fibre server is disabled via flag, skipping Fibre server startup")
		return nil, nil
	}

	// Get PrivValidator from CometBFT node
	privVal := config.Node.PrivValidator()
	if privVal == nil {
		config.Logger.Info("Node is not a validator, skipping Fibre server startup")
		return nil, nil
	}
	config.Logger.Info("Initializing Fibre server for validator")

	// Create QueryClient from gRPC connection
	if config.GRPCClient == nil {
		return nil, fmt.Errorf("gRPC client is not initialized")
	}
	queryClient := types.NewQueryClient(config.GRPCClient)

	// Create SetGetter using BlockAPI gRPC client
	blockAPIClient := coregrpc.NewBlockAPIClient(config.GRPCClient)
	valGet := fibregrpc.NewSetGetter(blockAPIClient)

	// Create Store based on config
	storeType := config.StoreType
	if storeType == "" {
		storeType = StoreTypeBadger // default
	}

	storeConfig := DefaultStoreConfig()
	var store *Store
	var err error

	switch storeType {
	case StoreTypeMemory:
		store = NewMemoryStore(storeConfig)
		config.Logger.Info("Using in-memory store for Fibre server")
	case StoreTypeBadger:
		// Get store path from config or use default
		storePath := config.StorePath
		if storePath == "" {
			// Default to <home>/data/fibre-store
			storePath = filepath.Join(config.RootDir, "data", "fibre-store")
		}
		if err := os.MkdirAll(storePath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create Fibre store directory: %w", err)
		}
		store, err = NewBadgerStore(storePath, storeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Fibre store: %w", err)
		}
		config.Logger.Info("Using Badger store for Fibre server", "path", storePath)
	default:
		return nil, fmt.Errorf("invalid store type: %s (must be %q or %q)", storeType, StoreTypeMemory, StoreTypeBadger)
	}

	// Create ServerConfig
	serverConfig := DefaultServerConfig()

	// Get chain ID from config or use default
	if config.ChainID != "" {
		serverConfig.ChainID = config.ChainID
	}

	// Create Fibre Server
	fibreServer, err := NewServer(privVal, queryClient, valGet, store, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre server: %w", err)
	}

	// Register Fibre server with gRPC server
	types.RegisterFibreServer(config.GRPCServer, fibreServer)
	config.Logger.Info("Fibre server registered with gRPC server", "chain-id", serverConfig.ChainID, "block-time", serverConfig.BlockTime, "store-type", storeType)
	return fibreServer, nil
}
