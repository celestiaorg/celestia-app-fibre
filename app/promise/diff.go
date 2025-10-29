package promise

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	sdkmath "cosmossdk.io/math"
	fibretypes "github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cosmos/gogoproto/proto"
)

const diffFilename = "promise_diff_snapshot.bin"

// ReservationMetadata tracks the signer, amount, and expiration for a promise reservation.
type ReservationMetadata struct {
	Signer    string
	Amount    sdkmath.Int
	ExpiresAt time.Time
}

// ReservationEntry contains the hash and metadata for a stored reservation.
type ReservationEntry struct {
	Hash []byte
	ReservationMetadata
}

// BalanceDiff maintains the per-signer aggregated reservations and reservation metadata.
type BalanceDiff struct {
	mu           sync.RWMutex
	entries      map[string]sdkmath.Int
	reservations map[string]ReservationMetadata
}

// NewBalanceDiff creates an empty BalanceDiff.
func NewBalanceDiff() *BalanceDiff {
	return &BalanceDiff{
		entries:      make(map[string]sdkmath.Int),
		reservations: make(map[string]ReservationMetadata),
	}
}

// Add increments the reserved balance for a signer.
func (d *BalanceDiff) Add(signer string, amount sdkmath.Int) {
	if amount.IsZero() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current := d.entries[signer]
	if current.IsNil() {
		current = sdkmath.ZeroInt()
	}
	d.entries[signer] = current.Add(amount)
}

// Sub decrements the reserved balance for a signer and prunes empty entries.
func (d *BalanceDiff) Sub(signer string, amount sdkmath.Int) {
	if amount.IsZero() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current := d.entries[signer]
	if current.IsNil() {
		current = sdkmath.ZeroInt()
	}
	updated := current.Sub(amount)
	if updated.IsNegative() {
		updated = sdkmath.ZeroInt()
	}
	if updated.IsZero() {
		delete(d.entries, signer)
		return
	}
	d.entries[signer] = updated
}

// Lookup returns the aggregated reservation amount for a signer.
func (d *BalanceDiff) Lookup(signer string) sdkmath.Int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.entries) == 0 {
		return sdkmath.ZeroInt()
	}
	return d.entries[signer]
}

// Snapshot returns a copy of the current aggregated reservations.
func (d *BalanceDiff) Snapshot() map[string]sdkmath.Int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.entries) == 0 {
		return nil
	}
	out := make(map[string]sdkmath.Int, len(d.entries))
	for signer, amount := range d.entries {
		out[signer] = amount
	}
	return out
}

// TrackReservation records metadata for a promise reservation.
func (d *BalanceDiff) TrackReservation(hash []byte, signer string, amount sdkmath.Int, expiresAt time.Time) {
	key := hexKey(hash)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reservations == nil {
		d.reservations = make(map[string]ReservationMetadata)
	}
	d.reservations[key] = ReservationMetadata{
		Signer:    signer,
		Amount:    amount,
		ExpiresAt: expiresAt.UTC(),
	}
}

// ReleaseReservation removes a tracked reservation by hash.
func (d *BalanceDiff) ReleaseReservation(hash []byte) (ReservationMetadata, bool) {
	key := hexKey(hash)
	d.mu.Lock()
	defer d.mu.Unlock()
	meta, ok := d.reservations[key]
	if ok {
		delete(d.reservations, key)
	}
	return meta, ok
}

// ExpireReservations removes reservations whose expiration is before or equal to now.
// It updates the aggregated balances and returns the expired entries.
func (d *BalanceDiff) ExpireReservations(now time.Time) []ReservationEntry {
	nowUTC := now.UTC()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.reservations) == 0 {
		return nil
	}
	expired := make([]ReservationEntry, 0)
	for key, meta := range d.reservations {
		if !meta.ExpiresAt.IsZero() && (nowUTC.After(meta.ExpiresAt) || nowUTC.Equal(meta.ExpiresAt)) {
			if amount, ok := d.entries[meta.Signer]; ok {
				updated := amount.Sub(meta.Amount)
				if updated.IsNegative() {
					updated = sdkmath.ZeroInt()
				}
				if updated.IsZero() {
					delete(d.entries, meta.Signer)
				} else {
					d.entries[meta.Signer] = updated
				}
			}
			delete(d.reservations, key)
			hash, err := hex.DecodeString(key)
			if err != nil {
				continue
			}
			expired = append(expired, ReservationEntry{
				Hash:                hash,
				ReservationMetadata: meta,
			})
		}
	}
	if len(expired) == 0 {
		return nil
	}
	return expired
}

// SaveToDisk persists the diff and reservation metadata under homeDir.
func (d *BalanceDiff) SaveToDisk(homeDir string) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	snapshot := &fibretypes.PromiseDiffSnapshot{
		Balances:     make([]*fibretypes.PromiseSignerBalance, 0, len(d.entries)),
		Reservations: make([]*fibretypes.PromiseReservation, 0, len(d.reservations)),
	}

	for signer, amount := range d.entries {
		snapshot.Balances = append(snapshot.Balances, &fibretypes.PromiseSignerBalance{
			Signer: signer,
			Amount: amount.String(),
		})
	}

	for key, meta := range d.reservations {
		hash, err := hex.DecodeString(key)
		if err != nil {
			return err
		}
		snapshot.Reservations = append(snapshot.Reservations, &fibretypes.PromiseReservation{
			Hash:          hash,
			Signer:        meta.Signer,
			Amount:        meta.Amount.String(),
			ExpiresAtUnix: meta.ExpiresAt.Unix(),
		})
	}

	bz, err := proto.Marshal(snapshot)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(homeDir, diffFilename)
	return os.WriteFile(path, bz, 0o600)
}

// LoadFromDisk restores the diff and reservations from disk.
func (d *BalanceDiff) LoadFromDisk(homeDir string) error {
	path := filepath.Join(homeDir, diffFilename)
	bz, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	var snapshot fibretypes.PromiseDiffSnapshot
	if err := proto.Unmarshal(bz, &snapshot); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.entries = make(map[string]sdkmath.Int, len(snapshot.Balances))
	for _, bal := range snapshot.Balances {
		amt, ok := sdkmath.NewIntFromString(bal.Amount)
		if !ok {
			return errors.New("invalid balance amount in snapshot")
		}
		d.entries[bal.Signer] = amt
	}

	d.reservations = make(map[string]ReservationMetadata, len(snapshot.Reservations))
	for _, res := range snapshot.Reservations {
		amt, ok := sdkmath.NewIntFromString(res.Amount)
		if !ok {
			return errors.New("invalid reservation amount in snapshot")
		}
		key := hex.EncodeToString(res.Hash)
		d.reservations[key] = ReservationMetadata{
			Signer:    res.Signer,
			Amount:    amt,
			ExpiresAt: time.Unix(res.ExpiresAtUnix, 0).UTC(),
		}
	}

	return nil
}

func hexKey(hash []byte) string {
	return hex.EncodeToString(hash)
}
