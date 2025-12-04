package fibre_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	core "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
)

func TestClientDownload(t *testing.T) {
	tests := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"Success", testClientDownloadSuccess},
		{"Concurrent", testClientDownloadConcurrent},
		{"ContextCancellation", testClientDownloadContextCancellation},
		{"ClosedClient", testClientDownloadClosedClient},
		{"SucceedsWithPartialFailures", testClientDownloadSucceedsWithPartialFailures},
		{"BlobNotFound", testClientDownloadBlobNotFound},
		{"NotEnoughRows", testClientDownloadNotEnoughRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func testClientDownloadSuccess(t *testing.T) {
	blob := makeTestBlobV0(t, 256*1024)
	client := makeTestDownloadClient(t, 10, 0, []*fibre.Blob{blob}, nil)
	defer client.Close()

	downloaded, err := client.Download(t.Context(), blob.Commitment())
	require.NoError(t, err)
	require.NotNil(t, downloaded)
	require.Equal(t, blob.Data(), downloaded.Data())
}

func testClientDownloadConcurrent(t *testing.T) {
	const numConcurrent = 5

	blobs := make([]*fibre.Blob, numConcurrent)
	for i := range numConcurrent {
		blobs[i] = makeTestBlobV0(t, 256*1024)
	}

	client := makeTestDownloadClient(t, 100, 0, blobs, nil)
	defer client.Close()

	var wg sync.WaitGroup
	for _, blob := range blobs {
		wg.Add(1)
		go func(blob *fibre.Blob) {
			defer wg.Done()

			downloaded, err := client.Download(t.Context(), blob.Commitment())
			require.NoError(t, err)
			require.Equal(t, blob.Data(), downloaded.Data())
		}(blob)
	}
	wg.Wait()
}

func testClientDownloadContextCancellation(t *testing.T) {
	blob := makeTestBlobV0(t, 256*1024)
	client := makeTestDownloadClient(t, 10, 0, []*fibre.Blob{blob}, nil)
	defer client.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := client.Download(ctx, fibre.Commitment{})
	require.ErrorIs(t, err, context.Canceled)
}

func testClientDownloadClosedClient(t *testing.T) {
	blob := makeTestBlobV0(t, 256*1024)
	client := makeTestDownloadClient(t, 10, 0, []*fibre.Blob{blob}, nil)

	require.NoError(t, client.Close())
	require.NoError(t, client.Close()) // idempotent

	_, err := client.Download(t.Context(), fibre.Commitment{})
	require.ErrorIs(t, err, fibre.ErrClientClosed)
}

func testClientDownloadSucceedsWithPartialFailures(t *testing.T) {
	blob := makeTestBlobV0(t, 256*1024)
	// fail 1/3 of validators
	client := makeTestDownloadClient(t, 10, 3, []*fibre.Blob{blob}, nil)
	defer client.Close()

	downloaded, err := client.Download(t.Context(), blob.Commitment())
	require.NoError(t, err)
	require.NotNil(t, downloaded)
	require.Equal(t, blob.Data(), downloaded.Data())
}

func testClientDownloadBlobNotFound(t *testing.T) {
	blob := makeTestBlobV0(t, 256*1024)
	client := makeTestDownloadClient(t, 10, 0, []*fibre.Blob{blob}, nil)
	defer client.Close()

	// request a commitment that doesn't exist
	_, err := client.Download(t.Context(), fibre.Commitment{1, 2, 3})
	require.ErrorIs(t, err, fibre.ErrBlobNotFound)
}

func testClientDownloadNotEnoughRows(t *testing.T) {
	const numValidators = 10
	blob := makeTestBlobV0(t, 256*1024)
	// fail most validators so we don't get enough rows for reconstruction
	client := makeTestDownloadClient(t, numValidators, numValidators-1, []*fibre.Blob{blob}, func(cfg *fibre.ClientConfig) {
		cfg.DownloadConcurrency = numValidators
	})
	defer client.Close()

	_, err := client.Download(t.Context(), blob.Commitment())
	require.ErrorIs(t, err, fibre.ErrNotEnoughRows)
}

// makeTestDownloadClient creates a download client that serves the given blobs.
// numFailures specifies how many validators should fail (0 for none).
func makeTestDownloadClient(
	t *testing.T,
	numValidators, numFailures int,
	blobs []*fibre.Blob,
	customCfg func(*fibre.ClientConfig),
) *fibre.Client {
	t.Helper()

	validators, privKeys := makeTestValidators(t, numValidators)

	var failCount atomic.Int32
	mockClientFn := func(ctx context.Context, val *core.Validator) (grpc.Client, error) {
		valIdx := -1
		for i, v := range validators {
			if v.Address.String() == val.Address.String() {
				valIdx = i
				break
			}
		}
		if valIdx == -1 {
			return nil, fmt.Errorf("no private key found for validator %s", val.Address)
		}

		client := &downloadMockClient{
			validator:     val,
			valIdx:        valIdx,
			numValidators: numValidators,
			privKey:       privKeys[valIdx],
			blobs:         blobs,
		}

		if numFailures > 0 {
			currentCount := failCount.Add(1)
			if currentCount <= int32(numFailures) {
				return failingClient{}, nil
			}
		}
		return client, nil
	}

	cfg := fibre.DefaultClientConfig()
	cfg.NewClientFn = mockClientFn
	if numFailures > 0 {
		cfg.DownloadConcurrency = 5
	}
	if customCfg != nil {
		customCfg(&cfg)
	}

	valSet := validator.Set{ValidatorSet: core.NewValidatorSet(validators), Height: 100}
	client, err := fibre.NewClient(nil, makeTestKeyring(t), &mockValidatorSetGetter{set: valSet}, &mockHostRegistry{}, cfg)
	require.NoError(t, err)
	return client
}

// mock infrastructure

type downloadMockClient struct {
	validator     *core.Validator
	valIdx        int
	numValidators int
	privKey       cmted25519.PrivKey
	blobs         []*fibre.Blob
}

func (d *downloadMockClient) UploadShard(ctx context.Context, req *types.UploadShardRequest, opts ...grpclib.CallOption) (*types.UploadShardResponse, error) {
	return &types.UploadShardResponse{}, nil
}

func (d *downloadMockClient) DownloadShard(ctx context.Context, req *types.DownloadShardRequest, opts ...grpclib.CallOption) (*types.DownloadShardResponse, error) {
	var commitment fibre.Commitment
	copy(commitment[:], req.Commitment)

	// find the blob matching the commitment
	var blob *fibre.Blob
	for _, b := range d.blobs {
		if commitment.Equals(b.Commitment()) {
			blob = b
			break
		}
	}
	if blob == nil {
		return &types.DownloadShardResponse{}, nil
	}

	// determine which rows this validator should return
	blobCfg := fibre.DefaultBlobConfigV0()
	totalRows := blobCfg.OriginalRows + blobCfg.ParityRows

	var rowIndices []int
	for i := 0; i < totalRows; i++ {
		if i%d.numValidators == d.valIdx {
			rowIndices = append(rowIndices, i)
		}
	}

	rows := make([]*types.BlobRow, 0, len(rowIndices))
	var rlcRoot [32]byte
	for _, idx := range rowIndices {
		row, err := blob.Row(idx)
		if err != nil {
			continue
		}
		rows = append(rows, &types.BlobRow{
			Index: uint32(row.Index),
			Data:  row.Row,
			Proof: row.RowProof.RowProof,
		})
		if len(rows) == 1 {
			rlcRoot = row.RLCRoot
		}
	}

	return &types.DownloadShardResponse{
		Shard: &types.BlobShard{
			Rlc:  &types.BlobShard_Root{Root: rlcRoot[:]},
			Rows: rows,
		},
	}, nil
}

func (d *downloadMockClient) Close() error {
	return nil
}
