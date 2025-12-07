package fibre_test

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/stretchr/testify/require"
)

// TestClientServerDownload validates end-to-end download flow with various blob sizes and configurations.
func TestClientServerDownload(t *testing.T) {
	tests := []struct {
		name           string
		numValidators  int
		numClients     int
		blobsPerClient int
		blobSize       int
		duplicate      int // upload same blob multiple times at different heights
	}{
		{
			name:           "MaxBlobSize",
			numValidators:  3,
			numClients:     2,
			blobsPerClient: 1,
			blobSize:       fibre.DefaultBlobConfigV0().MaxBlobSize,
			duplicate:      2,
		},
		{
			name:           "MinBlobSize",
			numValidators:  3,
			numClients:     2,
			blobsPerClient: 1,
			blobSize:       1,
			duplicate:      1,
		},
		{
			name:           "ManyClientsManyServersManyBlobs",
			numValidators:  10,
			numClients:     10,
			blobsPerClient: 5,
			blobSize:       128 * 1024, // 128 KiB
			duplicate:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := makeTestEnv(t, tt.numValidators, tt.numClients, func(cfg *fibre.ClientConfig) {
				// ensure all validators receive rows by setting target signatures to 100%
				cfg.UploadTargetSignaturesCount.Numerator = 1
				cfg.UploadTargetSignaturesCount.Denominator = 1
			}, nil)
			defer env.Close()

			totalBlobs := tt.numClients * tt.blobsPerClient
			allCommitments := make([]fibre.Commitment, totalBlobs)
			allData := make([][]byte, totalBlobs)

			// upload blobs
			err := env.ForEachClient(t.Context(), func(ctx context.Context, client *fibre.Client, clientIdx int) error {
				for blobIdx := range tt.blobsPerClient {
					data := make([]byte, tt.blobSize)
					if _, err := cryptorand.Read(data); err != nil {
						return fmt.Errorf("generating random data for blob %d: %w", blobIdx, err)
					}

					blob, err := fibre.NewBlob(data, client.Config().BlobConfig)
					if err != nil {
						return fmt.Errorf("creating blob %d: %w", blobIdx, err)
					}

					// upload blob (possibly multiple times at different heights)
					for uploadIdx := range tt.duplicate {
						if tt.duplicate > 1 {
							env.SetHeight(uint64(100 + uploadIdx*100))
						}
						if _, err := client.Upload(ctx, testNamespace, blob); err != nil {
							return fmt.Errorf("uploading blob %d (upload %d): %w", blobIdx, uploadIdx, err)
						}
					}

					slotIdx := clientIdx*tt.blobsPerClient + blobIdx
					allCommitments[slotIdx] = blob.Commitment()
					allData[slotIdx] = data
				}
				return nil
			})
			require.NoError(t, err)

			// download and validate
			err = env.ForEachClient(t.Context(), func(ctx context.Context, client *fibre.Client, clientIdx int) error {
				for blobIdx := range tt.blobsPerClient {
					slotIdx := clientIdx*tt.blobsPerClient + blobIdx
					commitment := allCommitments[slotIdx]
					originalData := allData[slotIdx]

					blob, err := client.Download(ctx, commitment)
					if err != nil {
						return fmt.Errorf("downloading blob %s: %w", commitment.String(), err)
					}
					if blob == nil {
						return fmt.Errorf("downloaded blob is nil for commitment %s", commitment.String())
					}
					if !bytes.Equal(blob.Data(), originalData) {
						return fmt.Errorf("data mismatch for %s: downloaded %d bytes, expected %d bytes",
							commitment.String(), len(blob.Data()), len(originalData))
					}
					if !blob.Commitment().Equals(commitment) {
						return fmt.Errorf("commitment mismatch: got %s, expected %s",
							blob.Commitment().String(), commitment.String())
					}
				}
				return nil
			})
			require.NoError(t, err)
		})
	}
}
