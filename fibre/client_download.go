package fibre

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/rsema1d"
	core "github.com/cometbft/cometbft/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Download retrieves and reconstructs a blob by commitment from the validator set.
//
// Errors:
//   - [ErrBlobNotFound]: no shard was retrieved for the blob
//   - [ErrNotEnoughRows]: not enough rows were retrieved to reconstruct the original data
//   - reconstruction errors: if the commitment doesn't match or reconstruction fails
//   - context errors: timeouts, cancellations
func (c *Client) Download(ctx context.Context, commitment Commitment) (*Blob, error) {
	if c.closed.Load() {
		return nil, ErrClientClosed
	}

	ctx, span := c.tracer.Start(ctx, "fibre.Client.Download",
		trace.WithAttributes(attribute.String("blob_commitment", commitment.String())),
	)
	defer span.End()

	c.log.DebugContext(ctx, "initiating blob download", "blob_commitment", commitment)

	// get validator set
	// TODO(@Wondertan): If we don't want to pass height here, we should at least ensure we handle the case
	// where the most recent validator set is different from the one the data was posted at somehow.
	valSet, err := c.valGet.Head(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get validator set")
		return nil, fmt.Errorf("getting validator set: %w", err)
	}
	span.AddEvent("got_validator_set", trace.WithAttributes(
		attribute.Int("validator_count", len(valSet.Validators)),
		attribute.Int64("validator_set_height", int64(valSet.Height)),
	))

	blob, err := c.downloadBlob(ctx, valSet, commitment)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to download")
		return nil, err
	}

	err = blob.Reconstruct()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reconstruct")
		return nil, fmt.Errorf("reconstructing data: %w", err)
	}

	c.log.DebugContext(ctx, "blob download completed successfully",
		"blob_commitment", commitment,
		"upload_size", blob.UploadSize(),
		"data_size", blob.DataSize(),
		"row_size", blob.RowSize(),
	)
	span.AddEvent("reconstructed", trace.WithAttributes(
		attribute.Int("data_size", len(blob.Data())),
		attribute.Int("row_size", blob.RowSize()),
	))
	span.SetStatus(codes.Ok, "")
	return blob, nil
}

