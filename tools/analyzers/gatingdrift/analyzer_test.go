package gatingdrift_test

import (
	"testing"

	"github.com/baphled/flowstate/tools/analyzers/gatingdrift"
	"golang.org/x/tools/go/analysis/analysistest"
)

var testdata = analysistest.TestData()

// TestDrift_FlagsStaleDocstring verifies the analyser reports a struct
// whose field docstring names a gating identifier (Caps.Field) the
// enclosing package never reads.
func TestDrift_FlagsStaleDocstring(t *testing.T) {
	analysistest.Run(t, testdata, gatingdrift.Analyzer, "drift")
}

// TestClean_PassesWhenGatingIdentifierIsRead verifies the analyser stays
// silent when the package actually reads the gating identifier named in
// the docstring.
func TestClean_PassesWhenGatingIdentifierIsRead(t *testing.T) {
	analysistest.Run(t, testdata, gatingdrift.Analyzer, "clean")
}

// TestMCPCase_CherryRevertsB960869 is the explicit anti-regression
// fixture: a miniature reproduction of the b960869 bug shape (MCP
// bypass). The analyser must report it. If this case ever stops
// firing, the analyser has lost the protection the user demanded.
func TestMCPCase_CherryRevertsB960869(t *testing.T) {
	analysistest.Run(t, testdata, gatingdrift.Analyzer, "mcpcase")
}
