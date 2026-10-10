package main

import (
	"os"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		// Exit codes: see commands.ExitCode. A command that ran remotely and
		// failed exits with the remote status.
		os.Exit(commands.ExitCode(err))
	}
}
