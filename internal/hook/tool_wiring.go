package hook

import (
	"context"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// ToolWiringHook dynamically wires per-manifest tools at request time.
//
// Two manifest signals can independently require lazy wiring:
//
//   - Delegation tools (delegate, background_output, background_cancel)
//     are wired when manifest.Delegation.CanDelegate is true.
//   - coordination_store is wired when manifest.Capabilities.Tools
//     contains "coordination_store" — independent of CanDelegate, per
//     ADR-Coordination Store Boundary §Decision 9.
//
// When at least one signal applies and the corresponding tool is not
// yet registered, ensureTools is called to lazily wire that manifest's
// runtime tool set. req.Tools is then refreshed from the schema
// rebuilder so the provider sees the up-to-date tool list. Manifests
// that satisfy neither signal short-circuit to the next handler — the
// hook does not mutate the request.
//
// Expected:
//   - manifestGetter returns the current agent manifest on each call.
//   - hasTool checks whether a named tool is registered in the engine.
//   - ensureTools wires runtime tools into the engine for the given
//     manifest.
//   - schemaRebuilder returns the current filtered tool schemas.
//
// Returns:
//   - A Hook that lazily wires runtime tools and refreshes req.Tools.
//
// Side effects:
//   - May trigger tool registration via ensureTools on first request
//     after a manifest swap.
//   - Overwrites req.Tools with freshly built schemas when at least one
//     wiring signal applies.
func ToolWiringHook(
	manifestGetter func() agent.Manifest,
	hasTool func(string) bool,
	ensureTools func(agent.Manifest),
	schemaRebuilder func() []provider.Tool,
) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			manifest := manifestGetter()
			canDelegate := manifest.Delegation.CanDelegate
			needsCoord := declaresCoordinationStore(manifest)
			if !canDelegate && !needsCoord {
				return next(ctx, req)
			}
			if canDelegate && !hasTool("delegate") {
				ensureTools(manifest)
			} else if needsCoord && !hasTool("coordination_store") {
				ensureTools(manifest)
			}
			req.Tools = schemaRebuilder()
			return next(ctx, req)
		}
	}
}

// declaresCoordinationStore reports whether the manifest opts into the
// coordination_store tool via capabilities.tools. Local copy of the
// canonical guard (App.hasCoordinationTool) to avoid a hook → app
// import cycle; one allocation per request, net cost negligible.
func declaresCoordinationStore(manifest agent.Manifest) bool {
	for _, t := range manifest.Capabilities.Tools {
		if t == "coordination_store" {
			return true
		}
	}
	return false
}
