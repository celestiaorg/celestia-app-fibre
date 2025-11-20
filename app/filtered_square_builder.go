package app

import (
	"fmt"

	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	fibretypes "github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	square "github.com/celestiaorg/go-square/v3"
	"github.com/celestiaorg/go-square/v3/share"
	"github.com/celestiaorg/go-square/v3/tx"
	tmbytes "github.com/cometbft/cometbft/libs/bytes"
	coretypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// FilteredSquareBuilder filters txs and blobs using a copy of the state and tx validity
// rules before adding it the square.
type FilteredSquareBuilder struct {
	handler  sdk.AnteHandler
	txConfig client.TxConfig
	builder  *square.Builder
}

func NewFilteredSquareBuilder(
	handler sdk.AnteHandler,
	txConfig client.TxConfig,
	maxSquareSize,
	subtreeRootThreshold int,
) (*FilteredSquareBuilder, error) {
	builder, err := square.NewBuilder(maxSquareSize, subtreeRootThreshold)
	if err != nil {
		return nil, err
	}
	return &FilteredSquareBuilder{
		handler:  handler,
		txConfig: txConfig,
		builder:  builder,
	}, nil
}

func (fsb *FilteredSquareBuilder) Build() (square.Square, error) {
	return fsb.builder.Export()
}

func (fsb *FilteredSquareBuilder) Builder() *square.Builder {
	return fsb.builder
}

func (fsb *FilteredSquareBuilder) Fill(ctx sdk.Context, txs [][]byte) [][]byte {
	logger := ctx.Logger().With("app/filtered-square-builder")

	// note that there is an additional filter step for tx size of raw txs here
	normalTxs, blobTxs, payForFibreTxs := separateTxs(fsb.txConfig, txs)

	var (
		nonPFBMessageCount = 0
		pfbMessageCount    = 0
		dec                = fsb.txConfig.TxDecoder()
		n                  = 0
		m                  = 0
	)

	for _, tx := range normalTxs {
		sdkTx, err := dec(tx)
		if err != nil {
			logger.Error("decoding already checked transaction", "tx", tmbytes.HexBytes(coretypes.Tx(tx).Hash()), "error", err)
			continue
		}

		// Set the tx size on the context before calling the AnteHandler
		ctx = ctx.WithTxBytes(tx)

		msgTypes := msgTypes(sdkTx)
		if nonPFBMessageCount+len(sdkTx.GetMsgs()) > appconsts.MaxNonPFBMessages {
			logger.Debug("skipping tx because the max non PFB message count was reached", "tx", tmbytes.HexBytes(coretypes.Tx(tx).Hash()))
			continue
		}

		if !fsb.builder.AppendTx(tx) {
			logger.Debug("skipping tx because it was too large to fit in the square", "tx", tmbytes.HexBytes(coretypes.Tx(tx).Hash()))
			continue
		}

		ctx, err = fsb.handler(ctx, sdkTx, false)
		// either the transaction is invalid (ie incorrect nonce) and we
		// simply want to remove this tx, or we're catching a panic from one
		// of the anteHandlers which is logged.
		if err != nil {
			logger.Error(
				"filtering already checked transaction",
				"tx", tmbytes.HexBytes(coretypes.Tx(tx).Hash()),
				"error", err,
				"msgs", msgTypes,
			)
			telemetry.IncrCounter(1, "prepare_proposal", "invalid_std_txs")
			err = fsb.builder.RevertLastTx()
			if err != nil {
				logger.Error("reverting last transaction", "error", err)
			}
			continue
		}

		nonPFBMessageCount += len(sdkTx.GetMsgs())
		normalTxs[n] = tx
		n++
	}

	for _, tx := range blobTxs {
		sdkTx, err := dec(tx.Tx)
		if err != nil {
			logger.Error("decoding already checked blob transaction", "tx", tmbytes.HexBytes(coretypes.Tx(tx.Tx).Hash()), "error", err)
			continue
		}

		// Set the tx size on the context before calling the AnteHandler
		ctx = ctx.WithTxBytes(tx.Tx)

		if pfbMessageCount+len(sdkTx.GetMsgs()) > appconsts.MaxPFBMessages {
			logger.Debug("skipping blob tx because the max pfb message count was reached", "tx", tmbytes.HexBytes(coretypes.Tx(tx.Tx).Hash()))
			continue
		}

		if !fsb.builder.AppendBlobTx(tx) {
			logger.Debug("skipping tx because it was too large to fit in the square", "tx", tmbytes.HexBytes(coretypes.Tx(tx.Tx).Hash()))
			continue
		}

		ctx, err = fsb.handler(ctx, sdkTx, false)
		// either the transaction is invalid (ie incorrect nonce) and we
		// simply want to remove this tx, or we're catching a panic from one
		// of the anteHandlers which is logged.
		if err != nil {
			logger.Error(
				"filtering already checked blob transaction", "tx", tmbytes.HexBytes(coretypes.Tx(tx.Tx).Hash()), "error", err,
			)
			telemetry.IncrCounter(1, "prepare_proposal", "invalid_blob_txs")
			err = fsb.builder.RevertLastBlobTx()
			if err != nil {
				logger.Error("reverting last blob transaction failed", "error", err)
			}
			continue
		}

		pfbMessageCount += len(sdkTx.GetMsgs())
		blobTxs[m] = tx
		m++
	}

	payForFibreTxCount := 0

	for _, tx := range payForFibreTxs {
		sdkTx, err := dec(tx)
		if err != nil {
			logger.Error("decoding already checked pay-for-fibre transaction", "tx", tmbytes.HexBytes(coretypes.Tx(tx).Hash()), "error", err)
			continue
		}

		// Set the tx size on the context before calling the AnteHandler
		ctx = ctx.WithTxBytes(tx)

		msgTypes := msgTypes(sdkTx)

		// Append pay-for-fibre transaction to builder (will be routed to PayForFibreNamespace in Export)
		if !fsb.builder.AppendPayForFibreTx(tx) {
			logger.Debug("skipping pay-for-fibre tx because it was too large to fit in the square", "tx", tmbytes.HexBytes(coretypes.Tx(tx).Hash()))
			continue
		}

		ctx, err = fsb.handler(ctx, sdkTx, false)
		// either the transaction is invalid (ie incorrect nonce) and we
		// simply want to remove this tx, or we're catching a panic from one
		// of the anteHandlers which is logged.
		if err != nil {
			logger.Error(
				"filtering already checked pay-for-fibre transaction",
				"tx", tmbytes.HexBytes(coretypes.Tx(tx).Hash()),
				"error", err,
				"msgs", msgTypes,
			)
			telemetry.IncrCounter(1, "prepare_proposal", "invalid_pay_for_fibre_txs")
			err = fsb.builder.RevertLastPayForFibreTx()
			if err != nil {
				logger.Error("reverting last pay-for-fibre transaction", "error", err)
			}
			continue
		}

		// Generate and add system-level blob for this MsgPayForFibre transaction
		msgPayForFibre, hasPayForFibre := extractMsgPayForFibre(sdkTx)
		if hasPayForFibre {
			opts := PayForFibreOptions{
				StrictErrorHandling: false, // Continue on error, log and skip
				OnError: func(txHash []byte, err error, reason string) {
					logger.Error(
						reason,
						"tx", tmbytes.HexBytes(txHash),
						"error", err,
					)
					telemetry.IncrCounter(1, "prepare_proposal", "failed_system_blob_creation")
				},
				OnSkip: func(txHash []byte, reason string) {
					logger.Debug(reason, "tx", tmbytes.HexBytes(txHash))
				},
			}
			if err := addPayForFibreTxWithSystemBlob(fsb.builder, tx, msgPayForFibre, opts); err != nil {
				continue
			}
		}

		payForFibreTxs[payForFibreTxCount] = tx
		payForFibreTxCount++
	}

	kept := make([][]byte, 0, m+n+payForFibreTxCount)
	kept = append(kept, normalTxs[:n]...)
	kept = append(kept, encodeBlobTxs(blobTxs[:m])...)
	kept = append(kept, payForFibreTxs[:payForFibreTxCount]...)
	return kept
}

func msgTypes(sdkTx sdk.Tx) []string {
	msgs := sdkTx.GetMsgs()
	msgNames := make([]string, len(msgs))
	for i, msg := range msgs {
		msgNames[i] = sdk.MsgTypeURL(msg)
	}
	return msgNames
}

func encodeBlobTxs(blobTxs []*tx.BlobTx) [][]byte {
	txs := make([][]byte, len(blobTxs))
	var err error
	for i, blobTx := range blobTxs {
		txs[i], err = tx.MarshalBlobTx(blobTx.Tx, blobTx.Blobs...)
		if err != nil {
			panic(err)
		}
	}
	return txs
}

// separateTxs decodes raw tendermint txs into normal, blob, and pay-for-fibre txs.
// This function filters out transactions that exceed MaxTxSize. In process_proposal,
// transactions are already validated for size before this function is called, so
// the size check here is redundant but harmless.
func separateTxs(txConfig client.TxConfig, rawTxs [][]byte) (normalTxs [][]byte, blobTxs []*tx.BlobTx, payForFibreTxs [][]byte) {
	normalTxs = make([][]byte, 0, len(rawTxs))
	blobTxs = make([]*tx.BlobTx, 0, len(rawTxs))
	payForFibreTxs = make([][]byte, 0, len(rawTxs))
	dec := txConfig.TxDecoder()

	for _, rawTx := range rawTxs {
		// this check in theory shouldn't get hit, as txs should be filtered
		// in CheckTx. However in tests we're inserting too large of txs
		// therefore also filter here. In process_proposal, transactions are
		// already validated for size, so this check is redundant but harmless.
		if len(rawTx) > appconsts.MaxTxSize {
			continue
		}

		bTx, isBlob, err := tx.UnmarshalBlobTx(rawTx)
		if isBlob {
			if err != nil {
				panic(err)
			}
			blobTxs = append(blobTxs, bTx)
			continue
		}

		// Decode the transaction
		sdkTx, err := dec(rawTx)
		if err != nil {
			normalTxs = append(normalTxs, rawTx)
			continue
		}

		// Check if this is a pay-for-fibre transaction
		if _, hasPayForFibre := extractMsgPayForFibre(sdkTx); hasPayForFibre {
			payForFibreTxs = append(payForFibreTxs, rawTx)
			continue
		}
		// If it's not a pay-for-fibre transaction, add it to the normal transactions
		normalTxs = append(normalTxs, rawTx)
	}
	return normalTxs, blobTxs, payForFibreTxs
}

// extractMsgPayForFibre extracts MsgPayForFibre from a transaction's messages.
// Returns the first MsgPayForFibre found and true if found, nil and false otherwise.
func extractMsgPayForFibre(sdkTx sdk.Tx) (*fibretypes.MsgPayForFibre, bool) {
	msgs := sdkTx.GetMsgs()
	for _, msg := range msgs {
		if pff, ok := msg.(*fibretypes.MsgPayForFibre); ok {
			return pff, true
		}
	}
	return nil, false
}

// createSystemBlobForPayForFibre creates a system-level blob for a MsgPayForFibre message.
// The blob uses share version 2 and contains the Fibre blob version and commitment.
func createSystemBlobForPayForFibre(msg *fibretypes.MsgPayForFibre) (*share.Blob, error) {
	// Extract namespace from PaymentPromise
	namespaceBytes := msg.PaymentPromise.Namespace
	if len(namespaceBytes) != share.NamespaceSize {
		return nil, fmt.Errorf("invalid namespace size: expected %d bytes, got %d", share.NamespaceSize, len(namespaceBytes))
	}
	namespace, err := share.NewNamespaceFromBytes(namespaceBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace: %w", err)
	}

	// Convert signer from bech32 to 20-byte address
	signerAddr, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signer address: %w", err)
	}
	signerBytes := signerAddr.Bytes()
	if len(signerBytes) != share.SignerSize {
		return nil, fmt.Errorf("invalid signer size: expected %d bytes, got %d", share.SignerSize, len(signerBytes))
	}

	// Extract fibre_blob_version and commitment from PaymentPromise
	fibreBlobVersion := msg.PaymentPromise.BlobVersion
	commitment := msg.PaymentPromise.Commitment
	if len(commitment) != share.FibreCommitmentSize {
		return nil, fmt.Errorf("invalid commitment size: expected %d bytes, got %d", share.FibreCommitmentSize, len(commitment))
	}

	// Create V2 blob using NewV2Blob
	blob, err := share.NewV2Blob(namespace, fibreBlobVersion, commitment, signerBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create V2 blob: %w", err)
	}

	return blob, nil
}

