package session

import "context"

// IDKey is the context key used to propagate the active session ID.
type IDKey struct{}

// ProviderOverrideKey is the context key used to propagate a
// per-session provider override. When non-empty, the engine uses this
// value instead of the global preferred provider for the stream call.
type ProviderOverrideKey struct{}

// ModelOverrideKey is the context key used to propagate a
// per-session model override. When non-empty, the engine uses this
// value instead of the global preferred model for the stream call.
type ModelOverrideKey struct{}

// ProviderOverrideFromContext extracts the provider override from the
// context, returning an empty string when no override is present.
func ProviderOverrideFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ProviderOverrideKey{}).(string)
	return v
}

// ModelOverrideFromContext extracts the model override from the
// context, returning an empty string when no override is present.
func ModelOverrideFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ModelOverrideKey{}).(string)
	return v
}
