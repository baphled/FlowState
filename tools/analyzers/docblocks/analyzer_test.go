package docblocks_test

import (
	"testing"

	"github.com/baphled/flowstate/tools/analyzers/docblocks"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestFunctions(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, docblocks.Analyzer, "funcs")
}

func TestMethods(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, docblocks.Analyzer, "methods")
}

func TestTypes(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, docblocks.Analyzer, "types")
}

func TestConstVars(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, docblocks.Analyzer, "constvars")
}

func TestExclusions(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, docblocks.Analyzer, "mainpkg")
}
