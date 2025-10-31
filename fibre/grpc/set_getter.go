package grpc

import (
	"context"
	"fmt"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
)

// GrpcGetter implements the [SetGetter] interface by fetching [Set]
// using the BlockAPI gRPC client.
type GrpcGetter struct {
	client coregrpc.BlockAPIClient
}

// NewGrpcGetter creates a new [GrpcGetter] instance with the provided BlockAPI gRPC client.
func NewGrpcGetter(client coregrpc.BlockAPIClient) *GrpcGetter {
	return &GrpcGetter{
		client: client,
	}
}

// Head returns the latest [Set] by calling getByHeight with 0 height.
// This avoids an additional roundtrip to get the status first.
func (g *GrpcGetter) Head(ctx context.Context) (validator.Set, error) {
	return g.getByHeight(ctx, 0)
}

// GetByHeight returns the [Set] at the specified height.
// Height must be greater than 0. Use [GrpcGetter.Head] to get the latest [Set].
// TODO(@Wondertan): Ensure that server side can handle head+1 case gracefully
func (g *GrpcGetter) GetByHeight(ctx context.Context, height uint64) (validator.Set, error) {
	if height == 0 {
		return validator.Set{}, fmt.Errorf("height must be greater than 0, use Head() to get the latest validator set")
	}
	return g.getByHeight(ctx, height)
}

// getByHeight is the private implementation that allows 0 height for latest validator set.
func (g *GrpcGetter) getByHeight(ctx context.Context, height uint64) (validator.Set, error) {
	signedHeight := int64(height)

	// fetch the validator set for this height (0 means latest)
	validatorResp, err := g.client.ValidatorSet(ctx, &coregrpc.ValidatorSetRequest{
		Height: signedHeight,
	})
	if err != nil {
		return validator.Set{}, fmt.Errorf("getting validator set at height %d: %w", height, err)
	}
	if validatorResp.ValidatorSet == nil {
		return validator.Set{}, fmt.Errorf("validator set is nil in response for height %d", height)
	}

	validatorSet, err := core.ValidatorSetFromProto(validatorResp.ValidatorSet)
	if err != nil {
		return validator.Set{}, fmt.Errorf("converting validator set from proto at height %d: %w", height, err)
	}

	return validator.Set{
		ValidatorSet: validatorSet,
		Height:       uint64(validatorResp.Height),
	}, nil
}

var _ validator.SetGetter = (*GrpcGetter)(nil)
