package context

import (
	"github.com/pkoukk/tiktoken-go"
)

// DefaultModelContextFallback is the safety-net token cap used when a
// provider/model lookup cannot supply a concrete ContextLength. It
// supersedes the historical 4096 hardcode that quietly truncated ~70%
// of an 11-skill FlowState system prompt to fit. 16K comfortably fits
// the always-active skill bundle (skill bodies are 2-5K each, the
// default bundle is 11 skills) plus the agent's own system_prompt and
// delegation tables, while leaving room for conversation history. It
// is well within every provider in the FlowState support matrix —
// Anthropic (200K), OpenAI/Copilot/Gemini (128K), ZAI/OpenZen (128K+),
// and the Ollama families that report 32K-131K but where RULER shows
// quality drops past 32K (so 16K is the safe headroom for system
// prompt + conversation). Operators with hardware that warrants a
// different cap override via cfg.SystemPromptBudget (env:
// FLOWSTATE_SYSTEM_PROMPT_BUDGET).
const DefaultModelContextFallback = 16384

// TokenCounter defines methods for counting tokens in text.
type TokenCounter interface {
	// Count returns the number of tokens in the given text.
	Count(text string) int
	// ModelLimit returns the token limit for the given model.
	ModelLimit(model string) int
}

// FallbackSetter is implemented by token counters that allow operators
// to override the model-context fallback used when the resolver cannot
// supply a concrete ContextLength. App.New consults this interface so
// cfg.SystemPromptBudget propagates into the same code path the engine
// uses for ModelContextLimit. Implementations must accept a non-zero
// value and ignore zero/negative inputs (so callers can pass a
// possibly-unset config field without guarding the call site).
type FallbackSetter interface {
	SetFallback(limit int)
}

// ModelResolver resolves context window limits for provider models.
type ModelResolver interface {
	// ResolveContextLength returns the context window token limit for the given provider and model pair.
	ResolveContextLength(provider, model string) int
}

// TiktokenCounter counts tokens using the tiktoken library.
type TiktokenCounter struct {
	encoding string
	resolver ModelResolver
	provider string
	fallback int
}

// NewTiktokenCounter creates a new TiktokenCounter with the default cl100k_base encoding.
//
// Returns:
//   - A configured TiktokenCounter instance.
//
// Side effects:
//   - None.
func NewTiktokenCounter() *TiktokenCounter {
	return &TiktokenCounter{encoding: "cl100k_base", fallback: DefaultModelContextFallback}
}

// SetFallback overrides the model-context fallback returned when the
// configured resolver yields zero (unknown model, missing provider).
// Zero or negative inputs are ignored so callers may pass an unset
// config field without guarding the call. See DefaultModelContextFallback
// for the rationale on the shipped default.
//
// Expected:
//   - limit is the new fallback token cap; values <= 0 leave the
//     existing fallback untouched.
//
// Side effects:
//   - Mutates the receiver.
func (c *TiktokenCounter) SetFallback(limit int) {
	if limit <= 0 {
		return
	}
	c.fallback = limit
}

// NewTiktokenCounterWithResolver creates a new TiktokenCounter with a ModelResolver
// and provider name for dynamic context limit resolution.
//
// Expected:
//   - resolver is non-nil and provider is non-empty for dynamic resolution.
//
// Returns:
//   - A configured TiktokenCounter instance.
//
// Side effects:
//   - None.
func NewTiktokenCounterWithResolver(resolver ModelResolver, provider string) *TiktokenCounter {
	return &TiktokenCounter{
		encoding: "cl100k_base",
		resolver: resolver,
		provider: provider,
		fallback: DefaultModelContextFallback,
	}
}

// Count returns the number of tokens in the text using tiktoken encoding.
//
// Expected:
//   - text is the string to tokenise.
//
// Returns:
//   - The token count for the given text.
//
// Side effects:
//   - Falls back to approximate counting if the encoding fails to load.
func (c *TiktokenCounter) Count(text string) int {
	enc, err := tiktoken.GetEncoding(c.encoding)
	if err != nil {
		fallback := NewApproximateCounter()
		return fallback.Count(text)
	}
	return len(enc.Encode(text, nil, nil))
}

// ModelLimit returns the token limit for the given model name.
//
// Expected:
//   - model is a provider model identifier string.
//
// Returns:
//   - The maximum token limit for the specified model, resolved from the
//     configured ModelResolver if available, or the counter's configured
//     fallback (DefaultModelContextFallback by default; override via
//     SetFallback).
//
// Side effects:
//   - None.
func (c *TiktokenCounter) ModelLimit(model string) int {
	if c.resolver != nil && c.provider != "" {
		limit := c.resolver.ResolveContextLength(c.provider, model)
		if limit > 0 {
			return limit
		}
	}
	return resolveFallback(c.fallback)
}

