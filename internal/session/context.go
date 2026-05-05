package session

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

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

// PriorMessagesKey is the context key used to propagate the per-session
// conversation history from the session manager into the engine. When
// present, the engine uses these messages as the source of truth for
// the model request payload instead of reading from its process-wide
// recall store. This isolates context windows across concurrent
// sessions sharing a single engine — without it, the shared store
// accumulates every session's turns and leaks them as a prefix to the
// next session's request.
type PriorMessagesKey struct{}

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

// WithPriorMessages returns a derived context carrying the supplied
// session-scoped history. Always attaches the key — including for an
// empty slice — so the receiving engine treats this as a "use
// session-scoped messages" call rather than falling back to its
// process-wide shared-store path. A nil/empty slice means "fresh
// session, no history yet"; both are valid and both must take the
// session-scoped path. Without this, turn 1 of every session would
// silently fall through to the shared store (which still accumulates
// every session's history) and inherit a contaminated prefix.
func WithPriorMessages(ctx context.Context, msgs []provider.Message) context.Context {
	if msgs == nil {
		// Materialise a non-nil empty slice so PriorMessagesFromContext
		// can distinguish "key attached, no history" from "key absent".
		msgs = []provider.Message{}
	}
	return context.WithValue(ctx, PriorMessagesKey{}, msgs)
}

// PriorMessagesFromContext extracts the per-session prior messages from
// the context. The boolean return distinguishes "no key present" (use
// the legacy shared-store path — CLI behaviour) from "key attached"
// (use session-scoped messages, even if empty). An empty slice with
// the key attached means "fresh session, no prior history" and must
// still bypass the shared store.
func PriorMessagesFromContext(ctx context.Context) ([]provider.Message, bool) {
	v, ok := ctx.Value(PriorMessagesKey{}).([]provider.Message)
	if !ok {
		return nil, false
	}
	return v, true
}
