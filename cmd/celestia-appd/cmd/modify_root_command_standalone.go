//go:build !multiplexer

package cmd

import (
	"github.com/celestiaorg/celestia-app-fibre/v6/app"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/spf13/cobra"
)

// modifyRootCommand sets the default root command without adding a multiplexer.
// It uses a custom start command handler for explicit server lifecycle handling.
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
}