// downloadFrom downloads a shard for a commitment from a single validator and applies its rows to the blob.
// Returns true if enough rows have been collected for reconstruction.
func (c *Client) downloadFrom(
	ctx context.Context,
	val *core.Validator,
	blob *Blob,
) bool {
	commitment := blob.Commitment()
	log := c.log.With("validator", val.Address.String(), "blob_commitment", commitment)

	ctx, span := c.tracer.Start(ctx, "download_from",
		trace.WithAttributes(attribute.String("validator_address", val.Address.String())),
	)
	defer span.End()

	client, err := c.clientCache.GetClient(ctx, val)
	if err != nil {
		log.WarnContext(ctx, "can't get grpc.FibreClient", "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "can't get grpc.FibreClient")
		return false
	}
	span.AddEvent("client_acquired")

	resp, err := client.DownloadShard(ctx, &types.DownloadShardRequest{Commitment: commitment[:]})
	if err != nil {
		if context.Cause(ctx) == errReconstructed {
			log.DebugContext(ctx, "stopped downloading shard as already reconstructed", "error", err)
			span.AddEvent("already_reconstructed")
			span.SetStatus(codes.Ok, "")
			return false
		}
		log.WarnContext(ctx, "failed to download shard", "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to download shard")
		return false
	}
	rows, err := parseShard(resp.GetShard())
	if err != nil {
		log.WarnContext(ctx, "failed to parse shard", "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse shard")
		return false
	}
	var rowSize int
	if len(rows) > 0 && len(rows[0].Row) > 0 {
		rowSize = len(rows[0].Row)
	}
	span.AddEvent("rows_received", trace.WithAttributes(
		attribute.Int("row_count", len(rows)),
		attribute.Int("row_size", rowSize),
	))

	var applied int
	for _, row := range rows {
		isDone, err := blob.SetRow(row)
		if err != nil {
			log.WarnContext(ctx, "invalid row", "row_index", row.Index, "error", err)
			span.AddEvent("invalid_row", trace.WithAttributes(attribute.Int("row_index", len(rows))))
			continue
		}
		if isDone && applied == 0 {
			log.WarnContext(ctx, "blob was already reconstructed and no rows were applied", "rows_total", len(rows), "row_size", rowSize)
			span.AddEvent("rows_applied", trace.WithAttributes(
				attribute.Int("applied", applied),
				attribute.Int("total", len(rows)),
				attribute.Int("row_size", rowSize),
			))
			span.SetStatus(codes.Ok, "") // this is ok because validator behaved correctly
			return true
		}
		if isDone {
			log.DebugContext(ctx, "got rows completing the blob", "rows_applied", applied, "rows_total", len(rows), "row_size", rowSize)
			span.AddEvent("rows_applied", trace.WithAttributes(
				attribute.Int("applied", applied),
				attribute.Int("total", len(rows)),
				attribute.Int("row_size", rowSize),
			))
			span.SetStatus(codes.Ok, "")
			return true
		}

		applied++
	}

	span.AddEvent("rows_applied", trace.WithAttributes(
		attribute.Int("applied", applied),
		attribute.Int("total", len(rows)),
		attribute.Int("row_size", rowSize),
	))
	if applied == 0 {
		log.WarnContext(ctx, "no rows applied", "rows_total", len(rows), "row_size", rowSize)
		span.SetStatus(codes.Error, "no rows applied")
		return false
	}

	log.DebugContext(ctx, "got rows", "rows_applied", applied, "rows_total", len(rows), "row_size", rowSize)
	span.SetStatus(codes.Ok, "")
	return false
}

// errReconstructed is used to communicate that the blob has been reconstructed.
var errReconstructed = errors.New("already reconstructed")

// downloadBlob downloads shards from validators concurrently and populates the blob.
func (c *Client) downloadBlob(
	ctx context.Context,
	valSet validator.Set,
	commitment Commitment,
) (*Blob, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(errReconstructed)

	var (
		responses            atomic.Uint32         // tracks finished responses
		responsesExhaustedCh = make(chan struct{}) // closes when all responses complete
	)

	var (
		blodDoneOnce atomic.Bool
		blobDone     = make(chan struct{})
		blob         = NewEmptyBlob(c.cfg.BlobConfig, commitment)
	)

	// request shards from validators concurrently
loop:
	for _, val := range valSet.Validators {
		// acquire semaphore before spawning goroutine
		select {
		case c.downloadSem <- struct{}{}:
		case <-blobDone:
			break loop
		case <-ctx.Done():
			break loop
		}

		c.closeWg.Add(1)
		go func(val *core.Validator) {
			defer func() {
				// release semaphore
				<-c.downloadSem

				// mark response as complete if so
				totalRequests := len(valSet.Validators)
				if totalRequests == int(responses.Add(1)) {
					close(responsesExhaustedCh)
				}

				// unblock Close
				c.closeWg.Done()
			}()

			isDone := c.downloadFrom(ctx, val, blob)
			if isDone && blodDoneOnce.CompareAndSwap(false, true) {
				close(blobDone)
			}
		}(val)
	}

	select {
	case <-ctx.Done(): // oops, abort
		return nil, ctx.Err()
	case <-blobDone: // enough data collected
		// stop spawning new requests and cancel ongoing
		cancel(errReconstructed)
	case <-responsesExhaustedCh: // no more responses to wait for
		// attempt to continue with what we have
	}

	return blob, nil
}

// parseShard extracts and validates rows from the BlobShard response, constructing RowInclusionProofs.
// Returns the row inclusion proofs with RLC root already set.
func parseShard(shard *types.BlobShard) ([]*rsema1d.RowInclusionProof, error) {
	if shard == nil {
		return nil, fmt.Errorf("shard response is nil")
	}

	rowsArray := shard.GetRows()
	if len(rowsArray) == 0 {
		return nil, fmt.Errorf("no rows in shard")
	}

	if len(shard.GetRoot()) != 32 {
		return nil, fmt.Errorf("invalid RLC root length: expected 32 bytes, got %d", len(shard.GetRoot()))
	}

	var rlcRoot [32]byte
	copy(rlcRoot[:], shard.GetRoot())

	proofs := make([]*rsema1d.RowInclusionProof, 0, len(rowsArray))
	for _, row := range rowsArray {
		if row == nil {
			continue
		}
		proofs = append(proofs, &rsema1d.RowInclusionProof{
			RowProof: rsema1d.RowProof{
				Index:    int(row.Index),
				Row:      row.Data,
				RowProof: row.Proof,
			},
			RLCRoot: rlcRoot,
		})
	}

	return proofs, nil
}
