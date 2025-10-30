package fibre_test

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/rand"
	"testing"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/go-square/v3/share"
	"github.com/stretchr/testify/require"
)

// TestClientServerDownload validates end-to-end download flow at scale.
// tests that uploaded blobs can be downloaded from validators.
func TestClientServerDownload(t *testing.T) {
	const (
		numValidators  = 10
		numClients     = 10
		blobsPerClient = 5
		peakBlobSize   = 256 * 1024 // 256 KiB max
	)

	env := makeTestEnv(t, numValidators, numClients, func(cfg *fibre.ClientConfig) {
		// ensure all validators receive rows by setting target signatures to 100%
		cfg.UploadTargetSignaturesCount.Numerator = 1
		cfg.UploadTargetSignaturesCount.Denominator = 1
	}, nil)
	defer env.Close()

	totalBlobs := numClients * blobsPerClient
	allCommitments := make([]fibre.Commitment, totalBlobs)
	allData := make([][]byte, totalBlobs)

	// first upload blobs to test download
	err := env.ForEachClient(t.Context(), func(ctx context.Context, client *fibre.Client, clientIdx int) error {
		for blobIdx := range blobsPerClient {
			// generate random blob data with random size up to peakBlobSize
			blobSize := 1 + rand.Intn(peakBlobSize)
			data := make([]byte, blobSize)
			if _, err := cryptorand.Read(data); err != nil {
				return fmt.Errorf("generating random data for blob %d: %w", blobIdx, err)
			}

			blob, err := fibre.NewBlob(data, client.Config().BlobConfig)
			if err != nil {
				return fmt.Errorf("creating blob %d: %w", blobIdx, err)
			}

			ns := share.MustNewV0Namespace([]byte{byte(clientIdx >> 8), byte(clientIdx)})
			_, err = client.Upload(ctx, ns, blob)
			if err != nil {
				return fmt.Errorf("uploading blob %d: %w", blobIdx, err)
			}

			// write to designated slot without mutex
			slotIdx := clientIdx*blobsPerClient + blobIdx
			allCommitments[slotIdx] = blob.Commitment()
			allData[slotIdx] = data
		}

		return nil
	})
	require.NoError(t, err)

	// then download and validate
	err = env.ForEachClient(t.Context(), func(ctx context.Context, client *fibre.Client, clientIdx int) error {
		for blobIdx := range blobsPerClient {
			slotIdx := clientIdx*blobsPerClient + blobIdx
			commitment := allCommitments[slotIdx]
			originalData := allData[slotIdx]

			blob, err := client.Download(ctx, commitment)
			if err != nil {
				return fmt.Errorf("downloading blob %s: %w", commitment.String(), err)
			}

			// validate we got the blob back
			if blob == nil {
				return fmt.Errorf("downloaded blob is nil for commitment %s", commitment.String())
			}

			// validate data matches original
			downloadedData := blob.Data()
			if !bytes.Equal(downloadedData, originalData) {
				return fmt.Errorf("data mismatch for %s: downloaded %d bytes, expected %d bytes",
					commitment.String(), len(downloadedData), len(originalData))
			}

			// validate commitment matches
			if !blob.Commitment().Equals(commitment) {
				return fmt.Errorf("commitment mismatch: got %s, expected %s",
					blob.Commitment().String(), commitment.String())
			}
		}

		return nil
	})
	require.NoError(t, err)
}
