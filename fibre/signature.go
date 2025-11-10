package fibre

import (
	"github.com/cometbft/cometbft/types"
)

// ValidatorSignatureDomain is the unique identifier used when requesting validator signatures
// via CometBFT's PrivValidator.SignRawBytes. Keeping this constant in sync between signing
// (validator RPC) and verification (client/state machine) ensures both sides agree on the exact
// bytes that were signed.
const ValidatorSignatureDomain = "fibre-v1"

// ValidatorSignatureSignBytes returns the byte slice that validators actually sign for a given
// Fibre PaymentPromise. It wraps the raw sign bytes with CometBFT's RawBytesMessageSignBytes
// format, including the domain separator and metadata required by the remote signer.
func ValidatorSignatureSignBytes(chainID string, raw []byte) ([]byte, error) {
	return types.RawBytesMessageSignBytes(chainID, ValidatorSignatureDomain, raw)
}
