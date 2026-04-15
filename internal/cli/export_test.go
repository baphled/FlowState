package cli

import (
	"io"
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

// HTTPShutdownerForTest mirrors the unexported httpShutdowner interface
// so Item 6's regression test can inject a fake server.
type HTTPShutdownerForTest = httpShutdowner

// EngineShutdownerForTest mirrors the unexported engineShutdowner
// interface. Item 6 uses it to assert that serve shutdown always
// invokes Engine.Shutdown; a future refactor of runServe that skips
// the call must fail this test.
type EngineShutdownerForTest = engineShutdowner

// PerformServeShutdownForTest exposes the unexported helper so the
// regression test can drive it without binding a port or wiring up a
// signal loop.
func PerformServeShutdownForTest(server HTTPShutdownerForTest, eng EngineShutdownerForTest, out, errOut io.Writer) error {
	return performServeShutdown(server, eng, out, errOut)
}
