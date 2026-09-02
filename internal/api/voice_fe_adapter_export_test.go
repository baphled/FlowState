package api_test

import (
	"context"

	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
)

// errDispatcher is a voice.Dispatcher stand-in whose DispatchEphemeral
// always fails, pinning the verbatim error pass-through of the adapter
// path.
type errDispatcher struct{}

// DispatchEphemeral always fails with a deadline error.
func (errDispatcher) DispatchEphemeral(ctx context.Context, _ dispatchpkg.DispatchRequest, _ interface{}) (dispatchpkg.EphemeralHandle, error) {
	return dispatchpkg.EphemeralHandle{}, context.DeadlineExceeded
}
