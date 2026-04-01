package hook

import (
	"context"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// ToolWiringHook dynamically wires delegation tools when the active agent permits delegation.
//
// This hook checks on every request whether the current agent manifest has can_delegate set.
// When delegation is enabled and the delegate tool is not yet registered, it calls ensureTools
// to lazily wire the delegation tools (delegate, background_output, background_cancel,
// coordination_store). It then rebuilds the request's tool schemas to reflect any changes.
//
// Expected:
//   - manifestGetter returns the current agent manifest on each call.
//   - hasTool checks whether a named tool is registered in the engine.
//   - ensureTools wires delegation tools into the engine for the given manifest.
//   - schemaRebuilder returns the current filtered tool schemas.
//
// Returns:
//   - A Hook that lazily wires delegation tools and refreshes req.Tools.
//
// Side effects:
//   - May trigger tool registration via ensureTools on first delegating request.
//   - Overwrites req.Tools with freshly built schemas for delegating agents.
func ToolWiringHook(
	manifestGetter func() agent.Manifest,
	hasTool func(string) bool,
	ensureTools func(agent.Manifest),
	schemaRebuilder func() []provider.Tool,
) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			manifest := manifestGetter()
			if !manifest.Delegation.CanDelegate {
				return next(ctx, req)
			}
			if !hasTool("delegate") {
				ensureTools(manifest)
			}
			req.Tools = schemaRebuilder()
			return next(ctx, req)
		}
	}
}
