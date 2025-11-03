//go:build multiplexer

package abci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
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
// If isValidator is false, this function does nothing and returns nil.
// If isValidator is true and initialization fails, returns an error (preventing node startup).
func (m *Multiplexer) startFibreServer(
	ctx context.Context,
	cmtNode *node.Node,
	grpcServer *grpc.Server,
	isValidator bool,
) error {
	if !isValidator {
		m.logger.Info("Node is not a validator, skipping Fibre server startup")
		return nil
	}

	m.logger.Info("Initializing Fibre server for validator node")

	// Get PrivValidator from CometBFT node
	privVal := cmtNode.PrivValidator()
	if privVal == nil {
		return fmt.Errorf("failed to get PrivValidator from CometBFT node")
	}

	// Create QueryClient from gRPC connection
	if m.clientContext.GRPCClient == nil {
		return fmt.Errorf("gRPC client is not initialized")
	}
	queryClient := types.NewQueryClient(m.clientContext.GRPCClient)

	// Create SetGetter using BlockAPI gRPC client
	// We need to create a BlockAPIClient from the gRPC connection
	// Since BlockAPI is registered on the gRPC server, we can use the client connection
	if m.clientContext.GRPCClient == nil {
		return fmt.Errorf("gRPC client is not initialized for BlockAPI")
	}
	// Create BlockAPIClient from the gRPC client connection
	blockAPIClient := coregrpc.NewBlockAPIClient(m.clientContext.GRPCClient)
	valGet := validator.NewGrpcGetter(blockAPIClient)

	// Create Store (badger by default)
	homeDir := m.svrCtx.Config.RootDir
	storePath := filepath.Join(homeDir, "data", "fibre-store")
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return fmt.Errorf("failed to create Fibre store directory: %w", err)
	}

	storeCfg := fibre.DefaultStoreConfig()
	store, err := fibre.NewBadgerStore(storePath, storeCfg)
	if err != nil {
		return fmt.Errorf("failed to create Fibre store: %w", err)
	}

	// Create ServerConfig
	serverCfg := fibre.DefaultServerConfig()
	serverCfg.ChainID = m.svrCtx.Viper.GetString("chain-id")
	if serverCfg.ChainID == "" {
		// Fallback: try to get chain ID from genesis
		genDoc := cmtNode.GenesisDoc()
		if genDoc != nil {
			serverCfg.ChainID = genDoc.ChainID
		} else {
			// Use chainID from multiplexer
			serverCfg.ChainID = m.chainID
		}
	}
	// BlockTime defaults to 6s from DefaultServerConfig

	// Create Fibre Server
	fibreServer, err := fibre.NewServer(
		privVal,
		queryClient,
		valGet,
		store,
		serverCfg,
	)
	if err != nil {
		return fmt.Errorf("failed to create Fibre server: %w", err)
	}

	// Register Fibre server with gRPC server
	types.RegisterFibreServer(grpcServer, fibreServer)

	m.logger.Info("Fibre server registered with gRPC server", "chain-id", serverCfg.ChainID, "store-path", storePath)

	return nil
}
