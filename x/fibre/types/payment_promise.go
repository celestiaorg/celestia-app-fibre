package types

import (
	"encoding/binary"
	fmt "fmt"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// SignBytes constructs the sign bytes for a payment promise according to the
// sdk_module spec. Format: chain_id || namespace || blob_size || commitment ||
// row_version || height || creation_timestamp || signer_public_key
func (p PaymentPromise) SignBytes() (signBytes []byte, err error) {
	signBytes = append(signBytes, []byte(p.ChainId)...)
	signBytes = append(signBytes, p.Namespace...)

	blobSizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(blobSizeBytes, p.BlobSize)
	signBytes = append(signBytes, blobSizeBytes...)

	signBytes = append(signBytes, p.Commitment...)

	rowVersionBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(rowVersionBytes, p.RowVersion)
	signBytes = append(signBytes, rowVersionBytes...)

	heightBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(heightBytes, uint64(p.Height))
	signBytes = append(signBytes, heightBytes...)

	timestampBytes, err := p.CreationTimestamp.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal timestamp: %v", err)
	}
	signBytes = append(signBytes, timestampBytes...)

	if p.SignerPublicKey == nil {
		return nil, fmt.Errorf("signer public key cannot be nil")
	}

	pubKey, ok := p.SignerPublicKey.GetCachedValue().(cryptotypes.PubKey)
	if ok && pubKey != nil {
		// Get the 20-byte address from the public key
		signerAddr := sdk.AccAddress(pubKey.Address())
		signBytes = append(signBytes, signerAddr.Bytes()...)
	}

	return signBytes, nil
}