// PayForFibreOptions configures how PayForFibre transactions are handled when adding to the builder.
type PayForFibreOptions struct {
	// StrictErrorHandling: if true, return error immediately on failure.
	// If false, log the error and continue to next transaction.
	// false in prepare_proposal, true in process_proposal.
	StrictErrorHandling bool
	// OnError is called when an error occurs (only used when StrictErrorHandling is false)
	OnError func(txHash []byte, err error, reason string)
	// OnSkip is called when a transaction is skipped (only used when StrictErrorHandling is false)
	OnSkip func(txHash []byte, reason string)
}

// buildSquareFromSeparatedTxs builds a square from already-separated transactions.
// This is a shared helper used by both prepare_proposal (via FilteredSquareBuilder) and process_proposal.
func buildSquareFromSeparatedTxs(
	normalTxs [][]byte,
	blobTxs []*tx.BlobTx,
	payForFibreTxs [][]byte,
	txConfig client.TxConfig,
	maxSquareSize, int,
	subtreeRootThreshold int,
	opts PayForFibreOptions,
) (square.Square, error) {
	// Create a square builder
	builder, err := square.NewBuilder(maxSquareSize, subtreeRootThreshold)
	if err != nil {
		return nil, fmt.Errorf("failed to create square builder: %w", err)
	}

	// Add normal transactions
	for _, tx := range normalTxs {
		if !builder.AppendTx(tx) {
			if opts.StrictErrorHandling {
				return nil, fmt.Errorf("not enough space to append normal tx")
			}
			// In non-strict mode (prepare_proposal), we skip transactions that don't fit
			// This shouldn't happen in practice since transactions are validated
			continue
		}
	}

	// Add blob transactions
	for _, blobTx := range blobTxs {
		if !builder.AppendBlobTx(blobTx) {
			if opts.StrictErrorHandling {
				return nil, fmt.Errorf("not enough space to append blob tx")
			}
			// In non-strict mode, skip transactions that don't fit
			continue
		}
	}

	// Add PayForFibre transactions and create system blobs
	if err := addPayForFibreTxsToBuilder(builder, payForFibreTxs, txConfig, opts); err != nil {
		return nil, err
	}

	// Export the square
	return builder.Export()
}

