package cli

import "time"

// WaitForBackgroundExtractionsForTest exposes the unexported helper so
// its timeout-warn behaviour can be exercised without standing up a
// full engine+goroutine.
func WaitForBackgroundExtractionsForTest(waiter BackgroundExtractionWaiterForTest, timeout time.Duration) {
	waitForBackgroundExtractions(waiter, timeout)
}

// BackgroundExtractionWaiterForTest mirrors the unexported package
// interface so external test packages can satisfy it.
type BackgroundExtractionWaiterForTest interface {
	WaitForBackgroundExtractions(timeout time.Duration) bool
}
