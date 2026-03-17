package context

import (
	"strings"

	"github.com/pkoukk/tiktoken-go"
)

type TokenCounter interface {
	Count(text string) int
	ModelLimit(model string) int
}

type TiktokenCounter struct {
	encoding string
}

func NewTiktokenCounter() *TiktokenCounter {
	return &TiktokenCounter{encoding: "cl100k_base"}
}

func (c *TiktokenCounter) Count(text string) int {
	enc, err := tiktoken.GetEncoding(c.encoding)
	if err != nil {
		fallback := NewApproximateCounter()
		return fallback.Count(text)
	}
	return len(enc.Encode(text, nil, nil))
}

func (c *TiktokenCounter) ModelLimit(model string) int {
	return modelLimit(model)
}

type ApproximateCounter struct{}

func NewApproximateCounter() *ApproximateCounter {
	return &ApproximateCounter{}
}

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

type TokenBudget struct {
	Total      int
	Used       int
	categories map[string]int
}

func NewTokenBudget(total int) *TokenBudget {
	return &TokenBudget{Total: total, categories: make(map[string]int)}
}

func (b *TokenBudget) Remaining() int {
	return b.Total - b.Used
}

func (b *TokenBudget) Reserve(category string, tokens int) {
	b.Used += tokens
	b.categories[category] += tokens
}

func (b *TokenBudget) CanFit(tokens int) bool {
	return b.Remaining() >= tokens
}

func (b *TokenBudget) Reset() {
	b.Used = 0
	b.categories = make(map[string]int)
}

func (b *TokenBudget) UsedByCategory(category string) int {
	return b.categories[category]
}
