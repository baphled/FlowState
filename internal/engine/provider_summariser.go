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

	model, providerName := p.resolveRoute()

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
// call. The resolver is consulted first when both a manifest and resolver
// are present; any error or empty result degrades gracefully to the
// fallbackModel. An empty provider string lets the caller's chat provider
// pick its own default.
//
// Expected:
//   - The receiver's manifest and resolver may be nil. Neither is a
//     fatal condition: the method treats missing inputs as "use the
//     fallback model and let the chat provider pick its own provider".
//
// Returns:
//   - model is the model identifier to use in ChatRequest.Model. Equals
//     fallbackModel when the resolver cannot produce one.
//   - providerName is the ChatRequest.Provider hint. Empty when the
//     resolver did not supply one, letting the caller's provider pick.
//
// Side effects:
//   - None.
func (p *ProviderSummariser) resolveRoute() (model, providerName string) {
	model = p.fallbackModel
	if p.resolver == nil || p.manifest == nil {
		return model, ""
	}
	cfg, err := p.resolver.ResolveForManifest(p.manifest)
	if err != nil {
		return model, ""
	}
	if cfg.Model != "" {
		model = cfg.Model
	}
	return model, cfg.Provider
}

// Compile-time guard that ProviderSummariser satisfies ctxstore.Summariser.
var _ ctxstore.Summariser = (*ProviderSummariser)(nil)
