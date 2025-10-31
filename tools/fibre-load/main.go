package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/celestiaorg/celestia-app/v6/app"
	"github.com/celestiaorg/celestia-app/v6/app/encoding"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	"github.com/celestiaorg/celestia-app/v6/fibre/validator"
	grpcregistry "github.com/celestiaorg/celestia-app/v6/fibre/validator/grpc"
	"github.com/celestiaorg/celestia-app/v6/pkg/user"
	valaddrtypes "github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	"github.com/celestiaorg/go-square/v3/share"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultEndpoint     = "localhost:9090"
	defaultKeyringDir   = ".celestia-app"
	defaultInterval     = 1.0         // seconds between transactions
	defaultPayloadSize  = 1024 * 1024 // 1MB
	defaultNamespaceStr = "fibre"     // default namespace for blobs
	defaultKeyName      = "fibre-load-key"
)

func main() {
	var (
		endpoint     = flag.String("grpc-endpoint", defaultEndpoint, "gRPC endpoint of the consensus node")
		keyringDir   = flag.String("keyring-dir", "", "Directory containing the keyring (defaults to ~/.celestia-app)")
		interval     = flag.Float64("interval", defaultInterval, "Interval between transactions in seconds")
		payloadSize  = flag.Int("payload-size", defaultPayloadSize, "Size of payload data in bytes")
		namespaceStr = flag.String("namespace", defaultNamespaceStr, "Namespace for blob submission")
	)
	flag.Parse()

	// Set default keyring directory if not specified
	if *keyringDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get home directory: %v\n", err)
			os.Exit(1)
		}
		*keyringDir = filepath.Join(homeDir, defaultKeyringDir)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		fmt.Println("\nShutting down...")
		cancel()
	}()

	if err := runLoad(ctx, *endpoint, *keyringDir, *interval, *payloadSize, *namespaceStr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runLoad(
	ctx context.Context,
	endpoint string,
	keyringDir string,
	interval float64,
	payloadSize int,
	namespaceStr string,
) error {
	fmt.Printf("Fibre Load Generator\n")
	fmt.Printf("====================\n")
	fmt.Printf("gRPC Endpoint: %s\n", endpoint)
	fmt.Printf("Keyring Directory: %s\n", keyringDir)
	fmt.Printf("Interval: %.2f seconds\n", interval)
	fmt.Printf("Payload Size: %d bytes\n", payloadSize)
	fmt.Printf("Namespace: %s\n\n", namespaceStr)

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

	hostRegistry := grpcregistry.NewHostRegistry(valaddrtypes.NewQueryClient(grpcConn))
	valGet := validator.NewGrpcGetter(coregrpc.NewBlockAPIClient(grpcConn))

	fibreClient, err := fibre.NewClient(txClient, kr, valGet, hostRegistry, fibre.DefaultClientConfig())
	if err != nil {
		return fmt.Errorf("failed to create fibre client: %w", err)
	}

	fmt.Println("Starting load generation...")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	// Create ticker with the specified interval
	tickerDuration := time.Duration(interval * float64(time.Second))
	ticker := time.NewTicker(tickerDuration)
	defer ticker.Stop()

	var txCount uint64

	// Main load generation loop
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\nTotal transactions submitted: %d\n", txCount)
			return nil
		case <-ticker.C:
			txCount++
			go func(count uint64) {
				// Create random blob data
				blobData := make([]byte, payloadSize)
				if _, err := rand.Read(blobData); err != nil {
					fmt.Printf("[%d] Failed to generate random data: %v\n", count, err)
					return
				}

				// Submit transaction
				resp, err := fibreClient.Put(ctx, namespace, blobData)
				if err != nil {
					fmt.Printf("[%d] Failed to submit tx: %v\n", count, err)
					return
				}

				fmt.Printf("[%d] Transaction %s confirmed at height %d\n", count, resp.TxHash, resp.Height)
			}(txCount)
		}
	}
}

// setupKeyring initializes the keyring and automatically selects the best key.
// It finds the key with the most funds, if none have funds it picks the first key,
// if no keys are present it generates a new key.
// Returns the keyring, key name, and address.
func setupKeyring(ctx context.Context, keyringDir string, encCfg encoding.Config, grpcConn *grpc.ClientConn) (keyring.Keyring, string, string, error) {
	// Ensure keyring directory exists
	if err := os.MkdirAll(keyringDir, 0700); err != nil {
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
