package docblocks_test

import (
	"testing"

	"github.com/baphled/flowstate/tools/analyzers/docblocks"
	"golang.org/x/tools/go/analysis/analysistest"
)

var testdata = analysistest.TestData()

func TestFunctions(t *testing.T) {
	analysistest.Run(t, testdata, docblocks.Analyzer, "funcs")
}

func TestMethods(t *testing.T) {
	analysistest.Run(t, testdata, docblocks.Analyzer, "methods")
}

func TestTypes(t *testing.T) {
	analysistest.Run(t, testdata, docblocks.Analyzer, "types")
}

func TestConstVars(t *testing.T) {
	analysistest.Run(t, testdata, docblocks.Analyzer, "constvars")
}

func TestExclusions(t *testing.T) {
	analysistest.Run(t, testdata, docblocks.Analyzer, "mainpkg")
}
