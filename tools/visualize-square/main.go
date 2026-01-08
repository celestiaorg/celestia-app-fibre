package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/celestiaorg/celestia-app/v6/app"
	"github.com/celestiaorg/celestia-app/v6/app/encoding"
	"github.com/celestiaorg/celestia-app/v6/pkg/appconsts"
	squarev4 "github.com/celestiaorg/go-square/v4"
	"github.com/celestiaorg/go-square/v4/share"
	"github.com/cometbft/cometbft/rpc/client/http"
)

func main() {
	var (
		rpcAddr = flag.String("rpc", "http://localhost:26657", "RPC address of the node")
		height  = flag.Int64("height", 0, "Block height to query (required)")
	)
	flag.Parse()

	if *height <= 0 {
		fmt.Fprintf(os.Stderr, "Error: height must be specified and greater than 0\n")
		flag.Usage()
		os.Exit(1)
	}

	ctx := context.Background()

	// Create RPC client
	client, err := http.New(*rpcAddr, "/websocket")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create RPC client: %v\n", err)
		os.Exit(1)
	}

	// Query block
	fmt.Printf("Querying block %d from %s...\n", *height, *rpcAddr)
	blockResp, err := client.Block(ctx, height)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get block: %v\n", err)
		os.Exit(1)
	}

	block := blockResp.Block
	appVersion := block.Version.App

	fmt.Printf("Block height: %d\n", block.Height)
	fmt.Printf("App version: %d\n", appVersion)
	fmt.Printf("Number of transactions: %d\n", len(block.Txs))

	// Create encoding config to get PayForFibre handler
	encCfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)
	handler := app.NewPayForFibreHandler(encCfg.TxConfig)

	// Construct data square
	txs := block.Txs.ToSliceOfBytes()
	maxSquareSize := appconsts.SquareSizeUpperBound
	subtreeRootThreshold := appconsts.SubtreeRootThreshold

	fmt.Printf("\nConstructing data square...\n")
	dataSquare, err := squarev4.Construct(txs, maxSquareSize, subtreeRootThreshold, handler)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to construct data square: %v\n", err)
		os.Exit(1)
	}

	squareSize := dataSquare.Size()
	totalShares := squareSize * squareSize

	fmt.Printf("Square size: %d x %d\n", squareSize, squareSize)
	fmt.Printf("Total shares: %d\n", totalShares)

	// Analyze namespaces
	namespaceRanges := analyzeNamespaces(dataSquare)

	// Visualize the square
	fmt.Printf("\n" + strings.Repeat("=", 80) + "\n")
	fmt.Printf("Data Square Visualization\n")
	fmt.Printf(strings.Repeat("=", 80) + "\n\n")
	visualizeSquare(dataSquare, namespaceRanges)
}

type namespaceInfo struct {
	namespace share.Namespace
	start     int
	end       int
	name      string
}

func analyzeNamespaces(square squarev4.Square) []namespaceInfo {
	if len(square) == 0 {
		return nil
	}

	var ranges []namespaceInfo
	currentNS := square[0].Namespace()
	start := 0

	for i := 1; i < len(square); i++ {
		ns := square[i].Namespace()
		if !ns.Equals(currentNS) {
			ranges = append(ranges, namespaceInfo{
				namespace: currentNS,
				start:     start,
				end:       i,
				name:      getNamespaceName(currentNS),
			})
			currentNS = ns
			start = i
		}
	}

	// Add the last range
	ranges = append(ranges, namespaceInfo{
		namespace: currentNS,
		start:     start,
		end:       len(square),
		name:      getNamespaceName(currentNS),
	})

	return ranges
}

func getNamespaceName(ns share.Namespace) string {
	switch {
	case ns.IsTx():
		return "Tx"
	case ns.IsPayForBlob():
		return "PayForBlob"
	case ns.IsPayForFibre():
		return "PayForFibre"
	case ns.IsTailPadding():
		return "TailPadding"
	case ns.IsPrimaryReservedPadding():
		return "ReservedPadding"
	default:
		// User namespace - show last 8 hex chars (the actual namespace ID)
		nsStr := ns.String()
		if len(nsStr) > 16 {
			// Show last 8 chars which represent the user-specified namespace ID
			return "Blob:" + nsStr[len(nsStr)-8:]
		}
		return "Blob:" + nsStr
	}
}

