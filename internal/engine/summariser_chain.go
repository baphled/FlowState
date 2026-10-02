// Package engine provides the core FlowState orchestration engine.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// ErrSummariserUnavailable is returned when every entry in the
// SummariserChain fails. It is the loud, typed terminal state of the
// Phase 1 fallback design: callers must never treat a total chain
// failure as a silent no-op. Wrap information for each failed entry is
// available via the error chain and AttemptedProviders.
var ErrSummariserUnavailable = errors.New("summariser chain: all providers failed")

// SummariserStatus is the typed outcome of a SummariserChain run.
//
// Phase 1 of the compaction redesign defines three statuses:
//   - SummariserStatusOK — an entry produced a non-empty summary.
//   - SummariserStatusOverflow — reserved for Phase 2's drop-oldest
//     retry; Phase 1 surfaces context-length errors from the provider
//     as plain failures that walk the chain.
//   - SummariserStatusUnavailable — every entry failed; the caller
//     receives ErrSummariserUnavailable.
type SummariserStatus string

const (
	// SummariserStatusOK marks a successful summarisation.
	SummariserStatusOK SummariserStatus = "ok"
	// SummariserStatusOverflow marks a summarisation request that
	// exceeded the provider's context window.
	SummariserStatusOverflow SummariserStatus = "overflow"
	// SummariserStatusUnavailable marks a fully failed chain.
	SummariserStatusUnavailable SummariserStatus = "unavailable"
)

// SummariserChainEntry pins one (provider, model) hop in the
// SummariserChain. Provider must match a name registered in the
// provider registry; Model is the model identifier dispatched to that
// provider. An empty Model is dispatched as-is — the provider rejects
// it loudly rather than silently substituting.
type SummariserChainEntry struct {
	// Provider is the registry key (e.g. "zai", "anthropic", "ollama").
	Provider string `json:"provider" yaml:"provider"`
	// Model is the model identifier used in the ChatRequest.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
}

// SummariserChain is the ordered multi-provider summariser introduced
// in Phase 1 of the compaction redesign. It satisfies the
// ctxstore.Summariser interface and walks its entries in priority
// order: the configured summariser provider first (Z.AI by default
// when credentials are present), Anthropic second, and a local Ollama
// with a configurable model name as the last resort.
//
// A hop is considered failed when the provider is missing from the
// registry, the Chat call errors, or the response is empty — an empty
// summary is a failure, not a success, so the engine never dispatches
// a silent no-op. When every hop fails, Summarise returns an error
// wrapping ErrSummariserUnavailable and the caller degrades to the
// bounded truncation fallback with a WARN.
type SummariserChain struct {
	registry *provider.Registry
	entries  []SummariserChainEntry

	attempted []string
}

// NewSummariserChain constructs a chain over the given registry and
// ordered entries. A nil registry yields a chain whose every hop fails
// (entries are recorded as attempted) — configuration errors therefore
// surface through the standard ErrSummariserUnavailable path.
//
// Expected:
//   - registry may be nil; see note above.
//   - entries is the ordered fallback matrix, highest priority first.
//
// Returns:
//   - A SummariserChain. Never nil.
//
// Side effects:
//   - None.
func NewSummariserChain(registry *provider.Registry, entries []SummariserChainEntry) *SummariserChain {
	return &SummariserChain{registry: registry, entries: entries}
}

// WithEntries replaces the chain's ordered fallback matrix. Fluent
// setter for callers that construct the chain before configuration is
// resolved.
//
// Expected:
//   - entries is the ordered fallback matrix, highest priority first.
//
// Returns:
//   - The receiver for chaining. Never nil.
//
// Side effects:
//   - Mutates the receiver's entries field.
func (c *SummariserChain) WithEntries(entries []SummariserChainEntry) *SummariserChain {
	c.entries = entries
	return c
}

