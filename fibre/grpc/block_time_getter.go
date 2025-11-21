package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"google.golang.org/grpc"
)

// BlockTimeGetter implements the [BlockTimeGetter] interface by fetching the latest block time
// using the Service gRPC client.
type BlockTimeGetter struct {
	client cmtservice.ServiceClient
}

// NewBlockTimeGetter creates a new [BlockTimeGetter] instance with the provided gRPC connection.
func NewBlockTimeGetter(conn *grpc.ClientConn) *BlockTimeGetter {
	return &BlockTimeGetter{
		client: cmtservice.NewServiceClient(conn),
	}
}

// GetBlockTime returns the current block time from the blockchain by querying the latest block.
func (g *BlockTimeGetter) GetBlockTime(ctx context.Context) (time.Time, error) {
	resp, err := g.client.GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return time.Time{}, fmt.Errorf("getting latest block: %w", err)
	}

	if resp == nil || resp.SdkBlock == nil {
		return time.Time{}, fmt.Errorf("invalid response: block is nil")
	}

	return resp.SdkBlock.Header.Time, nil
}

var _ validator.BlockTimeGetter = (*BlockTimeGetter)(nil)
