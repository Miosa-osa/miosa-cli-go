package main

import (
	"os"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
