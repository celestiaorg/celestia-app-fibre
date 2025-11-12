package app

import (
	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	fibretypes "github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	square "github.com/celestiaorg/go-square/v3"
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
func separateTxs(txConfig client.TxConfig, rawTxs [][]byte) (normalTxs [][]byte, blobTxs []*tx.BlobTx, payForFibreTxs [][]byte) {
	normalTxs = make([][]byte, 0, len(rawTxs))
	blobTxs = make([]*tx.BlobTx, 0, len(rawTxs))
	payForFibreTxs = make([][]byte, 0, len(rawTxs))
	dec := txConfig.TxDecoder()

	for _, rawTx := range rawTxs {
		// this check in theory shouldn't get hit, as txs should be filtered
		// in CheckTx. However in tests we're inserting too large of txs
		// therefore also filter here.
		if len(rawTx) > appconsts.MaxTxSize {
			continue
		}

		bTx, isBlob, err := tx.UnmarshalBlobTx(rawTx)
		if isBlob {
			if err != nil {
				panic(err)
			}
			blobTxs = append(blobTxs, bTx)
		} else {
			// Check if this is a pay-for-fibre transaction
			sdkTx, err := dec(rawTx)
			if err != nil {
				normalTxs = append(normalTxs, rawTx)
				continue
			}

			if _, hasPayForFibre := extractMsgPayForFibre(sdkTx); hasPayForFibre {
				payForFibreTxs = append(payForFibreTxs, rawTx)
			} else {
				normalTxs = append(normalTxs, rawTx)
			}
		}
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
