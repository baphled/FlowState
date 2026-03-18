// Package main provides the entry point for the docblocks analyser command.
package main

import (
	"github.com/baphled/flowstate/tools/analyzers/docblocks"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(docblocks.Analyzer)
}
