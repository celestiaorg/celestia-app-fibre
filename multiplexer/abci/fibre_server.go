//go:build multiplexer

package abci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/node"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"google.golang.org/grpc"
)

// isValidatorNode checks if the node has a PrivValidator configured.
// Returns true if PrivValidatorKeyFile exists and is readable, false otherwise.
func isValidatorNode(cfg *cmtcfg.Config) bool {
	pvKeyFile := cfg.PrivValidatorKeyFile()
	_, err := os.Stat(pvKeyFile)
	return err == nil
}

// startFibreServer initializes and registers the Fibre server with the gRPC server for a validator node.
// Returns the Fibre server instance and an error. The server should be stopped gracefully during shutdown.
// If isValidator is false, this function does nothing and returns nil, nil.
// If isValidator is true and initialization fails, returns an error (preventing node startup).
func (m *Multiplexer) startFibreServer(
	ctx context.Context,
	cmtNode *node.Node,
	grpcServer *grpc.Server,
	isValidator bool,
) (*fibre.Server, error) {
	// Check if Fibre server is enabled via flag
	fibreEnabled := m.svrCtx.Viper.GetBool("fibre.enable")
	if !fibreEnabled {
		m.logger.Info("Fibre server is disabled via flag, skipping startup")
		return nil, nil
	}

	if !isValidator {
		m.logger.Info("Node is not a validator, skipping Fibre server startup")
		return nil, nil
	}

	m.logger.Info("Initializing Fibre server for validator node")

	// Get PrivValidator from CometBFT node
	privVal := cmtNode.PrivValidator()
	if privVal == nil {
		return nil, fmt.Errorf("failed to get PrivValidator from CometBFT node")
	}

	// Create QueryClient from gRPC connection
	if m.clientContext.GRPCClient == nil {
		return nil, fmt.Errorf("gRPC client is not initialized")
	}
	queryClient := types.NewQueryClient(m.clientContext.GRPCClient)

	// Create SetGetter using BlockAPI gRPC client
	// We need to create a BlockAPIClient from the gRPC connection
	// Since BlockAPI is registered on the gRPC server, we can use the client connection
	if m.clientContext.GRPCClient == nil {
		return nil, fmt.Errorf("gRPC client is not initialized for BlockAPI")
	}
	// Create BlockAPIClient from the gRPC client connection
	blockAPIClient := coregrpc.NewBlockAPIClient(m.clientContext.GRPCClient)
	valGet := fibregrpc.NewSetGetter(blockAPIClient)

	// Create Store based on CLI flag
	storeType := m.svrCtx.Viper.GetString("fibre.store-type")
	if storeType == "" {
		storeType = "badger" // default
	}

	storeCfg := fibre.DefaultStoreConfig()
	var store *fibre.Store
	var err error

	if storeType == "memory" {
		store = fibre.NewMemoryStore(storeCfg)
		m.logger.Info("Using in-memory store for Fibre server")
	} else if storeType == "badger" {
		// Get store path from flag or use default
		storePath := m.svrCtx.Viper.GetString("fibre.store-path")
		if storePath == "" {
			// Default to <home>/data/fibre-store
			homeDir := m.svrCtx.Config.RootDir
			storePath = filepath.Join(homeDir, "data", "fibre-store")
		}
		if err := os.MkdirAll(storePath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create Fibre store directory: %w", err)
		}
		store, err = fibre.NewBadgerStore(storePath, storeCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create Fibre store: %w", err)
		}
		m.logger.Info("Using Badger store for Fibre server", "path", storePath)
	} else {
		return nil, fmt.Errorf("invalid store type: %s (must be 'memory' or 'badger')", storeType)
	}

	// Create ServerConfig
	serverCfg := fibre.DefaultServerConfig()

	// Get chain ID from config or genesis (should match the node's chain ID)
	chainID := m.svrCtx.Viper.GetString("chain-id")
	if chainID == "" {
		// Fallback: try to get chain ID from genesis
		genDoc := cmtNode.GenesisDoc()
		if genDoc != nil {
			chainID = genDoc.ChainID
		} else {
			// Use chainID from multiplexer
			chainID = m.chainID
		}
	}
	serverCfg.ChainID = chainID

	// Get block time from flag or use default
	blockTime := m.svrCtx.Viper.GetDuration("fibre.block-time")
	if blockTime > 0 {
		serverCfg.BlockTime = blockTime
	}
	// Otherwise BlockTime defaults to 6s from DefaultServerConfig

	// Create Fibre Server
	fibreServer, err := fibre.NewServer(
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
	types.RegisterFibreServer(grpcServer, fibreServer)

	m.logger.Info("Fibre server registered with gRPC server",
		"chain-id", serverCfg.ChainID,
		"block-time", serverCfg.BlockTime,
		"store-type", storeType)

	return fibreServer, nil
}
