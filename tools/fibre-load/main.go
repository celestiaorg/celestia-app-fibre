package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
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
	defaultChainID      = "celestia" // default chain ID
)

var (
	endpoint          string
	keyringDir        string
	interval          float64
	payloadSize       int
	namespaceStr      string
	validatorHostFile string
	chainID           string
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

		return runLoad(ctx, endpoint, keyringDir, interval, payloadSize, namespaceStr, validatorHostFile, chainID)
	},
}

func init() {
	rootCmd.Flags().StringVarP(&endpoint, "grpc-endpoint", "e", defaultEndpoint, "gRPC endpoint of the consensus node")
	rootCmd.Flags().StringVarP(&keyringDir, "keyring-dir", "k", "", "directory containing the keyring (defaults to ~/.celestia-app)")
	rootCmd.Flags().Float64VarP(&interval, "interval", "i", defaultInterval, "interval between transactions in seconds")
	rootCmd.Flags().IntVarP(&payloadSize, "payload-size", "s", defaultPayloadSize, "size of payload data in bytes")
	rootCmd.Flags().StringVarP(&namespaceStr, "namespace", "n", defaultNamespaceStr, "namespace for blob submission")
	rootCmd.Flags().StringVarP(&validatorHostFile, "validator-hosts", "v", "", "path to JSON file containing validator address to host mapping (required)")
	rootCmd.Flags().StringVarP(&chainID, "chain-id", "c", defaultChainID, "chain ID for the network (can also be set via CHAIN_ID env var)")
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
	interval float64,
	payloadSize int,
	namespaceStr string,
	validatorHostFile string,
	chainID string,
) error {
	fmt.Printf("Fibre Load Generator\n")
	fmt.Printf("====================\n")
	fmt.Printf("gRPC Endpoint: %s\n", endpoint)
	fmt.Printf("Keyring Directory: %s\n", keyringDir)
	fmt.Printf("Chain ID: %s\n", chainID)
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
	// Estimate: 100 transactions worth of escrow funding
	if err := fundEscrowUpfront(ctx, txClient, payloadSize, 1000000); err != nil {
		return fmt.Errorf("failed to fund escrow: %w", err)
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
