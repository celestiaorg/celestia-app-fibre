//go:build !multiplexer

package cmd

import (
	"fmt"

	"github.com/celestiaorg/celestia-app/v6/app"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/spf13/cobra"
)

// modifyRootCommand sets the default root command without adding a multiplexer.
// It uses a custom start command handler to integrate Fibre server for validators.
func modifyRootCommand(rootCommand *cobra.Command) {
	server.AddCommandsWithStartCmdOptions(
		rootCommand,
		app.NodeHome,
		NewAppServer,
		appExporter,
		server.StartCmdOptions{
			AddFlags:            addStartFlags,
			StartCommandHandler: startCommandHandler,
		},
	)

	// Enhance start command documentation with Fibre server information
	if err := enhanceStartCommandHelp(rootCommand); err != nil {
		panic(fmt.Errorf("failed to enhance start command help: %w", err))
	}
}
