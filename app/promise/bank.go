package promise

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/celestiaorg/celestia-app/v6/fibre"
	fibretypes "github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/cosmos/gogoproto/proto"
)

const promiseStoreDir = "promise_store"

// Bank defines the persistence interface for storing payment promises externally.
type Bank interface {
	Store(pp *fibretypes.PaymentPromise) error
	Remove(hash []byte) error
}

// FileBank persists payment promises to disk under a directory.
type FileBank struct {
	root string
}

// NewFileBank constructs a FileBank rooted at dir.
func NewFileBank(root string) *FileBank {
	return &FileBank{root: root}
}

// Store writes the promise to disk named by its hash.
func (b *FileBank) Store(pp *fibretypes.PaymentPromise) error {
	if b == nil {
		return nil
	}
	if err := os.MkdirAll(b.root, 0o755); err != nil {
		return err
	}
	hash, err := HashPaymentPromise(pp)
	if err != nil {
		return err
	}
	filename := filepath.Join(b.root, fmt.Sprintf("%x.bin", hash))
	data, err := proto.Marshal(pp)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0o600)
}

// Remove deletes the persisted promise blob.
func (b *FileBank) Remove(hash []byte) error {
	if b == nil {
		return nil
	}
	filename := filepath.Join(b.root, fmt.Sprintf("%x.bin", hash))
	err := os.Remove(filename)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// HashPaymentPromise computes the hash of a PaymentPromise protobuf message.
func HashPaymentPromise(pp *fibretypes.PaymentPromise) ([]byte, error) {
	if pp == nil {
		return nil, errors.New("nil payment promise")
	}
	var promise fibre.PaymentPromise
	if err := promise.FromProto(pp); err != nil {
		return nil, err
	}
	return promise.Hash()
}

// PromiseStoreDir returns the directory name used for storing promises.
func PromiseStoreDir() string {
	return promiseStoreDir
}

// DiffFilename returns the filename used for the diff snapshot.
func DiffFilename() string {
	return diffFilename
}