// Summarise walks the chain in order and returns the first non-empty
// summary. Each hop issues one Chat call with the system and user
// prompts threaded as separate messages.
//
// Expected:
//   - ctx carries cancellation/deadline for the remote calls.
//   - systemPrompt and userPrompt are the T8 prompts.
//   - msgs is unused; the rendered userPrompt already encodes it.
//
// Returns:
//   - The first non-empty model response on success.
//   - An error wrapping ErrSummariserUnavailable when every hop fails,
//     with one wrapped cause per attempted provider.
//
// Side effects:
//   - One Chat call per attempted provider, in priority order.
//   - Records the attempted provider names (see AttemptedProviders).
func (c *SummariserChain) Summarise(
	ctx context.Context,
	systemPrompt string,
	userPrompt string,
	_ []provider.Message,
) (string, error) {
	c.attempted = c.attempted[:0]
	causes := make([]error, 0, len(c.entries))
	for _, entry := range c.entries {
		p, err := c.lookup(entry.Provider)
		if err != nil {
			c.attempted = append(c.attempted, entry.Provider)
			causes = append(causes, fmt.Errorf("summariser chain: provider %q: %w", entry.Provider, err))
			continue
		}
		c.attempted = append(c.attempted, entry.Provider)
		resp, err := p.Chat(ctx, provider.ChatRequest{
			Provider: entry.Provider,
			Model:    entry.Model,
			Messages: []provider.Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: userPrompt},
			},
		})
		if err != nil {
			causes = append(causes, fmt.Errorf("summariser chain: provider %q chat: %w", entry.Provider, err))
			continue
		}
		if strings.TrimSpace(resp.Message.Content) == "" {
			causes = append(causes, fmt.Errorf("summariser chain: provider %q returned an empty summary", entry.Provider))
			continue
		}
		return resp.Message.Content, nil
	}
	return "", fmt.Errorf("%w: %s", ErrSummariserUnavailable, joinCauses(causes))
}

// AttemptedProviders returns the provider names tried by the most
// recent Summarise call, in attempt order. Empty before the first call.
//
// Returns:
//   - A copy of the attempted-provider slice; never nil after a call.
//
// Side effects:
//   - None.
func (c *SummariserChain) AttemptedProviders() []string {
	out := make([]string, len(c.attempted))
	copy(out, c.attempted)
	return out
}

// Status classifies an error returned by Summarise into the typed
// status vocabulary.
//
// Expected:
//   - err may be nil (the success case).
//
// Returns:
//   - SummariserStatusOK when err is nil.
//   - SummariserStatusUnavailable when err wraps
//     ErrSummariserUnavailable.
//   - SummariserStatusOverflow for any other error, letting Phase 2's
//     drop-oldest retry distinguish overflow-shaped failures once the
//     providers classify them.
//
// Side effects:
//   - None.
func (c *SummariserChain) Status(err error) SummariserStatus {
	if err == nil {
		return SummariserStatusOK
	}
	if errors.Is(err, ErrSummariserUnavailable) {
		return SummariserStatusUnavailable
	}
	return SummariserStatusOverflow
}

// lookup resolves a provider name against the chain's registry.
//
// Expected:
//   - name is a registry key such as "zai" or "anthropic".
//
// Returns:
//   - The registered provider, or a descriptive error when the
//     registry is nil or the name is unknown.
//
// Side effects:
//   - None.
func (c *SummariserChain) lookup(name string) (provider.Provider, error) {
	if c.registry == nil {
		return nil, fmt.Errorf("no provider registry wired")
	}
	return c.registry.Get(name)
}

// joinCauses renders per-provider failure causes for the terminal
// chain error. Single causes are rendered bare; multiple causes are
// semicolon-joined.
//
// Expected:
//   - causes is the per-hop failure slice collected by Summarise.
//
// Returns:
//   - A human-readable cause string, possibly empty.
//
// Side effects:
//   - None.
func joinCauses(causes []error) string {
	parts := make([]string, 0, len(causes))
	for _, cause := range causes {
		parts = append(parts, cause.Error())
	}
	return strings.Join(parts, "; ")
}

// Compile-time guard that SummariserChain satisfies ctxstore.Summariser.
var _ ctxstore.Summariser = (*SummariserChain)(nil)
