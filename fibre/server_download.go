package fibre

import (
	"context"
	"errors"
	"fmt"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DownloadRows handles the [types.FibreServer.DownloadRows] RPC call.
// It retrieves stored rows for the given commitment.
func (s *Server) DownloadRows(ctx context.Context, req *types.DownloadRowsRequest) (*types.DownloadRowsResponse, error) {
	ctx, span := s.tracer.Start(ctx, "fibre.Server.DownloadRows")
	defer span.End()

	// unmarshal and validate commitment
	var commitment Commitment
	if err := commitment.UnmarshalBinary(req.Commitment); err != nil {
		s.log.ErrorContext(ctx, "invalid commitment", "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid commitment")
		return nil, status.Error(grpccodes.InvalidArgument, fmt.Sprintf("invalid commitment: %v", err))
	}

	// retrieve rows from storage
	rows, err := s.store.Get(ctx, commitment)
	if err != nil {
		if errors.Is(err, ErrStoreNotFound) {
			s.log.WarnContext(ctx, "no rows found for commitment", "blob_commitment", commitment.String())
			span.SetStatus(codes.Error, "no rows found")
			return nil, status.Error(grpccodes.NotFound, fmt.Sprintf("no rows found for commitment %s", commitment.String()))
		}
		s.log.ErrorContext(ctx, "failed to retrieve rows", "blob_commitment", commitment.String(), "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to retrieve rows")
		return nil, status.Error(grpccodes.Internal, fmt.Sprintf("failed to retrieve rows: %v", err))
	}

	var rowSize int
	if len(rows.Rows) > 0 && len(rows.Rows[0].Data) > 0 {
		rowSize = len(rows.Rows[0].Data)
	}
	span.AddEvent("rows_read", trace.WithAttributes(
		attribute.Int("row_count", len(rows.Rows)),
		attribute.Int("row_size", rowSize),
	))

	s.log.InfoContext(ctx, "download successful",
		"blob_commitment", commitment.String(),
		"rows", len(rows.Rows),
		"row_size", rowSize,
	)

	span.SetStatus(codes.Ok, "")
	return &types.DownloadRowsResponse{
		Rows: rows,
	}, nil
}
