// Package main provides the entry point for the gatingdrift analyser
// command — Guard 3 of the review-pattern guards. See
// tools/analyzers/gatingdrift/analyzer.go for the rules and the b960869
// cherry-revert anti-regression case.
package main

import (
	"github.com/baphled/flowstate/tools/analyzers/gatingdrift"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(gatingdrift.Analyzer)
}
