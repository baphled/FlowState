package testutils

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// BlackholeServer returns an httptest server that accepts requests but never
// writes response headers, self-returning after the given cap so Close drains
// cleanly. It reproduces the provider-flap signature live: the connection is
// accepted, but no first byte ever arrives.
//
// Expected:
//   - selfReturnAfter: How long the handler holds the connection open
//     before returning on its own.
//
// Returns:
//   - A black-hole *httptest.Server.
//
// Side effects:
//   - None; callers must Close the returned server.
func BlackholeServer(selfReturnAfter time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(selfReturnAfter):
		}
	}))
}

// AssertStreamTerminatesWithin fails the test unless the stream started by
// the given function fully terminates within the provided window. It drains
// the chunk channel and tolerates an immediate start error.
//
// Expected:
//   - t: The running test handle.
//   - start: Opens the stream under test.
//   - within: Maximum time the stream may take to terminate.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Calls t.Fatalf when the stream exceeds the window.
func AssertStreamTerminatesWithin(t *testing.T, start func() (<-chan provider.StreamChunk, error), within time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ch, err := start()
		if err == nil {
			for range ch {
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("stream did not terminate within %s — stream-guard not bounding the no-headers window", within)
	}
}
