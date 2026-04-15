package cli

import (
	"time"

	"github.com/baphled/flowstate/internal/app"
)

// WaitForBackgroundExtractionsForTest exposes the unexported helper so
// its timeout-warn behaviour can be exercised without standing up a
// full engine+goroutine.
func WaitForBackgroundExtractionsForTest(waiter BackgroundExtractionWaiterForTest, timeout time.Duration) {
	waitForBackgroundExtractions(waiter, timeout)
}

// BackgroundExtractionWaiterForTest mirrors the unexported package
// interface so external test packages can satisfy it.
type BackgroundExtractionWaiterForTest interface {
	WaitForBackgroundExtractions(timeout time.Duration) error
}

// ResolveBackgroundExtractionWaitForTest exposes the unexported config
// resolver so Item 7's propagation test can verify that a custom
// compression.session_memory.wait_timeout reaches the CLI exit path.
func ResolveBackgroundExtractionWaitForTest(application *app.App) time.Duration {
	return resolveBackgroundExtractionWait(application)
}

// DefaultBackgroundExtractionWaitForTest exposes the fallback constant
// so tests can assert both the default-path and overridden-path values
// without duplicating the 35-second literal.
const DefaultBackgroundExtractionWaitForTest = defaultBackgroundExtractionWait
