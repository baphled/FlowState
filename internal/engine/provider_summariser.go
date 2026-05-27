package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// ErrNilProvider is returned when ProviderSummariser is constructed with
// a nil chat provider. Exposed so callers can distinguish misconfiguration
// from a transport or model-selection failure at call time.
var ErrNilProvider = errors.New("provider summariser: chat provider is nil")

// sessionModelCtxKey is the typed context key used to thread the
// targeted session's (provider, model) pair from CompactNow down to
// ProviderSummariser.resolveRoute. Unexported so external callers must
// go through WithSessionModel / sessionModelFromContext.
type sessionModelCtxKey struct{}

// sessionModelHint carries the per-call session model fallback. Both
// fields are optional but interpreted together — an empty modelID
// disables the hint regardless of providerID.
type sessionModelHint struct {
	providerID string
	modelID    string
}

// WithSessionModel returns a derived context that carries the session's
// (providerID, modelID) as a fallback hint for the summariser route.
//
// The hint is consulted by ProviderSummariser.resolveRoute when the
// category-routed model is empty OR an abstract descriptor that the
// resolver could not expand (e.g. "fast"/"reasoning" without a
// ModelLister wired). In that case the summariser will issue its Chat
// call against the session's current model instead of sending an
// unresolvable descriptor to the provider, which the provider would
// reject with "Unknown Model" — the May 2026 /compact regression.
//
// Expected:
//   - parent is the caller's context; never nil per net/context contract.
//   - providerID and modelID identify the session's currently selected
//     provider+model. Empty modelID is a no-op (returns parent
//     unchanged) so callers without session-stamping don't have to
//     branch.
//
// Returns:
//   - A context.Context derived from parent. When modelID is empty the
//     returned ctx is parent itself — no allocation.
//
// Side effects:
//   - None.
func WithSessionModel(parent context.Context, providerID, modelID string) context.Context {
	if modelID == "" {
		return parent
	}
	return context.WithValue(parent, sessionModelCtxKey{}, sessionModelHint{
		providerID: providerID,
		modelID:    modelID,
	})
}

// sessionModelFromContext returns the session model hint attached to
// ctx by WithSessionModel, or a zero hint when none is present. The
// zero value signals "no hint" — callers MUST treat empty modelID as
// "fall through to the next layer of fallback".
//
// Expected:
//   - ctx may be nil; a nil ctx returns a zero hint without panicking.
//
// Returns:
//   - The attached hint, or zero value when none is present.
//
// Side effects:
//   - None.
func sessionModelFromContext(ctx context.Context) sessionModelHint {
	if ctx == nil {
		return sessionModelHint{}
	}
	if hint, ok := ctx.Value(sessionModelCtxKey{}).(sessionModelHint); ok {
		return hint
	}
	return sessionModelHint{}
}

// ProviderSummariser adapts a provider.Provider and a SummariserResolver
// to the ctxstore.Summariser interface expected by the L2 AutoCompactor.
//
// Routing is delegated to the SummariserResolver per the ADR - Agent Model
// Contract: the summary tier on the manifest picks the category, and the
// CategoryConfig returned by the resolver supplies the model (and,
// optionally, provider) used for the summarisation call.
//
// When no manifest is available (ctxstore.AutoCompactor.Compact does not
// pass one), the adapter falls back to fallbackModel. This keeps L2
// functional for bootstraps that have not yet threaded a manifest through
// the hot path while still honouring the category routing contract when
// an explicit manifest is later wired via WithManifest.
type ProviderSummariser struct {
	chatProvider  provider.Provider
	resolver      SummariserResolver
	manifest      *agent.Manifest
	fallbackModel string
}

// NewProviderSummariser constructs an adapter. The chatProvider is
// required; passing nil is allowed but every Summarise call will return
// ErrNilProvider so misconfiguration surfaces at the first use rather
// than at construction.
//
// Expected:
//   - chatProvider is the provider.Provider used to issue the Chat call.
//     May be nil; see the note above.
//   - resolver may be nil; when nil the adapter uses fallbackModel
//     unconditionally. This keeps the adapter usable in bootstrap paths
//     that do not yet have a CategoryResolver wired.
//   - fallbackModel is the model identifier used when the resolver yields
//     no model (empty string or nil resolver). An empty fallbackModel is
//     accepted; the provider may reject the request downstream.
//
// Returns:
//   - A ProviderSummariser. Never nil.
//
// Side effects:
//   - None.
func NewProviderSummariser(chatProvider provider.Provider, resolver SummariserResolver, fallbackModel string) *ProviderSummariser {
	return &ProviderSummariser{
		chatProvider:  chatProvider,
		resolver:      resolver,
		fallbackModel: fallbackModel,
	}
}

