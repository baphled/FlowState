package context

import (
	"github.com/pkoukk/tiktoken-go"
)

// TokenCounter defines methods for counting tokens in text.
type TokenCounter interface {
	// Count returns the number of tokens in the given text.
	Count(text string) int
	// ModelLimit returns the token limit for the given model.
	ModelLimit(model string) int
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
}

// NewTiktokenCounter creates a new TiktokenCounter with the default cl100k_base encoding.
//
// Returns:
//   - A configured TiktokenCounter instance.
//
// Side effects:
//   - None.
func NewTiktokenCounter() *TiktokenCounter {
	return &TiktokenCounter{encoding: "cl100k_base"}
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
	return &TiktokenCounter{encoding: "cl100k_base", resolver: resolver, provider: provider}
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
//     configured ModelResolver if available, or 4096 as a safe fallback.
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
	return 4096
}

// ApproximateCounter estimates token counts using character-based approximation.
type ApproximateCounter struct {
	resolver ModelResolver
	provider string
}

// NewApproximateCounter creates a new character-based approximate token counter.
//
// Returns:
//   - A configured ApproximateCounter instance.
//
// Side effects:
//   - None.
func NewApproximateCounter() *ApproximateCounter {
	return &ApproximateCounter{}
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
	return &ApproximateCounter{resolver: resolver, provider: provider}
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
//     configured ModelResolver if available, or 4096 as a safe fallback.
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
	return 4096
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
