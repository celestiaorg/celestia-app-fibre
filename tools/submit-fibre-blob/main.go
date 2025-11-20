package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/celestiaorg/celestia-app/v6/app"
	"github.com/celestiaorg/celestia-app/v6/app/encoding"
	"github.com/celestiaorg/celestia-app/v6/fibre"
	fibregrpc "github.com/celestiaorg/celestia-app/v6/fibre/grpc"
	"github.com/celestiaorg/celestia-app/v6/pkg/user"
	valaddrtypes "github.com/celestiaorg/celestia-app/v6/x/valaddr/types"
	"github.com/celestiaorg/go-square/v3/share"
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	var (
		chainID        = flag.String("chain-id", "test", "Chain ID")
		keyName        = flag.String("key-name", "validator", "Key name in keyring")
		keyringBackend = flag.String("keyring-backend", "test", "Keyring backend")
		home           = flag.String("home", "", "Home directory (default: $HOME/.celestia-app)")
		grpcAddr       = flag.String("grpc-addr", "localhost:9090", "gRPC address of the node")
		timeout        = flag.Duration("timeout", 30*time.Second, "Timeout for operations")
	)
	flag.Parse()

	// Set default home directory
	if *home == "" {
		*home = os.Getenv("HOME") + "/.celestia-app"
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Generate random blob data (1024 bytes)
	blobBytes := make([]byte, 1024)
	if _, err := rand.Read(blobBytes); err != nil {
		log.Fatalf("Failed to generate random blob: %v", err)
	}

	// Generate random namespace
	nsID := make([]byte, share.NamespaceVersionZeroIDSize)
	if _, err := rand.Read(nsID); err != nil {
		log.Fatalf("Failed to generate random namespace: %v", err)
	}
	id := make([]byte, 0, share.NamespaceIDSize)
	id = append(id, share.NamespaceVersionZeroPrefix...)
	id = append(id, nsID...)
	ns, err := share.NewNamespace(share.NamespaceVersionZero, id)
	if err != nil {
		log.Fatalf("Failed to create namespace: %v", err)
	}
	fmt.Printf("Generated random blob (size: %d bytes)\n", len(blobBytes))
	fmt.Printf("Generated random namespace: %s\n", hex.EncodeToString(nsID))

	// Create gRPC connection
	grpcConn, err := grpc.NewClient(
		*grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(math.MaxInt32),
			grpc.MaxCallRecvMsgSize(math.MaxInt32),
		),
	)
	if err != nil {
		log.Fatalf("Failed to create gRPC connection: %v", err)
	}
	defer grpcConn.Close()

	// Create encoding config
	encCfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)

	// Create keyring
	// Note: Currently only supports "test" backend. Other backends can be added if needed.
	var kr keyring.Keyring
	if *keyringBackend == "test" {
		kr, err = keyring.New(app.Name, keyring.BackendTest, *home, nil, encCfg.Codec)
	} else {
		log.Fatalf("Unsupported keyring backend: %s (only 'test' is supported)", *keyringBackend)
	}
	if err != nil {
		log.Fatalf("Failed to initialize keyring: %v", err)
	}

	// Create TxClient
	var txClient *user.TxClient
	if *keyName != "" {
		txClient, err = user.SetupTxClient(ctx, kr, grpcConn, encCfg, user.WithDefaultAccount(*keyName))
	} else {
		txClient, err = user.SetupTxClient(ctx, kr, grpcConn, encCfg)
	}
	if err != nil {
		log.Fatalf("Failed to set up tx client: %v", err)
	}

	// Create validator set getter
	valGet := fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(grpcConn))

	// Create host registry
	queryClient := valaddrtypes.NewQueryClient(grpcConn)
	hostReg := fibregrpc.NewHostRegistry(queryClient)

	// Create Fibre client config
	clientCfg := fibre.DefaultClientConfig()
	clientCfg.ChainID = *chainID
	clientCfg.DefaultKeyName = *keyName

	// Create Fibre client
	fibreClient, err := fibre.NewClient(txClient, kr, valGet, hostReg, clientCfg)
	if err != nil {
		log.Fatalf("Failed to create Fibre client: %v", err)
	}
	defer fibreClient.Close()

	// Submit blob using Put (which handles upload + PayForFibre transaction)
	fmt.Printf("Submitting Fibre blob (size: %d bytes, namespace: %s)...\n", len(blobBytes), ns.String())
	result, err := fibreClient.Put(ctx, ns, blobBytes)
	if err != nil {
		log.Fatalf("Failed to submit Fibre blob: %v", err)
	}

	fmt.Printf("Successfully submitted Fibre blob!\n")
	fmt.Printf("Transaction hash: %s\n", result.TxHash)
	fmt.Printf("Height: %d\n", result.Height)
}
