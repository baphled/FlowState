// Package permissionmode carries the per-session permission-mode value
// across the engine → tool dispatch boundary via context.
//
// Permission modes are the user-facing safety dial on a FlowState
// session. The valid set is fixed at four values:
//
//   - ModePlan         — read-only; the engine filters write tools out
//                        of the schema list and the assistant cannot
//                        mutate the workspace. Enforcement for Plan
//                        lives engine-side (schema filter), NOT here.
//   - ModeDefault      — current behaviour: permissions.yaml + the
//                        legacy denied-roots check both apply.
//   - ModeAcceptEdits  — Write/Edit/MultiEdit prompts auto-accept;
//                        pathguard still enforces deny rules.
//   - ModeYolo         — full bypass: every pathguard *ForTool check
//                        short-circuits to PASS. Reserved for trusted
//                        sandboxes (e.g. ephemeral worktree agents).
//
// This package owns the canonical context key + accessors so that the
// session manager (the producer) and pathguard (the consumer) can
// share the value without either pulling in the engine package — the
// engine→session import chain would otherwise close into a cycle.
// The engine re-exports the same surface under engine.WithPermissionMode
// / engine.PermissionModeFromContext to honour the Permission Modes
// plan's stated API contract.
package permissionmode

import "context"

// Mode is one of the four canonical permission-mode values. A typed
// string (rather than an iota) keeps the value JSON-friendly for
// persistence on Session and forward-compatible if a future mode is
// introduced without renumbering.
type Mode = string

const (
	// ModePlan denotes a planning-only session. Engine schema
	// filtering enforces it — pathguard treats Plan identically to
	// Default (Plan is NOT a YOLO bypass).
	ModePlan Mode = "plan"
	// ModeDefault is the standard configuration. permissions.yaml +
	// legacy denied-roots both apply.
	ModeDefault Mode = "default"
	// ModeAcceptEdits auto-accepts permission prompts for the
	// Write/Edit/MultiEdit family. Pathguard deny rules still fire;
	// the auto-accept lives in the prompt layer, not here.
	ModeAcceptEdits Mode = "accept_edits"
	// ModeYolo is the full-bypass mode. Pathguard's *ForTool
	// methods short-circuit to PASS at the top of the function
	// body before any permissions-matcher or denied-roots check
	// runs.
	ModeYolo Mode = "yolo"
)

// modeKey is the unexported context key type. Using an unexported
// struct guarantees no accidental collision with other packages that
// might key contexts on a string literal.
type modeKey struct{}

// WithMode returns a derived context carrying the supplied permission
// mode. Empty input short-circuits to the input context unchanged so
// callers can opt out by passing "" — this preserves the "default
// behaviour when the key is absent" semantic and avoids stamping a
// zero value that downstream consumers would treat as the same as
// ModeDefault anyway.
func WithMode(ctx context.Context, mode Mode) context.Context {
	if mode == "" {
		return ctx
	}
	return context.WithValue(ctx, modeKey{}, mode)
}

// FromContext extracts the permission mode bound to ctx, returning
// ModeDefault when no key is present (or when the value bound is the
// empty string). Callers MUST treat ModeDefault as the safe fall-back
// — a missing mode means "behave as Default", never "bypass".
func FromContext(ctx context.Context) Mode {
	if ctx == nil {
		return ModeDefault
	}
	v, _ := ctx.Value(modeKey{}).(Mode)
	if v == "" {
		return ModeDefault
	}
	return v
}
