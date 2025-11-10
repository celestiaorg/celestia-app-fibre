package fibre

import (
	"context"
	"errors"
	"fmt"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DownloadRows handles the [types.FibreServer.DownloadRows] RPC call.
// It retrieves stored rows for the requested commitment from the Fibre store.
func (s *Server) DownloadRows(ctx context.Context, req *types.DownloadRowsRequest) (*types.DownloadRowsResponse, error) {
	if req == nil || len(req.Commitment) == 0 {
		return nil, status.Error(codes.InvalidArgument, "commitment is required")
	}

	var commitment Commitment
	if err := commitment.UnmarshalBinary(req.Commitment); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid commitment: %v", err))
	}

	rows, err := s.store.Get(ctx, commitment)
	if err != nil {
		if errors.Is(err, ErrStoreNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("rows not found for commitment %s", commitment.String()))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get rows: %v", err))
	}

	return &types.DownloadRowsResponse{
		Rows: rows,
	}, nil
}
