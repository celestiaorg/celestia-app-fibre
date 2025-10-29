package promise_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/celestia-app/v6/app/promise"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	fibretypes "github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/go-square/v3/share"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"
)

func TestBalanceDiffExpireReservations(t *testing.T) {
	diff := promise.NewBalanceDiff()

	signer := "celestia1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqzzdypm"
	hash := []byte{0x01, 0x02, 0x03}
	amount := sdkmath.NewInt(1_000)
	expiration := time.Now().Add(-time.Minute)

	diff.Add(signer, amount)
	diff.TrackReservation(hash, signer, amount, expiration)

	expired := diff.ExpireReservations(time.Now())
	require.Len(t, expired, 1)
	require.Equal(t, signer, expired[0].Signer)
	require.Equal(t, amount, expired[0].Amount)
	require.Empty(t, diff.Snapshot())
}

func TestBalanceDiffPersistence(t *testing.T) {
	diff := promise.NewBalanceDiff()
	signer := "celestia1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqzzdypm"
	hash := []byte{0xaa, 0x01}

	diff.Add(signer, sdkmath.NewInt(500))
	diff.TrackReservation(hash, signer, sdkmath.NewInt(500), time.Unix(1_700_000_000, 0).UTC())

	dir := t.TempDir()
	require.NoError(t, diff.SaveToDisk(dir))

	restored := promise.NewBalanceDiff()
	require.NoError(t, restored.LoadFromDisk(dir))

	snapshot := restored.Snapshot()
	require.Equal(t, sdkmath.NewInt(500), snapshot[signer])

	expired := restored.ExpireReservations(time.Unix(1_800_000_000, 0))
	require.Len(t, expired, 1)
	require.Equal(t, hash, expired[0].Hash)
}

func TestFileBankStoreRemove(t *testing.T) {
	dir := t.TempDir()
	bank := promise.NewFileBank(dir)

	pbPromise := makeTestPaymentPromise(t)
	require.NoError(t, bank.Store(pbPromise))

	hash, err := promise.HashPaymentPromise(pbPromise)
	require.NoError(t, err)

	filename := filepath.Join(dir, fmt.Sprintf("%x.bin", hash))
	stat, err := os.Stat(filename)
	require.NoError(t, err)
	require.False(t, stat.IsDir())

	require.NoError(t, bank.Remove(hash))
	_, err = os.Stat(filename)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func BenchmarkBalanceDiffAdd(b *testing.B) {
	diff := promise.NewBalanceDiff()
	amount := sdkmath.NewInt(1234)
	for n := 0; n < b.N; n++ {
		diff.Add(fmt.Sprintf("signer-%d", n%1024), amount)
	}
}

func makeTestPaymentPromise(t *testing.T) *fibretypes.PaymentPromise {
	t.Helper()

	privKey := secp256k1.GenPrivKey()
	ns := share.MustNewV0Namespace([]byte("testns"))

	pp := &fibre.PaymentPromise{
		ChainID:     "benchmark-chain",
		Height:      42,
		Namespace:   ns,
		BlobSize:    1024,
		BlobVersion: 0,
		Commitment: fibre.Commitment{
			1, 2, 3, 4, 5, 6, 7, 8,
			9, 10, 11, 12, 13, 14, 15, 16,
			17, 18, 19, 20, 21, 22, 23, 24,
			25, 26, 27, 28, 29, 30, 31, 32,
		},
		CreationTimestamp: time.Unix(1_700_000_000, 0).UTC(),
		SignerKey:         privKey.PubKey().(*secp256k1.PubKey),
	}

	signBytes, err := pp.SignBytes()
	require.NoError(t, err)
	signature, err := privKey.Sign(signBytes)
	require.NoError(t, err)
	pp.Signature = signature

	return pp.ToProto()
}
