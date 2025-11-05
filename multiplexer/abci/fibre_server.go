//go:build multiplexer

package abci

import (
	"context"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/cometbft/cometbft/node"
	"google.golang.org/grpc"
)

const (
	// ChainIDKey is the viper key for the chain ID
	ChainIDKey = "chain-id"
)

// startFibreServer initializes and registers the Fibre server with the gRPC server for a validator node.
// Returns the Fibre server instance and an error. The server should be stopped gracefully during shutdown.
// If the node is not a validator (no usable PrivValidator), this function does nothing and returns nil, nil.
// If the node is a validator and initialization fails, returns an error (preventing node startup).
func (m *Multiplexer) startFibreServer(
	_ context.Context,
	cmtNode *node.Node,
	grpcServer *grpc.Server,
) (*fibre.Server, error) {
	return fibre.SetupServer(fibre.ServerSetupConfig{
		Node:       cmtNode,
		GRPCServer: grpcServer,
		GRPCClient: m.clientContext.GRPCClient,
		Logger:     m.logger,
		RootDir:    m.svrCtx.Config.RootDir,
		Enabled:    m.svrCtx.Viper.GetBool("fibre.enable"),
		ChainID:    m.svrCtx.Viper.GetString(ChainIDKey),
	})
}
