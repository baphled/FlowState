package engine

import (
	"context"

	"github.com/baphled/flowstate/internal/permissionmode"
)

// WithPermissionMode returns a derived context carrying the supplied
// permission mode. Re-exports permissionmode.WithMode under the engine
// package's API surface — the Permission Modes plan (May 2026) §4
// Slice 1 contract names engine.WithPermissionMode as the canonical
// stamping helper.
//
// The canonical key + storage live in internal/permissionmode so that
// pathguard (the consumer) and session.Manager (the producer) can
// import the package without pulling engine into a cycle.
func WithPermissionMode(ctx context.Context, mode string) context.Context {
	return permissionmode.WithMode(ctx, mode)
}

// PermissionModeFromContext extracts the permission mode bound to ctx,
// returning the canonical "default" value when no key is present.
// Re-exports permissionmode.FromContext.
func PermissionModeFromContext(ctx context.Context) string {
	return permissionmode.FromContext(ctx)
}
