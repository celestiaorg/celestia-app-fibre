package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/celestia-app/v6/app"
	"github.com/celestiaorg/celestia-app/v6/app/encoding"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	"github.com/celestiaorg/celestia-app/v6/pkg/user"
	"github.com/celestiaorg/celestia-app/v6/x/fibre/types"
	"github.com/celestiaorg/go-square/v3/share"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	core "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultEndpoint     = "localhost:9091"
	defaultKeyringDir   = ".celestia-app"
	defaultInterval     = time.Second       // interval between transactions
	defaultPayloadSize  = 128 * 1024 * 1024 // 128MiB
	defaultNamespaceStr = "fibre"           // default namespace for blobs
	defaultKeyName      = "fibre-load-key"
	defaultChainID      = "celestia" // default chain ID
	defaultTracesDir    = ".celestia-app/data/traces"
)

var (
	endpoint          string
	keyringDir        string
	interval          time.Duration
	payloadSize       int
	maxConcurrency    int
	namespaceStr      string
	validatorHostFile string
	chainID           string
	tracesDir         string
)

var rootCmd = &cobra.Command{
	Use:   "fibre-load",
	Short: "A load testing tool for Celestia Fibre",
	Long: `fibre-load is a load testing tool that generates continuous blob transactions
for Celestia Fibre network. It automatically manages keys and balances,
and submits transactions at a configurable rate.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Set default keyring directory if not specified
		if keyringDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			keyringDir = filepath.Join(homeDir, defaultKeyringDir)
		}

		// Create cancellable context
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// Handle interrupt signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)
		go func() {
			<-sigChan
			fmt.Println("\nShutting down...")
			cancel()
		}()

		return runLoad(ctx, endpoint, keyringDir, interval, payloadSize, namespaceStr, validatorHostFile, chainID, tracesDir)
	},
}

func init() {
	rootCmd.Flags().StringVarP(&endpoint, "grpc-endpoint", "e", defaultEndpoint, "gRPC endpoint of the consensus node")
	rootCmd.Flags().StringVarP(&keyringDir, "keyring-dir", "k", "", "directory containing the keyring (defaults to ~/.celestia-app)")
	rootCmd.Flags().DurationVarP(&interval, "interval", "i", defaultInterval, "interval between transactions (e.g. 500ms, 2s)")
	rootCmd.Flags().IntVarP(&payloadSize, "payload-size", "s", defaultPayloadSize, "size of payload data in bytes")
	rootCmd.Flags().IntVarP(&maxConcurrency, "max-concurrency", "m", 1, "maximum number of concurrent transactions in flight")
	rootCmd.Flags().StringVarP(&namespaceStr, "namespace", "n", defaultNamespaceStr, "namespace for blob submission")
	rootCmd.Flags().StringVarP(&validatorHostFile, "validator-hosts", "v", "", "path to JSON file containing validator address to host mapping (required)")
	rootCmd.Flags().StringVarP(&chainID, "chain-id", "c", defaultChainID, "chain ID for the network (can also be set via CHAIN_ID env var)")
	rootCmd.Flags().StringVarP(&tracesDir, "traces-dir", "t", "", "directory to write metrics traces (defaults to ~/.celestia-app/data/traces)")
	rootCmd.MarkFlagRequired("validator-hosts")

	// Support CHAIN_ID environment variable - check after flags are parsed
	if envChainID := os.Getenv("CHAIN_ID"); envChainID != "" && chainID == "" {
		chainID = envChainID
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runLoad(
	ctx context.Context,
	endpoint string,
	keyringDir string,
	interval time.Duration,
	payloadSize int,
	namespaceStr string,
	validatorHostFile string,
	chainID string,
	tracesDir string,
) error {
	// Set default traces directory if not specified
	if tracesDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		tracesDir = filepath.Join(homeDir, defaultTracesDir)
	}

	fmt.Printf("Fibre Load Generator\n")
	fmt.Printf("====================\n")
	fmt.Printf("gRPC Endpoint: %s\n", endpoint)
	fmt.Printf("Keyring Directory: %s\n", keyringDir)
	fmt.Printf("Chain ID: %s\n", chainID)
	fmt.Printf("Interval: %s\n", interval)
	fmt.Printf("Payload Size: %d bytes\n", payloadSize)
	fmt.Printf("Namespace: %s\n", namespaceStr)
	fmt.Printf("Traces Directory: %s\n\n", tracesDir)

	// Set up OpenTelemetry tracing if OTEL_TRACING_ADDRESS is set
	if otelAddr := os.Getenv("OTEL_TRACING_ADDRESS"); otelAddr != "" {
		fmt.Printf("Setting up OpenTelemetry tracing to %s\n", otelAddr)
		if err := setupTracing(ctx, otelAddr); err != nil {
			fmt.Printf("Warning: failed to setup tracing: %v\n", err)
		} else {
			fmt.Println("OpenTelemetry tracing configured successfully\n")
		}
	}

	encCfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)

	// Parse namespace from string
	namespace, err := share.NewV0Namespace([]byte(namespaceStr))
	if err != nil {
		return fmt.Errorf("failed to parse namespace: %w", err)
	}

	// Create gRPC connection first (needed for balance queries)
	grpcConn, err := grpc.NewClient(
		endpoint,
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

	// Initialize or create keyring
	kr, keyName, address, err := setupKeyring(ctx, keyringDir, encCfg, grpcConn)
	if err != nil {
		return fmt.Errorf("failed to setup keyring: %w", err)
	}

	fmt.Printf("Using key: %s\n", keyName)
	fmt.Printf("Using address: %s\n\n", address)

	// Initialize tx client
	fmt.Println("Setting up fibre client...")
	txClient, err := user.SetupTxClient(ctx, kr, grpcConn, encCfg)
	if err != nil {
		return fmt.Errorf("failed to create tx client: %w", err)
	}

	// Load validator host mapping from file
	validatorHosts, err := loadValidatorHosts(validatorHostFile)
	if err != nil {
		return fmt.Errorf("failed to load validator hosts: %w", err)
	}

	hostRegistry := newStaticHostRegistry(validatorHosts)
	valGet := fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(grpcConn))

	// Configure fibre client with the selected key and chain ID
	fibreCfg := fibre.DefaultClientConfig()
	fibreCfg.DefaultKeyName = keyName
	fibreCfg.ChainID = chainID

	fibreClient, err := fibre.NewClient(txClient, kr, valGet, hostRegistry, fibreCfg)
	if err != nil {
		return fmt.Errorf("failed to create fibre client: %w", err)
	}

	// Fund escrow account upfront with enough for many transactions
	// Estimate: 10_000_000 transactions worth of escrow funding
	if err := fundEscrowUpfront(ctx, txClient, payloadSize, 10000000); err != nil {
		return fmt.Errorf("failed to fund escrow: %w", err)
	}

	// Set up metrics writer
	metricsWriter, err := newMetricsWriter(tracesDir)
	if err != nil {
		return fmt.Errorf("failed to create metrics writer: %w", err)
	}
	defer metricsWriter.Close()

	fmt.Println("Starting load generation...")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	if maxConcurrency <= 0 {
		return fmt.Errorf("max-concurrency must be greater than zero")
	}
	if interval <= 0 {
		return fmt.Errorf("interval must be greater than zero")
	}

	// Create ticker with the specified interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var (
		txCount atomic.Uint64
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxConcurrency)
	)

	defer func() {
		wg.Wait()
		fmt.Printf("\nTotal transactions submitted: %d\n", txCount.Load())
	}()

	startTx := func(count uint64) {
		wg.Add(1)
		go func(txNum uint64) {
			defer wg.Done()
			defer func() { <-sem }()

			startTime := time.Now()
			blobData := make([]byte, payloadSize)
			if _, err := rand.Read(blobData); err != nil {
				fmt.Printf("[%d] Failed to generate random data: %v\n", txNum, err)
				metricsWriter.WriteMetric(TxMetric{
					TxNum:       txNum,
					StartTime:   startTime,
					EndTime:     time.Now(),
					Success:     false,
					Error:       err.Error(),
					PayloadSize: payloadSize,
				})
				return
			}

			resp, err := fibreClient.Put(ctx, namespace, blobData)
			endTime := time.Now()

			if err != nil {
				fmt.Printf("[%d] Failed to submit tx: %v\n", txNum, err)
				metricsWriter.WriteMetric(TxMetric{
					TxNum:       txNum,
					StartTime:   startTime,
					EndTime:     endTime,
					Success:     false,
					Error:       err.Error(),
					PayloadSize: payloadSize,
				})
				return
			}

			latency := endTime.Sub(startTime)
			fmt.Printf("[%d] Transaction %s confirmed at height %d (latency: %v)\n", txNum, resp.TxHash, resp.Height, latency)

			metricsWriter.WriteMetric(TxMetric{
				TxNum:       txNum,
				StartTime:   startTime,
				EndTime:     endTime,
				Success:     true,
				TxHash:      resp.TxHash,
				Height:      resp.Height,
				PayloadSize: payloadSize,
				LatencyMs:   latency.Milliseconds(),
			})
		}(count)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			select {
			case sem <- struct{}{}:
				count := txCount.Add(1)
				startTx(count)
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// setupKeyring initializes the keyring and automatically selects the best key.
// It finds the key with the most funds, if none have funds it picks the first key,
// if no keys are present it generates a new key.
// Returns the keyring, key name, and address.
func setupKeyring(ctx context.Context, keyringDir string, encCfg encoding.Config, grpcConn *grpc.ClientConn) (keyring.Keyring, string, string, error) {
	// Ensure keyring directory exists
	if err := os.MkdirAll(keyringDir, 0o700); err != nil {
		return nil, "", "", fmt.Errorf("failed to create keyring directory: %w", err)
	}

	// Initialize keyring
	kr, err := keyring.New(app.Name, keyring.BackendTest, keyringDir, nil, encCfg.Codec)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to initialize keyring: %w", err)
	}

	// List all keys in keyring
	keys, err := kr.List()
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to list keys: %w", err)
	}

	var selectedKey *keyring.Record
	var selectedKeyName string

	// If no keys exist, create a new one
	if len(keys) == 0 {
		fmt.Println("No keys found in keyring, creating new key...")
		keyName := defaultKeyName

		keyInfo, _, err := kr.NewMnemonic(
			keyName,
			keyring.English,
			sdk.GetConfig().GetFullBIP44Path(),
			"", // empty passphrase for test keyring
			hd.Secp256k1,
		)
		if err != nil {
			return nil, "", "", fmt.Errorf("failed to create new key: %w", err)
		}
		fmt.Printf("Created new key: %s\n", keyName)
		selectedKey = keyInfo
		selectedKeyName = keyName
	} else {
		// Query balances for all keys and find the one with most funds
		fmt.Printf("Found %d key(s) in keyring, checking balances...\n", len(keys))

		bankClient := banktypes.NewQueryClient(grpcConn)
		maxBalance := big.NewInt(0)
		var keyWithMaxBalance *keyring.Record
		var keyWithMaxBalanceName string

		for _, key := range keys {
			keyName := key.Name
			address, err := key.GetAddress()
			if err != nil {
				fmt.Printf("Warning: failed to get address for key '%s': %v\n", keyName, err)
				continue
			}

			// Query balance
			balanceResp, err := bankClient.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{
				Address: address.String(),
			})
			if err != nil {
				fmt.Printf("Warning: failed to query balance for key '%s': %v\n", keyName, err)
				continue
			}

			// Calculate total balance (sum all denoms in utia equivalent)
			totalBalance := big.NewInt(0)
			for _, coin := range balanceResp.Balances {
				if coin.Amount.IsPositive() {
					totalBalance.Add(totalBalance, coin.Amount.BigInt())
				}
			}

			fmt.Printf("  Key '%s' (%s): %s utia\n", keyName, address.String(), totalBalance.String())

			// Track key with maximum balance
			if totalBalance.Cmp(maxBalance) > 0 {
				maxBalance = totalBalance
				keyWithMaxBalance = key
				keyWithMaxBalanceName = keyName
			}
		}

		// If a key with funds was found, use it
		if maxBalance.Cmp(big.NewInt(0)) > 0 {
			fmt.Printf("Selected key with most funds: %s\n", keyWithMaxBalanceName)
			selectedKey = keyWithMaxBalance
			selectedKeyName = keyWithMaxBalanceName
		} else {
			// No keys have funds, use the first key
			fmt.Println("No keys have funds, using first key in keyring")
			selectedKey = keys[0]
			selectedKeyName = keys[0].Name
		}
	}

	// Get address of selected key
	address, err := selectedKey.GetAddress()
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to get address from key: %w", err)
	}

	return kr, selectedKeyName, address.String(), nil
}

// loadValidatorHosts loads the validator address to host mapping from a JSON file.
// The JSON file should contain a map of validator consensus addresses (hex) to host addresses.
// Example format: {"39DC747611536ABCC734D861C3135DA54D490AAA": "localhost:50051"}
func loadValidatorHosts(filePath string) (map[string]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var hosts map[string]string
	if err := json.Unmarshal(data, &hosts); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("validator hosts file is empty")
	}

	return hosts, nil
}

// staticHostRegistry is a simple implementation of validator.HostRegistry
// that uses a hardcoded map of validator addresses to hosts.
type staticHostRegistry struct {
	hosts map[string]string
}

// newStaticHostRegistry creates a new static host registry with the given address-to-host mapping.
func newStaticHostRegistry(hosts map[string]string) *staticHostRegistry {
	return &staticHostRegistry{
		hosts: hosts,
	}
}

// GetHost returns the host for a given validator from the static map.
func (r *staticHostRegistry) GetHost(ctx context.Context, val *core.Validator) (validator.Host, error) {
	addr := val.Address.String()
	host, ok := r.hosts[addr]
	if !ok {
		return "", fmt.Errorf("no host configured for validator %s", addr)
	}
	return validator.Host(host), nil
}

// fundEscrowUpfront deposits funds to the escrow account upfront for multiple transactions.
// This prevents the need to check and fund escrow on every Put operation.
func fundEscrowUpfront(ctx context.Context, txClient *user.TxClient, payloadSize int, numTxs int) error {
	// Query fibre params to get gas per byte
	grpcConn := txClient.GRPCConn()
	queryClient := types.NewQueryClient(grpcConn)

	paramsResp, err := queryClient.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		return fmt.Errorf("querying fibre params: %w", err)
	}

	// Calculate required amount for numTxs transactions
	gasPerByte := paramsResp.Params.GasPerBlobByte
	totalGas := uint64(payloadSize) * uint64(gasPerByte) * uint64(numTxs)

	denom := "utia"
	amount := sdk.NewCoin(denom, sdkmath.NewIntFromUint64(totalGas))

	fmt.Printf("Funding escrow account with %s (enough for ~%d transactions)...\n", amount.String(), numTxs)

	signer := txClient.DefaultAddress().String()
	msg := &types.MsgDepositToEscrow{
		Signer: signer,
		Amount: amount,
	}

	txResp, err := txClient.BroadcastTx(ctx, []sdk.Msg{msg})
	if err != nil {
		return fmt.Errorf("broadcasting deposit transaction: %w", err)
	}

	if _, err := txClient.ConfirmTx(ctx, txResp.TxHash); err != nil {
		return fmt.Errorf("confirming deposit transaction: %w", err)
	}

	fmt.Printf("Escrow account funded successfully (tx: %s)\n\n", txResp.TxHash)
	return nil
}

// setupTracing configures OpenTelemetry tracing with OTLP HTTP exporter.
// The endpoint should be in the format "host:port" (e.g., "localhost:4318").
func setupTracing(ctx context.Context, endpoint string) error {
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(), // Use insecure connection for simplicity
	)
	if err != nil {
		return fmt.Errorf("creating OTLP exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)

	return nil
}

// TxMetric represents metrics for a single transaction.
type TxMetric struct {
	TxNum       uint64    `json:"tx_num"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Success     bool      `json:"success"`
	TxHash      string    `json:"tx_hash,omitempty"`
	Height      uint64    `json:"height,omitempty"`
	Error       string    `json:"error,omitempty"`
	PayloadSize int       `json:"payload_size"`
	LatencyMs   int64     `json:"latency_ms,omitempty"`
}

