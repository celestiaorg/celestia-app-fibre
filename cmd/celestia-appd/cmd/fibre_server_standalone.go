//go:build !multiplexer

package cmd

import (
	"context"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/cometbft/cometbft/node"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/server"
	"google.golang.org/grpc"
)

// startFibreServer initializes and registers the Fibre server with the gRPC server for a validator node.
// Returns the Fibre server instance and an error. The server should be stopped gracefully during shutdown.
// If the node is not a validator (no usable PrivValidator), this function does nothing and returns nil, nil.
// If the node is a validator and initialization fails, returns an error (preventing node startup).
func startFibreServer(
	ctx context.Context,
	svrCtx *server.Context,
	clientCtx client.Context,
	cmtNode *node.Node,
	grpcServer *grpc.Server,
) (*fibre.Server, error) {
	return fibre.SetupServer(
		cmtNode,
		grpcServer,
		clientCtx.GRPCClient,
		svrCtx.Logger,
		svrCtx.Config.RootDir,
		svrCtx.Viper.GetString(ChainIDKey),
	)
}
