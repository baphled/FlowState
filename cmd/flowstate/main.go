package main

import (
	"os"

	"github.com/baphled/flowstate/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