// metricsWriter handles writing transaction metrics to a file.
type metricsWriter struct {
	file   *os.File
	writer *bufio.Writer
	mu     sync.Mutex
}

// newMetricsWriter creates a new metrics writer that writes to a timestamped file
// in the specified traces directory.
func newMetricsWriter(tracesDir string) (*metricsWriter, error) {
	// Ensure traces directory exists
	if err := os.MkdirAll(tracesDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create traces directory: %w", err)
	}

	// Create filename
	filename := filepath.Join(tracesDir, "fibre-load-metrics.jsonl")

	// Open file for writing
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics file: %w", err)
	}

	fmt.Printf("Writing metrics to: %s\n\n", filename)

	return &metricsWriter{
		file:   file,
		writer: bufio.NewWriter(file),
	}, nil
}

// WriteMetric writes a transaction metric to the file as a JSON line.
func (mw *metricsWriter) WriteMetric(metric TxMetric) error {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	data, err := json.Marshal(metric)
	if err != nil {
		return fmt.Errorf("failed to marshal metric: %w", err)
	}

	if _, err := mw.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write metric: %w", err)
	}

	if err := mw.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	// Flush periodically to ensure data is written
	return mw.writer.Flush()
}

// Close flushes and closes the metrics file.
func (mw *metricsWriter) Close() error {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	if err := mw.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush metrics: %w", err)
	}

	if err := mw.file.Close(); err != nil {
		return fmt.Errorf("failed to close metrics file: %w", err)
	}

	return nil
}
