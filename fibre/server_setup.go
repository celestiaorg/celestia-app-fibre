package fibre

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cometbft/cometbft/node"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"google.golang.org/grpc"
)

// Logger interface abstracts logging functionality
type Logger interface {
	Info(msg string, keyvals ...interface{})
}

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
	Logger Logger
	// RootDir is the root directory for default store paths
	RootDir string
	// Enabled indicates whether the Fibre server should be started
	Enabled bool
	// StoreType is the store type: "memory" or "badger"
	StoreType string
	// StorePath is the path for the badger store (only used if StoreType is "badger")
	StorePath string
	// ChainID is the chain ID (will fallback to genesis if empty)
	ChainID string
	// DefaultChainID is the fallback chain ID if not found in config or genesis
	DefaultChainID string
	// BlockTime is the expected block time (0 means use default)
	BlockTime time.Duration
}

// SetupServer initializes and registers the Fibre server with the gRPC server for a validator node.
// Returns the Fibre server instance and an error. The server should be stopped gracefully during shutdown.
// If the node is not a validator (no usable PrivValidator), this function does nothing and returns nil, nil.
// If the node is a validator and initialization fails, returns an error (preventing node startup).
func SetupServer(cfg ServerSetupConfig) (*Server, error) {
	// Check if Fibre server is enabled
	if !cfg.Enabled {
		cfg.Logger.Info("Fibre server is disabled via flag, skipping startup")
		return nil, nil
	}

	// Check if node is a validator by checking if PrivValidator exists and is usable
	if !isValidatorNode(cfg.Node) {
		cfg.Logger.Info("Node is not a validator, skipping Fibre server startup")
		return nil, nil
	}

	cfg.Logger.Info("Initializing Fibre server for validator node")

	// Get PrivValidator from CometBFT node
	privVal := cfg.Node.PrivValidator()
	if privVal == nil {
		return nil, fmt.Errorf("failed to get PrivValidator from CometBFT node")
	}

	// Create QueryClient from gRPC connection
	if cfg.GRPCClient == nil {
		return nil, fmt.Errorf("gRPC client is not initialized")
	}
	queryClient := types.NewQueryClient(cfg.GRPCClient)

	// Create SetGetter using BlockAPI gRPC client
	blockAPIClient := coregrpc.NewBlockAPIClient(cfg.GRPCClient)
	valGet := fibregrpc.NewSetGetter(blockAPIClient)

	// Create Store based on config
	storeType := cfg.StoreType
	if storeType == "" {
		storeType = "badger" // default
	}

	storeCfg := DefaultStoreConfig()
	var store *Store
	var err error

	switch storeType {
	case "memory":
		store = NewMemoryStore(storeCfg)
		cfg.Logger.Info("Using in-memory store for Fibre server")
	case "badger":
		// Get store path from config or use default
		storePath := cfg.StorePath
		if storePath == "" {
			// Default to <home>/data/fibre-store
			storePath = filepath.Join(cfg.RootDir, "data", "fibre-store")
		}
		if err := os.MkdirAll(storePath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create Fibre store directory: %w", err)
		}
		store, err = NewBadgerStore(storePath, storeCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create Fibre store: %w", err)
		}
		cfg.Logger.Info("Using Badger store for Fibre server", "path", storePath)
	default:
		return nil, fmt.Errorf("invalid store type: %s (must be 'memory' or 'badger')", storeType)
	}

	// Create ServerConfig
	serverCfg := DefaultServerConfig()

	// Get chain ID from config or genesis (should match the node's chain ID)
	chainID := cfg.ChainID
	if chainID == "" {
		// Fallback: try to get chain ID from genesis
		genDoc := cfg.Node.GenesisDoc()
		if genDoc != nil {
			chainID = genDoc.ChainID
		} else {
			// Use provided default fallback
			chainID = cfg.DefaultChainID
		}
	}
	serverCfg.ChainID = chainID

	// Get block time from config or use default
	if cfg.BlockTime > 0 {
		serverCfg.BlockTime = cfg.BlockTime
	}
	// Otherwise BlockTime defaults to 6s from DefaultServerConfig

	// Create Fibre Server
	fibreServer, err := NewServer(
		privVal,
		queryClient,
		valGet,
		store,
		serverCfg,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fibre server: %w", err)
	}

	// Register Fibre server with gRPC server
	types.RegisterFibreServer(cfg.GRPCServer, fibreServer)

	cfg.Logger.Info("Fibre server registered with gRPC server",
		"chain-id", serverCfg.ChainID,
		"block-time", serverCfg.BlockTime,
		"store-type", storeType)

	return fibreServer, nil
}

// isValidatorNode checks if the node has a usable PrivValidator configured.
// Returns true if PrivValidator exists on the node and has a valid public key, false otherwise.
// This works for both FilePV and KMS-based validators, and correctly identifies non-validators
// even if they have a FilePV file.
func isValidatorNode(n *node.Node) bool {
	privVal := n.PrivValidator()
	if privVal == nil {
		return false
	}
	// Check if PrivValidator has a valid public key
	// This distinguishes actual validators from non-validators that might have a FilePV
	pubKey, err := privVal.GetPubKey()
	if err != nil || pubKey == nil {
		return false
	}
	return true
}