// addPayForFibreTxsToBuilder adds PayForFibre transactions and their system blobs to the builder.
func addPayForFibreTxsToBuilder(
	builder *square.Builder,
	payForFibreTxs [][]byte,
	txConfig client.TxConfig,
	opts PayForFibreOptions,
) error {
	decoder := txConfig.TxDecoder()

	for _, payForFibreTx := range payForFibreTxs {
		// Decode transaction to extract MsgPayForFibre
		sdkTx, err := decoder(payForFibreTx)
		if err != nil {
			if opts.StrictErrorHandling {
				return fmt.Errorf("failed to decode pay-for-fibre tx: %w", err)
			}
			if opts.OnError != nil {
				opts.OnError(coretypes.Tx(payForFibreTx).Hash(), err, "decoding pay-for-fibre transaction")
			}
			continue
		}

		// Append PayForFibre transaction
		if !builder.AppendPayForFibreTx(payForFibreTx) {
			if opts.StrictErrorHandling {
				return fmt.Errorf("not enough space to append pay-for-fibre tx")
			}
			if opts.OnSkip != nil {
				opts.OnSkip(coretypes.Tx(payForFibreTx).Hash(), "skipping pay-for-fibre tx because it was too large to fit in the square")
			}
			continue
		}

		// Extract MsgPayForFibre and create system blob
		msgPayForFibre, hasPayForFibre := extractMsgPayForFibre(sdkTx)
		if hasPayForFibre {
			if err := addPayForFibreTxWithSystemBlob(builder, payForFibreTx, msgPayForFibre, opts); err != nil {
				if opts.StrictErrorHandling {
					return err
				}
				continue
			}
		}
	}

	return nil
}

