package fibre_test

import (
	"context"
	"testing"
	"time"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/go-square/v3/share"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"
)

func makeTestPaymentPromise(chainID string, height int64, commitment fibre.Commitment, timestamp time.Time) *fibre.PaymentPromise {
	ns := share.MustNewV0Namespace([]byte("test"))
	signerKey := secp256k1.GenPrivKey().PubKey().(*secp256k1.PubKey)

	return &fibre.PaymentPromise{
		ChainID:           chainID,
		Height:            height,
		Namespace:         ns,
		BlobSize:          1024,
		Commitment:        commitment,
		CreationTimestamp: timestamp,
		SignerKey:         signerKey,
		Signature:         []byte("test-signature-64-bytes-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
	}
}

func TestStore_PutAndGet(t *testing.T) {
	ctx := context.Background()
	store := fibre.NewMemoryStore()

	commitment := fibre.Commitment{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}
	timestamp := time.Date(2025, 10, 21, 15, 30, 0, 0, time.UTC)
	promise := makeTestPaymentPromise("test-chain", 100, commitment, timestamp)

	// create test rows
	rows := &types.Rows{
		Rows: []*types.Row{
			{Index: 0, Data: []byte("row0"), Proof: [][]byte{[]byte("proof0")}},
			{Index: 1, Data: []byte("row1"), Proof: [][]byte{[]byte("proof1")}},
			{Index: 2, Data: []byte("row2"), Proof: [][]byte{[]byte("proof2")}},
		},
		Rlc: &types.Rows_Root{Root: make([]byte, 32)},
	}

	// put data
	err := store.Put(ctx, promise, rows)
	require.NoError(t, err)

	// get rows by commitment
	gotRows, err := store.Get(ctx, promise.Commitment)
	require.NoError(t, err)
	require.Len(t, gotRows.Rows, 3)
	require.Equal(t, rows.Rows[0].Index, gotRows.Rows[0].Index)
	require.Equal(t, rows.Rows[1].Index, gotRows.Rows[1].Index)
	require.Equal(t, rows.Rows[2].Index, gotRows.Rows[2].Index)

	// get payment promise by hash
	promiseHash, err := promise.Hash()
	require.NoError(t, err)
	gotPromise, err := store.GetPaymentPromise(ctx, promiseHash)
	require.NoError(t, err)
	require.Equal(t, promise.ChainID, gotPromise.ChainID)
	require.Equal(t, promise.Height, gotPromise.Height)
	require.Equal(t, promise.Commitment, gotPromise.Commitment)
}

func TestStore_Delete(t *testing.T) {
	ctx := context.Background()
	store := fibre.NewMemoryStore()

	commitment := fibre.Commitment{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}
	timestamp := time.Date(2025, 10, 21, 15, 30, 0, 0, time.UTC)
	promise := makeTestPaymentPromise("test-chain", 100, commitment, timestamp)

	rows := &types.Rows{
		Rows: []*types.Row{{Index: 0, Data: []byte("test"), Proof: [][]byte{[]byte("proof")}}},
		Rlc:  &types.Rows_Root{Root: make([]byte, 32)},
	}

	// put data
	err := store.Put(ctx, promise, rows)
	require.NoError(t, err)

	// verify data exists
	gotRows, err := store.Get(ctx, promise.Commitment)
	require.NoError(t, err)
	require.Len(t, gotRows.Rows, 1)

	// delete data
	promiseHash, err := promise.Hash()
	require.NoError(t, err)
	err = store.Delete(ctx, promise.Commitment, promiseHash)
	require.NoError(t, err)

	// verify data deleted
	_, err = store.Get(ctx, promise.Commitment)
	require.Error(t, err)

	_, err = store.GetPaymentPromise(ctx, promiseHash)
	require.Error(t, err)
}
