package support

import (
	"github.com/cucumber/godog"
)

// RegisterMemorySteps registers memory-specific step definitions.
//
// Expected:
//   - ctx is a non-nil godog.ScenarioContext for registering steps.
//
// Returns:
//   - None.
//
// Side effects:
//   - Registers step definitions on ctx. flowstate-memory-server has been
//     removed in favour of the native Qdrant-backed learning store.
func RegisterMemorySteps(ctx *godog.ScenarioContext) {
	_ = ctx
}
