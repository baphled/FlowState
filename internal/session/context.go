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

// StreamAgentOverrideKey is the context key used to redirect a single
// turn through a different agent than the session's persistent
// CurrentAgentID || AgentID. When non-empty, SendMessage drives the
// streamer and stamps the assistant message with this id instead of
// the session's resolved agent. The user message stamp stays under the
// session's persistent agent — the override is per-turn only and never
// mutates the session record.
//
// The session-scoped POST handler uses this for in-content @-mention
// dispatch (Bug 1 / May 2026): typing `@a-team` inside a
// default-assistant session redirects THIS turn to a-team's lead so
// the engine streams from the swarm with the matching Swarm Leadership
// block installed, while CurrentAgentID stays empty so the next turn
// without a mention routes back through default-assistant.
//
// Empty string is the dominant case; both an absent key and an empty
// value mean "no override, use the session's resolved agent".
type StreamAgentOverrideKey struct{}

// WithStreamAgentOverride returns a derived context carrying a single-
// turn agent override. Empty/whitespace agentID short-circuits to the
// input context unchanged — the override is opt-in and a noop when the
// caller has no specific agent to redirect to.
func WithStreamAgentOverride(ctx context.Context, agentID string) context.Context {
	if agentID == "" {
		return ctx
	}
	return context.WithValue(ctx, StreamAgentOverrideKey{}, agentID)
}

// StreamAgentOverrideFromContext extracts the per-turn agent override
// from the context. Returns an empty string when no override is set,
// which is the dominant case for sessions driven by their persistent
// agent_id.
func StreamAgentOverrideFromContext(ctx context.Context) string {
	v, _ := ctx.Value(StreamAgentOverrideKey{}).(string)
	return v
}

// AttachmentsKey is the context key used to propagate attachment
// references for the current turn from the session manager into the
// engine. The engine reads this on Stream entry and threads the slice
// onto the user message that gets built for the model request payload,
// where the per-provider translator (anthropic.attachmentsToBlocks,
// future openai/copilot equivalents) lifts each entry into a native
// content block.
//
// Plan "Chat Attachments Backend (May 2026)" §6 task-04 — engine seam
// stays pure-Go-data; the materialised []provider.Attachment slice
// carries the raw bytes for the in-flight turn only and is dropped
// after the request is constructed.
type AttachmentsKey struct{}

// WithAttachments returns a derived context carrying the supplied
// per-turn attachment slice. A nil/empty slice is a no-op — callers
// that have no attachments should not call this helper at all, so
// AttachmentsFromContext returns the zero state.
func WithAttachments(ctx context.Context, atts []provider.Attachment) context.Context {
	if len(atts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, AttachmentsKey{}, atts)
}

// AttachmentsFromContext extracts the per-turn attachment slice from
// the context. Returns a nil slice (and length-zero) when no key is
// present, which is the dominant case for text-only turns.
func AttachmentsFromContext(ctx context.Context) []provider.Attachment {
	v, _ := ctx.Value(AttachmentsKey{}).([]provider.Attachment)
	return v
}
