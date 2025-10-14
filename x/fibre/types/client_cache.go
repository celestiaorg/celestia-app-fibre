package types

import (
	"context"
	"errors"
	"sync"

	core "github.com/cometbft/cometbft/types"
)

// ClientCache caches [FibreClientCloser]s per validator using the provided constructor function.
// TODO(@Wondertan): Needs cleanup strategy, e.g. LRU
type ClientCache struct {
	newClient FibreClientCloserFn
	mu        sync.Mutex
	clients   map[string]*clientEntry // keyed by validator address string
}

// clientEntry holds a lazily-initialized [FibreClientCloser].
type clientEntry struct {
	sync.Once
	clientCloser FibreClientCloser
	err          error
}

// NewClientCache creates a new [ClientCache] with the given [FibreClientCloserFn].
func NewClientCache(newClient FibreClientCloserFn) *ClientCache {
	return &ClientCache{
		newClient: newClient,
		clients:   make(map[string]*clientEntry),
	}
}

// GetClient returns a cached [FibreClient] for the validator, creating one if needed.
// Uses the constructor function provided to [NewClientCache]. Only one dial per validator will occur.
func (cc *ClientCache) GetClient(ctx context.Context, val *core.Validator) (FibreClient, error) {
	addr := val.Address.String()

	cc.mu.Lock()
	entry, ok := cc.clients[addr]
	if !ok {
		entry = &clientEntry{}
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

// Close closes all cached [FibreClientCloser]s.
func (cc *ClientCache) Close() (err error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	for _, entry := range cc.clients {
		if entry.clientCloser != nil {
			err = errors.Join(err, entry.clientCloser.Close())
		}
	}
	cc.clients = make(map[string]*clientEntry)
	return err
}