// WithManifest binds the agent manifest used for category resolution.
// This is a fluent setter so callers can construct the adapter before the
// manifest is known and bind it once the engine is fully initialised.
//
// Expected:
//   - m may be nil; a nil manifest forces the fallback model path.
//
// Returns:
//   - The receiver for chaining. Never nil.
//
// Side effects:
//   - Mutates the receiver's manifest field.
func (p *ProviderSummariser) WithManifest(m *agent.Manifest) *ProviderSummariser {
	p.manifest = m
	return p
}

// Summarise satisfies ctxstore.Summariser by issuing a single Chat call
// to the configured provider. The T8 system and user prompts are threaded
// through as separate messages so the provider sees the role boundary.
//
// Expected:
//   - ctx carries cancellation/deadline for the remote call.
//   - systemPrompt is the fixed T8 SummaryPromptSystem.
//   - userPrompt is the rendered user prompt from AutoCompactor.
//   - msgs is the original cold-message slice; unused here because the
//     rendered userPrompt already encodes it.
//
// Returns:
//   - The model's raw textual response on success.
//   - ErrNilProvider when the adapter was constructed without a provider.
//   - Any provider error wrapped with context for diagnostics.
//
// Side effects:
//   - One Chat call against the configured provider.
func (p *ProviderSummariser) Summarise(
	ctx context.Context,
	systemPrompt string,
	userPrompt string,
	_ []provider.Message,
) (string, error) {
	if p.chatProvider == nil {
		return "", ErrNilProvider
	}

	model, providerName := p.resolveRoute(ctx)

	resp, err := p.chatProvider.Chat(ctx, provider.ChatRequest{
		Provider: providerName,
		Model:    model,
		Messages: []provider.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("provider summariser: chat call: %w", err)
	}
	return resp.Message.Content, nil
}

// resolveRoute returns the (model, provider) pair the summariser should
// call. The route is decided in three tiers, evaluated in order:
//
//  1. Category routing: when both a manifest and a SummariserResolver
//     are wired AND the resolved CategoryConfig yields a concrete model
//     (non-empty AND not an abstract descriptor — see
//     IsAbstractModelDescriptor), use that pair. This honours the
//     ADR-Agent-Model-Contract route when the deployment has wired a
//     ModelLister or supplied concrete category overrides.
//
//  2. Session model hint: when a session model is attached to ctx via
//     WithSessionModel (set by Engine.CompactNow per session), use it
//     as the route. This is the fallback the May 2026 /compact bug
//     needs: the resolver yields "fast"/"reasoning" without a
//     ModelLister, the per-deployment Ollama fallback is empty for
//     non-Ollama deployments, and the provider would reject the
//     abstract descriptor as "Unknown Model". Sending the session's
//     current model (e.g. "glm-4.6") guarantees a valid request.
//
//  3. Static fallback: the fallbackModel string supplied at
//     construction (typically cfg.Providers.Ollama.Model). Empty
//     fallbackModel returns an empty model — the chat provider will
//     reject the request loudly rather than silently substituting.
//
// Expected:
//   - The receiver's manifest and resolver may be nil. Neither is a
//     fatal condition; the method walks the tiers above.
//   - ctx may carry a session model hint via WithSessionModel.
//
// Returns:
//   - model is the model identifier to use in ChatRequest.Model.
//   - providerName is the ChatRequest.Provider hint. Empty when no
//     tier supplies one, letting the caller's chat provider pick.
//
// Side effects:
//   - None.
func (p *ProviderSummariser) resolveRoute(ctx context.Context) (model, providerName string) {
	// Tier 1: category routing when wired and concrete.
	if p.resolver != nil && p.manifest != nil {
		if cfg, err := p.resolver.ResolveForManifest(p.manifest); err == nil {
			if cfg.Model != "" && !IsAbstractModelDescriptor(cfg.Model) {
				return cfg.Model, cfg.Provider
			}
			// Category routing yielded an unresolved abstract
			// descriptor (e.g. "fast" with no ModelLister wired) or
			// an empty model. Fall through to the session hint —
			// keeping cfg.Provider would pin the request to a
			// provider that may not host the session's model, so
			// drop it together with the model.
		}
	}

	// Tier 2: session model hint. Honoured even when the resolver was
	// not wired — gives bootstrap paths a deterministic fallback.
	if hint := sessionModelFromContext(ctx); hint.modelID != "" {
		return hint.modelID, hint.providerID
	}

	// Tier 3: static fallback. Empty fallbackModel will be rejected by
	// the chat provider; that is preferable to silently picking a
	// surprise default.
	return p.fallbackModel, ""
}

// Compile-time guard that ProviderSummariser satisfies ctxstore.Summariser.
var _ ctxstore.Summariser = (*ProviderSummariser)(nil)
