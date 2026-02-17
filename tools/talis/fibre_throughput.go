package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/celestiaorg/celestia-app-fibre/v6/app"
	"github.com/celestiaorg/celestia-app-fibre/v6/app/encoding"
	fibretypes "github.com/celestiaorg/celestia-app-fibre/v6/x/fibre/types"
	"github.com/cometbft/cometbft/rpc/client/http"
	"github.com/spf13/cobra"
)

func fibreThroughputCmd() *cobra.Command {
	var (
		rootDir     string
		rpcEndpoint string
		duration    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "fibre-throughput",
		Short: "Monitor real-time fibre throughput per block",
		Long:  "Polls blocks from a validator's RPC endpoint, decodes MsgPayForFibre transactions, and prints throughput per block.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			if len(cfg.Validators) == 0 {
				return fmt.Errorf("no validators found in config")
			}

			if rpcEndpoint == "" {
				rpcEndpoint = fmt.Sprintf("http://%s:26657", cfg.Validators[0].PublicIP)
			}

			fmt.Printf("RPC endpoint: %s\n", rpcEndpoint)

			client, err := http.New(rpcEndpoint, "/websocket")
			if err != nil {
				return fmt.Errorf("failed to create RPC client: %w", err)
			}

			encCfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)
			txDecoder := encCfg.TxConfig.TxDecoder()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt)
			go func() {
				<-sigCh
				fmt.Println("\nReceived interrupt, shutting down...")
				cancel()
			}()

			if duration > 0 {
				ctx, cancel = context.WithTimeout(ctx, duration)
				defer cancel()
			}

			// Get the current latest height to start from
			statusResp, err := client.Status(ctx)
			if err != nil {
				return fmt.Errorf("failed to get status: %w", err)
			}
			nextHeight := statusResp.SyncInfo.LatestBlockHeight + 1
			fmt.Printf("Starting from height %d\n\n", nextHeight)

			var (
				totalBlocks     int64
				totalBytes      int64
				prevBlockTime   time.Time
				totalThroughput float64
			)

			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			for ctx.Err() == nil {
				select {
				case <-ctx.Done():
					continue
				case <-ticker.C:
				}

				// Fetch the latest height
				st, err := client.Status(ctx)
				if err != nil {
					if ctx.Err() != nil {
						continue
					}
					fmt.Printf("error fetching status: %v\n", err)
					continue
				}
				latestHeight := st.SyncInfo.LatestBlockHeight

				// Process all new blocks
				for h := nextHeight; h <= latestHeight && ctx.Err() == nil; h++ {
					height := h
					block, err := client.Block(ctx, &height)
					if err != nil {
						if ctx.Err() != nil {
							break
						}
						fmt.Printf("error fetching block %d: %v\n", h, err)
						continue
					}

					blockTime := block.Block.Time
					var blockTimeDelta float64
					if !prevBlockTime.IsZero() {
						blockTimeDelta = blockTime.Sub(prevBlockTime).Seconds()
					}
					prevBlockTime = blockTime

					var fibreTxCount int
					var blobBytes int64
					for _, rawTx := range block.Block.Txs {
						sdkTx, err := txDecoder(rawTx)
						if err != nil {
							continue
						}
						for _, msg := range sdkTx.GetMsgs() {
							if pff, ok := msg.(*fibretypes.MsgPayForFibre); ok {
								fibreTxCount++
								blobBytes += int64(pff.PaymentPromise.BlobSize)
							}
						}
					}

					var throughputMBs float64
					if blockTimeDelta > 0 {
						throughputMBs = float64(blobBytes) / blockTimeDelta / (1024 * 1024)
					}

					fmt.Printf("height=%d txs=%d blob_bytes=%d block_time=%.2fs throughput=%.2f MB/s\n",
						h, fibreTxCount, blobBytes, blockTimeDelta, throughputMBs)

					totalBlocks++
					totalBytes += blobBytes
					if blockTimeDelta > 0 {
						totalThroughput += throughputMBs
					}

					nextHeight = h + 1
				}
			}

			fmt.Printf("\n--- Summary ---\n")
			fmt.Printf("Total blocks:  %d\n", totalBlocks)
			fmt.Printf("Total bytes:   %d\n", totalBytes)
			if totalBlocks > 0 {
				fmt.Printf("Avg throughput: %.2f MB/s\n", totalThroughput/float64(totalBlocks))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory in which to initialize")
	cmd.Flags().StringVar(&rpcEndpoint, "rpc-endpoint", "", "CometBFT RPC endpoint (default: first validator IP:26657)")
	cmd.Flags().DurationVar(&duration, "duration", 0, "how long to run (0 = until Ctrl+C)")

	return cmd
}
