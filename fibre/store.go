package fibre

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	badger "github.com/ipfs/go-ds-badger4"
	pebble "github.com/ipfs/go-ds-pebble"
)

var (
	// ErrStoreNotFound is returned when no rows are found for a commitment in the store.
	ErrStoreNotFound = errors.New("no rows found in store")
)

// Store manages persistent storage of [PaymentPromise] and row data.
// It provides indexed access by [Commitment], promise hash, and timestamp.
type Store struct {
	ds ds.Batching
}

// NewMemoryStore creates a new [Store] with an in-memory datastore.
func NewMemoryStore() *Store {
	return &Store{
		ds: dssync.MutexWrap(ds.NewMapDatastore()),
	}
}

// NewBadgerStore creates a new [Store] with a badger4 datastore at the given path.
func NewBadgerStore(path string) (*Store, error) {
	opts := badger.DefaultOptions
	opts.GcDiscardRatio = 0.2
	opts.GcInterval = 15 * time.Minute
	opts.GcSleep = 10 * time.Second

	bds, err := badger.NewDatastore(path, &opts)
	if err != nil {
		return nil, fmt.Errorf("creating badger datastore: %w", err)
	}

	return &Store{
		ds: bds,
	}, nil
}

// NewPebbleStore creates a new [Store] with a pebble datastore at the given path.
func NewPebbleStore(path string) (*Store, error) {
	pds, err := pebble.NewDatastore(path, nil)
	if err != nil {
		return nil, fmt.Errorf("creating pebble datastore: %w", err)
	}

	return &Store{
		ds: pds,
	}, nil
}

// Put stores the [PaymentPromise] and rows in the datastore.
// Rows are stored as a single blob under /rows/<commitment>/<promise-hash>.
// The payment promise is stored under /pp/<promise-hash>.
// An empty value is indexed under /pp/<timestamp-YYYYMMDDHHmm>/<commitment>/<promise-hash> for time-based queries.
func (s *Store) Put(ctx context.Context, promise *PaymentPromise, rows *types.Rows) error {
	batch, err := s.ds.Batch(ctx)
	if err != nil {
		return fmt.Errorf("creating batch: %w", err)
	}

	ppData, err := gogoproto.Marshal(promise.ToProto())
	if err != nil {
		return fmt.Errorf("marshaling payment promise: %w", err)
	}
	promiseHash, err := promise.Hash()
	if err != nil {
		return fmt.Errorf("getting promise hash: %w", err)
	}
	if err := batch.Put(ctx, promiseKey(promiseHash), ppData); err != nil {
		return fmt.Errorf("putting payment promise: %w", err)
	}

	rowsData, err := gogoproto.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshaling rows: %w", err)
	}
	if err := batch.Put(ctx, rowsKey(promise.Commitment, promiseHash), rowsData); err != nil {
		return fmt.Errorf("putting rows: %w", err)
	}

	// create timestamp index
	if err := batch.Put(ctx, timestampKey(promise.CreationTimestamp, promise.Commitment, promiseHash), []byte{}); err != nil {
		return fmt.Errorf("putting timestamp index: %w", err)
	}

	return batch.Commit(ctx)
}

// Get retrieves rows with RLC root for the given [Commitment].
// If multiple stored Rows exist for the commitment, returns the first one that successfully unmarshals.
// If unmarshaling fails for some rows, it continues trying others and collects errors.
// Returns an error only if all rows fail to unmarshal (with all errors joined).
func (s *Store) Get(ctx context.Context, commitment Commitment) (*types.Rows, error) {
	results, err := s.ds.Query(ctx, query.Query{
		Prefix: fmt.Sprintf("/rows/%s", commitment.String()),
	})
	if err != nil {
		return nil, fmt.Errorf("querying rows: %w", err)
	}
	defer results.Close()

	// iterate through all results, return first successful unmarshal
	for result := range results.Next() {
		if result.Error != nil {
			err = errors.Join(err, result.Error)
			continue
		}

		rows := &types.Rows{}
		if err := gogoproto.Unmarshal(result.Value, rows); err != nil {
			err = errors.Join(err, fmt.Errorf("unmarshaling rows: %w", err))
			continue
		}

		// successfully unmarshaled, return immediately
		return rows, nil
	}
	if err != nil {
		return nil, err
	}

	return nil, ErrStoreNotFound
}

// GetPaymentPromise retrieves a [PaymentPromise] by its hash.
func (s *Store) GetPaymentPromise(ctx context.Context, promiseHash []byte) (*PaymentPromise, error) {
	data, err := s.ds.Get(ctx, promiseKey(promiseHash))
	if err != nil {
		return nil, fmt.Errorf("getting payment promise: %w", err)
	}

	var ppProto types.PaymentPromise
	if err := gogoproto.Unmarshal(data, &ppProto); err != nil {
		return nil, fmt.Errorf("unmarshaling payment promise: %w", err)
	}

	var promise PaymentPromise
	if err := promise.FromProto(&ppProto); err != nil {
		return nil, fmt.Errorf("converting from proto: %w", err)
	}

	return &promise, nil
}

// Delete removes the [PaymentPromise] and rows for the given [Commitment] and promise hash.
// NOTE: This function does not delete the timestamp index and it is expected to be deleted by DeleteAt.
func (s *Store) Delete(ctx context.Context, commitment Commitment, promiseHash []byte) error {
	batch, err := s.ds.Batch(ctx)
	if err != nil {
		return fmt.Errorf("creating batch: %w", err)
	}

	if err := batch.Delete(ctx, promiseKey(promiseHash)); err != nil {
		return fmt.Errorf("deleting payment promise: %w", err)
	}
	if err := batch.Delete(ctx, rowsKey(commitment, promiseHash)); err != nil {
		return fmt.Errorf("deleting rows: %w", err)
	}

	return batch.Commit(ctx)
}

// Close closes the underlying datastore.
func (s *Store) Close() error {
	return s.ds.Close()
}

// formatTimestamp formats a timestamp with minute precision (YYYYMMDDHHmm).
// This format is used for timestamp-based indexing in the datastore.
func formatTimestamp(timestamp time.Time) string {
	return timestamp.Format("200601021504")
}

func promiseKey(promiseHash []byte) ds.Key {
	return ds.NewKey(fmt.Sprintf("/pp/%s", hex.EncodeToString(promiseHash)))
}

func rowsKey(commitment Commitment, promiseHash []byte) ds.Key {
	return ds.NewKey(fmt.Sprintf("/rows/%s/%s", commitment.String(), hex.EncodeToString(promiseHash)))
}

func timestampKey(timestamp time.Time, commitment Commitment, promiseHash []byte) ds.Key {
	return ds.NewKey(fmt.Sprintf("/pp/%s/%s/%s", formatTimestamp(timestamp), commitment.String(), hex.EncodeToString(promiseHash)))
}
