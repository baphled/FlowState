package context

import (
	"strings"

	"github.com/pkoukk/tiktoken-go"
)

// TokenCounter defines methods for counting tokens in text.
type TokenCounter interface {
	// Count returns the number of tokens in the given text.
	Count(text string) int
	// ModelLimit returns the token limit for the given model.
	ModelLimit(model string) int
}

// TiktokenCounter counts tokens using the tiktoken library.
type TiktokenCounter struct {
	encoding string
}

// NewTiktokenCounter creates a new TiktokenCounter with the default encoding.
func NewTiktokenCounter() *TiktokenCounter {
	return &TiktokenCounter{encoding: "cl100k_base"}
}

// Count returns the number of tokens in the text using tiktoken encoding.
func (c *TiktokenCounter) Count(text string) int {
	enc, err := tiktoken.GetEncoding(c.encoding)
	if err != nil {
		fallback := NewApproximateCounter()
		return fallback.Count(text)
	}
	return len(enc.Encode(text, nil, nil))
}

// ModelLimit returns the token limit for the given model.
func (c *TiktokenCounter) ModelLimit(model string) int {
	return modelLimit(model)
}

// ApproximateCounter estimates token counts without external dependencies.
// ApproximateCounter estimates token counts using character-based approximation.
type ApproximateCounter struct{}

// NewApproximateCounter creates a new ApproximateCounter.
// NewApproximateCounter creates a new approximate token counter.
func NewApproximateCounter() *ApproximateCounter {
	return &ApproximateCounter{}
}

// Count estimates the token count for the given text.
// Count returns an approximate token count for the given text.
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

// ModelLimit returns the context limit for the given model.
// ModelLimit returns the token limit for the given model.
func (c *ApproximateCounter) ModelLimit(model string) int {
	return modelLimit(model)
}

func modelLimit(model string) int {
	switch model {
	case "gpt-4o", "gpt-4o-mini", "gpt-4-turbo":
		return 128000
	case "gpt-3.5-turbo":
		return 16385
	}

	if strings.HasPrefix(model, "claude") {
		return 200000
	}

	if strings.HasPrefix(model, "llama") ||
		strings.HasPrefix(model, "mistral") ||
		strings.HasPrefix(model, "nomic") {
		return 4096
	}

	return 4096
}

// TokenBudget tracks token usage against a total budget.
// TokenBudget tracks token allocation across categories.
type TokenBudget struct {
	Total      int
	Used       int
	categories map[string]int
}

// NewTokenBudget creates a TokenBudget with the given total limit.
// NewTokenBudget creates a new token budget with the given total.
func NewTokenBudget(total int) *TokenBudget {
	return &TokenBudget{Total: total, categories: make(map[string]int)}
}

// Remaining returns the number of tokens remaining in the budget.
// Remaining returns the number of tokens still available.
func (b *TokenBudget) Remaining() int {
	return b.Total - b.Used
}

// Reserve allocates tokens from the budget for a category.
// Reserve allocates tokens for the given category.
func (b *TokenBudget) Reserve(category string, tokens int) {
	b.Used += tokens
	b.categories[category] += tokens
}

// CanFit returns true if the requested tokens fit in the remaining budget.
// CanFit reports whether the given number of tokens fits in the remaining budget.
func (b *TokenBudget) CanFit(tokens int) bool {
	return b.Remaining() >= tokens
}

// Reset clears all usage and category tracking.
// Reset clears all token reservations.
func (b *TokenBudget) Reset() {
	b.Used = 0
	b.categories = make(map[string]int)
}

// UsedByCategory returns the tokens used by a specific category.
// UsedByCategory returns the tokens reserved for the given category.
func (b *TokenBudget) UsedByCategory(category string) int {
	return b.categories[category]
}
