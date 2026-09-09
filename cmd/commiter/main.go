package main

import (
	"os"

	"github.com/natsuki0413/commiter-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