// resolveFallback returns the configured fallback when set, falling
// back to DefaultModelContextFallback for zero-valued counters built
// before the fallback field existed.
//
// Expected:
//   - configured may be zero (unset) or a positive token cap.
//
// Returns:
//   - configured when it is positive; DefaultModelContextFallback otherwise.
//
// Side effects:
//   - None.
func resolveFallback(configured int) int {
	if configured > 0 {
		return configured
	}
	return DefaultModelContextFallback
}

// ApproximateCounter estimates token counts using character-based approximation.
type ApproximateCounter struct {
	resolver ModelResolver
	provider string
	fallback int
}

// NewApproximateCounter creates a new character-based approximate token counter.
//
// Returns:
//   - A configured ApproximateCounter instance.
//
// Side effects:
//   - None.
func NewApproximateCounter() *ApproximateCounter {
	return &ApproximateCounter{fallback: DefaultModelContextFallback}
}

// SetFallback overrides the model-context fallback returned when the
// configured resolver yields zero. Mirrors TiktokenCounter.SetFallback;
// see that method for semantics.
//
// Expected:
//   - limit is the new fallback token cap; values <= 0 leave the
//     existing fallback untouched.
//
// Side effects:
//   - Mutates the receiver.
func (c *ApproximateCounter) SetFallback(limit int) {
	if limit <= 0 {
		return
	}
	c.fallback = limit
}

// NewApproximateCounterWithResolver creates a new ApproximateCounter with a
// ModelResolver and provider name for dynamic context limit resolution.
//
// Expected:
//   - resolver is non-nil and provider is non-empty for dynamic resolution.
//
// Returns:
//   - A configured ApproximateCounter instance.
//
// Side effects:
//   - None.
func NewApproximateCounterWithResolver(resolver ModelResolver, provider string) *ApproximateCounter {
	return &ApproximateCounter{
		resolver: resolver,
		provider: provider,
		fallback: DefaultModelContextFallback,
	}
}

// Count returns an approximate token count for the given text using character-based estimation.
//
// Expected:
//   - text is the string to estimate tokens for.
//
// Returns:
//   - An approximate token count, minimum 1 for non-empty text.
//
// Side effects:
//   - None.
func (c *ApproximateCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	count := len(text) / 4
	if count == 0 {
		return 1
	}
	return count
}

// ModelLimit returns the token limit for the given model name.
//
// Expected:
//   - model is a provider model identifier string.
//
// Returns:
//   - The maximum token limit for the specified model, resolved from the
//     configured ModelResolver if available, or the counter's configured
//     fallback (DefaultModelContextFallback by default; override via
//     SetFallback).
//
// Side effects:
//   - None.
func (c *ApproximateCounter) ModelLimit(model string) int {
	if c.resolver != nil && c.provider != "" {
		limit := c.resolver.ResolveContextLength(c.provider, model)
		if limit > 0 {
			return limit
		}
	}
	return resolveFallback(c.fallback)
}

// TokenBudget tracks token allocation across categories against a total budget.
type TokenBudget struct {
	Total      int
	Used       int
	categories map[string]int
}

// NewTokenBudget creates a new token budget with the given total allocation.
//
// Expected:
//   - total is the maximum number of tokens available.
//
// Returns:
//   - A configured TokenBudget with zero usage.
//
// Side effects:
//   - None.
func NewTokenBudget(total int) *TokenBudget {
	return &TokenBudget{Total: total, categories: make(map[string]int)}
}

// Remaining returns the number of tokens still available in the budget.
//
// Returns:
//   - The difference between the total budget and tokens used.
//
// Side effects:
//   - None.
func (b *TokenBudget) Remaining() int {
	return b.Total - b.Used
}

// Reserve allocates tokens from the budget for the given category.
//
// Expected:
//   - category is a non-empty string identifying the reservation type.
//   - tokens is the number of tokens to allocate.
//
// Side effects:
//   - Increases the used token count and category-specific allocation.
func (b *TokenBudget) Reserve(category string, tokens int) {
	b.Used += tokens
	b.categories[category] += tokens
}

// CanFit reports whether the given number of tokens fits in the remaining budget.
//
// Expected:
//   - tokens is the number of tokens to check against the remaining budget.
//
// Returns:
//   - True if the tokens fit within the remaining budget.
//
// Side effects:
//   - None.
func (b *TokenBudget) CanFit(tokens int) bool {
	return b.Remaining() >= tokens
}

// Reset clears all token usage and category tracking.
//
// Side effects:
//   - Sets used tokens to zero and removes all category allocations.
func (b *TokenBudget) Reset() {
	b.Used = 0
	b.categories = make(map[string]int)
}

// UsedByCategory returns the tokens reserved for the given category.
//
// Expected:
//   - category is the name of the reservation category to query.
//
// Returns:
//   - The number of tokens allocated to the specified category.
//
// Side effects:
//   - None.
func (b *TokenBudget) UsedByCategory(category string) int {
	return b.categories[category]
}