func visualizeSquare(square squarev4.Square, namespaceRanges []namespaceInfo) {
	squareSize := square.Size()
	if squareSize == 0 {
		fmt.Println("Empty square")
		return
	}

	// Create a map for quick lookup
	shareToNamespace := make(map[int]namespaceInfo)
	for _, nr := range namespaceRanges {
		for i := nr.start; i < nr.end; i++ {
			shareToNamespace[i] = nr
		}
	}

	// Print namespace legend
	fmt.Println("Namespace Legend:")
	fmt.Println(strings.Repeat("-", 80))
	for _, nr := range namespaceRanges {
		fmt.Printf("  %-20s: shares [%4d, %4d) (count: %4d) - %s\n",
			nr.name, nr.start, nr.end, nr.end-nr.start, nr.namespace.String())
	}
	fmt.Println()

	// Visualize as a grid
	fmt.Println("Square Layout (row-major order):")
	fmt.Println(strings.Repeat("-", 80))

	// For large squares, we'll show a summary view
	if squareSize > 32 {
		visualizeLargeSquare(square, squareSize, shareToNamespace)
	} else {
		visualizeSmallSquare(square, squareSize, shareToNamespace)
	}
}

func visualizeSmallSquare(square squarev4.Square, squareSize int, shareToNamespace map[int]namespaceInfo) {
	// Create a character map for different namespace types
	// Use meaningful characters for reserved namespaces
	nsToChar := make(map[string]rune)

	// Pre-assign characters for known namespace types
	nsToChar["Tx"] = 'T'
	nsToChar["PayForBlob"] = 'P'
	nsToChar["PayForFibre"] = 'F'
	nsToChar["TailPadding"] = '.'
	nsToChar["ReservedPadding"] = 'R'

	// Assign remaining characters for blob namespaces
	chars := []rune{'B', '1', '2', '3', '4', '5', '6', '7', '8', '9'}
	usedChars := 0

	for i := 0; i < len(square); i++ {
		if nr, ok := shareToNamespace[i]; ok {
			if _, exists := nsToChar[nr.name]; !exists {
				nsToChar[nr.name] = chars[usedChars%len(chars)]
				usedChars++
			}
		}
	}

	// Print column headers
	fmt.Print("    ")
	for col := 0; col < squareSize; col++ {
		fmt.Printf("%2d ", col)
	}
	fmt.Println()

	// Print rows
	for row := 0; row < squareSize; row++ {
		fmt.Printf("%2d: ", row)
		for col := 0; col < squareSize; col++ {
			idx := row*squareSize + col
			if idx < len(square) {
				if nr, ok := shareToNamespace[idx]; ok {
					char := nsToChar[nr.name]
					fmt.Printf("%c  ", char)
				} else {
					fmt.Print("?  ")
				}
			} else {
				fmt.Print(".  ")
			}
		}
		fmt.Println()
	}

	// Print legend
	fmt.Println("\nCharacter Legend:")
	for name, char := range nsToChar {
		fmt.Printf("  %c = %s\n", char, name)
	}
}

func visualizeLargeSquare(square squarev4.Square, squareSize int, shareToNamespace map[int]namespaceInfo) {
	// For large squares, show a summary
	fmt.Printf("Square is %d x %d (too large to display in detail)\n\n", squareSize, squareSize)

	// Show first few rows
	fmt.Println("First 16 rows (sample):")
	fmt.Print("    ")
	for col := 0; col < 16 && col < squareSize; col++ {
		fmt.Printf("%2d ", col)
	}
	fmt.Println()

	for row := 0; row < 16 && row < squareSize; row++ {
		fmt.Printf("%2d: ", row)
		for col := 0; col < 16 && col < squareSize; col++ {
			idx := row*squareSize + col
			if idx < len(square) {
				if nr, ok := shareToNamespace[idx]; ok {
					// Use first letter of namespace name
					char := rune(nr.name[0])
					if char >= 'a' && char <= 'z' {
						char = char - 'a' + 'A'
					}
					fmt.Printf("%c  ", char)
				} else {
					fmt.Print("?  ")
				}
			} else {
				fmt.Print(".  ")
			}
		}
		fmt.Println()
	}

	// Show namespace distribution
	fmt.Println("\nNamespace Distribution (by row):")
	fmt.Println(strings.Repeat("-", 80))

	// Get namespace ranges from the square
	namespaceRanges := analyzeNamespaces(square)
	for _, nr := range namespaceRanges {
		startRow := nr.start / squareSize
		endRow := (nr.end - 1) / squareSize
		rowSpan := endRow - startRow + 1
		fmt.Printf("  %-20s: rows %4d-%4d (%d rows), shares [%4d, %4d)\n",
			nr.name, startRow, endRow, rowSpan, nr.start, nr.end)
	}
}
