package engine

import (
	"context"

	"github.com/baphled/flowstate/internal/agent"
)

// boundManifestKey is the context key used to bind a per-stream
// agent manifest to a context. The engine's BuildSystemPromptCtx
// and ToolSchemasCtx read it so that two concurrent Stream calls
// — each holding their own ctx with a different bound manifest —
// each build their system prompt and tool schemas from the
// manifest that owns the call, not from whichever manifest most
// recently won the race to mutate the engine's shared state.
//
// This is the seam that dissolves the cross-session manifest
// leakage: SetManifest still updates the engine's "current"
// manifest for sequential single-session flows and swap-for-
// next-turn semantics, but concurrent flows route through ctx
// so they cannot stomp on one another.
type boundManifestKey struct{}

// WithBoundManifest returns a copy of ctx that carries the given
// manifest as the per-stream binding. Stream() calls this once at
// entry, after resolving the requested agentID against the
// registry, and passes the returned ctx through buildContextWindow
// and any retry / hook path so every BuildSystemPrompt and
// ToolSchemas read sees the same value for the entire stream
// lifecycle.
//
// Pass an empty manifest (zero-value) to clear an existing
// binding — this is purely defensive; callers normally pass a
// concrete manifest or simply do not bind one at all.
//
// Expected:
//   - ctx is a valid context.
//   - manifest is the manifest the caller wants this stream to
//     use; an empty Manifest.ID disables the binding.
//
// Returns:
//   - A derived context carrying manifest.
//
// Side effects:
//   - None.
func WithBoundManifest(ctx context.Context, manifest agent.Manifest) context.Context {
	if manifest.ID == "" {
		return ctx
	}
	return context.WithValue(ctx, boundManifestKey{}, manifest)
}

// manifestFromContext extracts a bound manifest from ctx if one
// is present.
//
// Expected:
//   - ctx is a valid context that may carry a boundManifestKey
//     value.
//
// Returns:
//   - The bound manifest and true when one is set; the zero
//     manifest and false otherwise.
//
// Side effects:
//   - None.
func manifestFromContext(ctx context.Context) (agent.Manifest, bool) {
	if ctx == nil {
		return agent.Manifest{}, false
	}
	m, ok := ctx.Value(boundManifestKey{}).(agent.Manifest)
	if !ok || m.ID == "" {
		return agent.Manifest{}, false
	}
	return m, true
}
