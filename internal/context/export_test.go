package context

import (
	"text/template"

	"github.com/baphled/flowstate/internal/provider"
)

// templateMustBroken returns a parsed template whose Execute call will fail
// at runtime. The template accesses a map field that does not exist on
// summaryPromptData, which text/template reports as an execution error
// (parse succeeds because the action is syntactically valid).
func templateMustBroken() *template.Template {
	return template.Must(template.New("broken").Option("missingkey=error").Parse(`{{.DoesNotExist.Nested}}`))
}

// ExportedWalkUnits is a test-only shim exposing walkUnits to external test
// packages. It is defined in an _test.go file so the production build does
// not expose the walker's private contract.
func ExportedWalkUnits(msgs []provider.Message) []Unit {
	return walkUnits(msgs)
}

// ExportedWriteJob exposes the internal writeJob helper to error-path tests.
// Callers construct the job from public types via the shim below.
func ExportedWriteJob(storagePath string, kind UnitKind, msgs []provider.Message) error {
	job := persistJob{
		record:  CompactedMessage{StoragePath: storagePath},
		payload: CompactedUnit{Kind: kind, Messages: msgs},
	}
	return writeJob(job)
}

// ExportedSwapSummaryPromptTemplate replaces the pre-parsed summary prompt
// template with one whose Execute must fail (by referencing a non-existent
// field). Returns a restore function the test must defer. This exposes the
// otherwise unreachable Execute error branch in RenderSummaryPrompt to
// coverage tooling.
func ExportedSwapSummaryPromptTemplate() func() {
	original := parsedSummaryPrompt
	// A template that indexes a map nil-key on the data struct forces an
	// execution error without requiring the caller to construct invalid
	// template syntax.
	parsedSummaryPrompt = templateMustBroken()
	return func() { parsedSummaryPrompt = original }
}
