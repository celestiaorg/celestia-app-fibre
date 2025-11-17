package fibre

import (
	"context"
	"errors"
	"sync"

	core "github.com/cometbft/cometbft/types"
)

// TransportClientCache caches [TransportClient]s per validator using the provided constructor function.
// It works with both gRPC and DRPC clients through the [TransportClient] interface.
type TransportClientCache struct {
	newClient TransportClientFn
	mu        sync.Mutex
	clients   map[string]*transportClientEntry // keyed by validator address string
}

// transportClientEntry holds a lazily-initialized [TransportClient].
type transportClientEntry struct {
	sync.Once
	clientCloser TransportClient
	err          error
}

// NewTransportClientCache creates a new [TransportClientCache] with the given [TransportClientFn].
// expectedSize is a hint for the initial map capacity.
func NewTransportClientCache(newClient TransportClientFn, expectedSize int) *TransportClientCache {
	return &TransportClientCache{
		newClient: newClient,
		clients:   make(map[string]*transportClientEntry, expectedSize),
	}
}

// GetClient returns a cached [TransportClient] for the validator, creating one if needed.
// Uses the constructor function provided to [NewTransportClientCache]. Only one dial per validator will occur.
func (cc *TransportClientCache) GetClient(ctx context.Context, val *core.Validator) (TransportClient, error) {
	addr := val.Address.String()

	cc.mu.Lock()
	entry, ok := cc.clients[addr]
	if !ok {
		entry = &transportClientEntry{}
		cc.clients[addr] = entry
	}
	cc.mu.Unlock()

	entry.Do(func() {
		client, err := cc.newClient(ctx, val)
		if err != nil {
			entry.err = err
			return
		}
		entry.clientCloser = client
	})

	return entry.clientCloser, entry.err
}

// Close closes all cached [TransportClient]s.
func (cc *TransportClientCache) Close() (err error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	for _, entry := range cc.clients {
		if entry.clientCloser != nil {
			err = errors.Join(err, entry.clientCloser.Close())
		}
	}
	cc.clients = make(map[string]*transportClientEntry)
	return err
}
