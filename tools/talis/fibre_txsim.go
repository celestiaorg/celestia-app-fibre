package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/celestiaorg/celestia-app-fibre/v6/app"
	"github.com/celestiaorg/celestia-app-fibre/v6/app/encoding"
	"github.com/celestiaorg/celestia-app-fibre/v6/fibre"
	fibregrpc "github.com/celestiaorg/celestia-app-fibre/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app-fibre/v6/pkg/user"
	valaddrtypes "github.com/celestiaorg/celestia-app-fibre/v6/x/valaddr/types"
	"github.com/celestiaorg/go-square/v4/share"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func fibreTxsimCmd() *cobra.Command {
	var (
		rootDir      string
		grpcEndpoint string
		keyringDir   string
		keyName      string
		blobSize     int
		concurrency  int
		interval     time.Duration
		duration     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "fibre-txsim",
		Short: "Spam the network with fibre blob submissions",
		Long:  "Connects to a validator's gRPC endpoint and continuously submits fibre blobs using fibre.Client.Put().",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(rootDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			if len(cfg.Validators) == 0 {
				return fmt.Errorf("no validators found in config")
			}

			if grpcEndpoint == "" {
				grpcEndpoint = fmt.Sprintf("%s:9091", cfg.Validators[0].PublicIP)
			}
			if keyringDir == "" {
				keyringDir = filepath.Join(rootDir, "payload", cfg.Validators[0].Name)
			}

			fmt.Printf("gRPC endpoint: %s\n", grpcEndpoint)
			fmt.Printf("Keyring dir:   %s\n", keyringDir)
			fmt.Printf("Key name:      %s\n", keyName)
			fmt.Printf("Blob size:     %d bytes\n", blobSize)
			fmt.Printf("Concurrency:   %d\n", concurrency)
			fmt.Printf("Interval:      %s\n", interval)
			fmt.Printf("Duration:      %s\n", duration)

			encCfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)

			kr, err := keyring.New(app.Name, keyring.BackendTest, keyringDir, nil, encCfg.Codec)
			if err != nil {
				return fmt.Errorf("failed to initialize keyring: %w", err)
			}

			grpcConn, err := grpc.NewClient(
				grpcEndpoint,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultCallOptions(
					grpc.MaxCallSendMsgSize(math.MaxInt32),
					grpc.MaxCallRecvMsgSize(math.MaxInt32),
				),
			)
			if err != nil {
				return fmt.Errorf("failed to create gRPC connection: %w", err)
			}
			defer grpcConn.Close()

			valGet := fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(grpcConn))
			hostReg := fibregrpc.NewHostRegistry(valaddrtypes.NewQueryClient(grpcConn))

			clientCfg := fibre.DefaultClientConfig()
			clientCfg.ChainID = cfg.ChainID
			clientCfg.DefaultKeyName = keyName

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			txClient, err := user.SetupTxClient(ctx, kr, grpcConn, encCfg, user.WithDefaultAccount(keyName))
			if err != nil {
				return fmt.Errorf("failed to set up tx client: %w", err)
			}

			fibreClient, err := fibre.NewClient(txClient, kr, valGet, hostReg, clientCfg)
			if err != nil {
				return fmt.Errorf("failed to create fibre client: %w", err)
			}
			defer fibreClient.Close()

			// Handle Ctrl+C
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt)
			go func() {
				<-sigCh
				fmt.Println("\nReceived interrupt, shutting down...")
				cancel()
			}()

			// Apply duration limit if set
			if duration > 0 {
				ctx, cancel = context.WithTimeout(ctx, duration)
				defer cancel()
			}

			// Stats
			var (
				totalSent  atomic.Int64
				successes  atomic.Int64
				failures   atomic.Int64
				totalLatNs atomic.Int64
			)
			startTime := time.Now()

			// Semaphore for bounded concurrency
			sem := make(chan struct{}, concurrency)
			var wg sync.WaitGroup

			fmt.Println("\nStarting fibre blob spam...")

			// If interval is set, use a ticker to pace blob submissions.
			// Otherwise, fire as fast as the semaphore allows.
			var tick <-chan time.Time
			if interval > 0 {
				t := time.NewTicker(interval)
				defer t.Stop()
				tick = t.C
			}

			for ctx.Err() == nil {
				// Wait for the interval tick (if configured)
				if tick != nil {
					select {
					case <-ctx.Done():
						continue
					case <-tick:
					}
				}

				// Acquire semaphore slot
				select {
				case <-ctx.Done():
					continue
				case sem <- struct{}{}:
				}

				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-sem }()

					// Generate random namespace
					nsID := make([]byte, share.NamespaceVersionZeroIDSize)
					if _, err := rand.Read(nsID); err != nil {
						fmt.Printf("error generating namespace: %v\n", err)
						failures.Add(1)
						totalSent.Add(1)
						return
					}
					id := make([]byte, 0, share.NamespaceIDSize)
					id = append(id, share.NamespaceVersionZeroPrefix...)
					id = append(id, nsID...)
					ns, err := share.NewNamespace(share.NamespaceVersionZero, id)
					if err != nil {
						fmt.Printf("error creating namespace: %v\n", err)
						failures.Add(1)
						totalSent.Add(1)
						return
					}

					// Generate random blob data
					data := make([]byte, blobSize)
					if _, err := rand.Read(data); err != nil {
						fmt.Printf("error generating blob data: %v\n", err)
						failures.Add(1)
						totalSent.Add(1)
						return
					}

					t := time.Now()
					result, err := fibreClient.Put(ctx, ns, data)
					lat := time.Since(t)

					totalSent.Add(1)
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						failures.Add(1)
						fmt.Printf("error: %v (latency=%s)\n", err, lat)
						return
					}

					successes.Add(1)
					totalLatNs.Add(lat.Nanoseconds())
					fmt.Printf("height=%d tx=%s latency=%s\n", result.Height, result.TxHash, lat)
				}()
			}

			wg.Wait()

			elapsed := time.Since(startTime)
			s := successes.Load()
			f := failures.Load()
			var avgLat time.Duration
			if s > 0 {
				avgLat = time.Duration(totalLatNs.Load() / s)
			}

			fmt.Printf("\n--- Summary ---\n")
			fmt.Printf("Duration:   %s\n", elapsed.Truncate(time.Second))
			fmt.Printf("Total sent: %d\n", totalSent.Load())
			fmt.Printf("Successes:  %d\n", s)
			fmt.Printf("Failures:   %d\n", f)
			fmt.Printf("Avg latency (success): %s\n", avgLat)

			return nil
		},
	}

	cmd.Flags().StringVarP(&rootDir, "directory", "d", ".", "root directory in which to initialize")
	cmd.Flags().StringVar(&grpcEndpoint, "grpc-endpoint", "", "gRPC endpoint (default: first validator IP:9091)")
	cmd.Flags().StringVar(&keyringDir, "keyring-dir", "", "keyring directory (default: <rootDir>/payload/<first-validator>)")
	cmd.Flags().StringVar(&keyName, "key-name", "validator", "key name in keyring (must match the account that deposited to escrow)")
	cmd.Flags().IntVar(&blobSize, "blob-size", 1000000, "size of each blob in bytes")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "number of concurrent blob submissions")
	cmd.Flags().DurationVar(&interval, "interval", 0, "delay between blob submissions (0 = no delay, fire as fast as possible)")
	cmd.Flags().DurationVar(&duration, "duration", 0, "how long to run (0 = until Ctrl+C)")

	return cmd
}