// addPayForFibreTxWithSystemBlob adds a single PayForFibre transaction and its system blob to the builder.
// The transaction should already be appended to the builder before calling this function.
func addPayForFibreTxWithSystemBlob(
	builder *square.Builder,
	tx []byte,
	msgPayForFibre *fibretypes.MsgPayForFibre,
	opts PayForFibreOptions,
) error {
	txHash := coretypes.Tx(tx).Hash()

	// Create system blob
	systemBlob, err := createSystemBlobForPayForFibre(msgPayForFibre)
	if err != nil {
		if opts.StrictErrorHandling {
			return fmt.Errorf("failed to create system blob for pay-for-fibre: %w", err)
		}
		if opts.OnError != nil {
			opts.OnError(txHash, err, "failed to create system blob for pay-for-fibre transaction")
		}
		// Revert the transaction that was already appended
		if revertErr := builder.RevertLastPayForFibreTx(); revertErr != nil && opts.OnError != nil {
			opts.OnError(txHash, revertErr, "reverting last pay-for-fibre transaction after system blob creation failure")
		}
		return err
	}

	// Add system blob to builder
	if !builder.AppendSystemBlob(systemBlob) {
		if opts.StrictErrorHandling {
			return fmt.Errorf("not enough space to append system blob")
		}
		if opts.OnSkip != nil {
			opts.OnSkip(txHash, "skipping pay-for-fibre tx because system blob was too large to fit in the square")
		}
		// Revert the transaction that was already appended
		if revertErr := builder.RevertLastPayForFibreTx(); revertErr != nil && opts.OnError != nil {
			opts.OnError(txHash, revertErr, "reverting last pay-for-fibre transaction after system blob addition failure")
		}
		return fmt.Errorf("system blob too large")
	}

	return nil
}
