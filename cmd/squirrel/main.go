package main

import (
	"os"

	"github.com/elpol4k0/squirrel/cmd/squirrel/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
