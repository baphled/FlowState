package context

import (
	"context"
	"log"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

type BuildResult struct {
	Messages        []provider.Message
	TokensUsed      int
	BudgetRemaining int
	Truncated       bool
}

type ContextWindowBuilder struct {
	counter TokenCounter
}

func NewContextWindowBuilder(counter TokenCounter) *ContextWindowBuilder {
	return &ContextWindowBuilder{counter: counter}
}

func (b *ContextWindowBuilder) Build(manifest *agent.AgentManifest, store *FileContextStore, tokenBudget int) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, "", nil, false)
}

func (b *ContextWindowBuilder) BuildContext(ctx context.Context, manifest *agent.AgentManifest, userMessage string, store *FileContextStore, tokenBudget int) []provider.Message {
	_ = ctx
	result := b.buildInternal(manifest, store, tokenBudget, "", nil, true)

	if userMessage != "" {
		result.Messages = append(result.Messages, provider.Message{
			Role:    "user",
			Content: userMessage,
		})
	}

	return result.Messages
}

func (b *ContextWindowBuilder) BuildWithSummary(manifest *agent.AgentManifest, store *FileContextStore, tokenBudget int, summary string) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, summary, nil, false)
}

func (b *ContextWindowBuilder) BuildWithSemanticResults(manifest *agent.AgentManifest, store *FileContextStore, tokenBudget int, semanticResults []SearchResult) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, "", semanticResults, false)
}

//nolint:gocognit,gocyclo // Context window building requires coordinating multiple concerns
func (b *ContextWindowBuilder) buildInternal(manifest *agent.AgentManifest, store *FileContextStore, tokenBudget int, summary string, semanticResults []SearchResult, logWarnings bool) BuildResult {
	budget := NewTokenBudget(tokenBudget)
	var messages []provider.Message
	truncated := false

	systemPrompt := manifest.Instructions.SystemPrompt
	systemTokens := b.counter.Count(systemPrompt)

	if systemTokens > tokenBudget {
		if logWarnings {
			log.Printf("warning: system prompt truncated from %d to %d tokens (budget: %d)", systemTokens, tokenBudget, tokenBudget)
		}
		systemPrompt = b.truncateToFit(systemPrompt, tokenBudget)
		truncated = true
		systemTokens = b.counter.Count(systemPrompt)
	}

	messages = append(messages, provider.Message{Role: "system", Content: systemPrompt})
	budget.Reserve("system", systemTokens)

	if summary != "" && budget.CanFit(b.counter.Count(summary)) {
		summaryTokens := b.counter.Count(summary)
		messages = append(messages, provider.Message{Role: "assistant", Content: summary})
		budget.Reserve("summary", summaryTokens)
	}

	seenIDs := make(map[string]bool)
	if len(semanticResults) > 0 {
		for _, sr := range semanticResults {
			msgTokens := b.counter.Count(sr.Message.Content)
			if budget.CanFit(msgTokens) {
				messages = append(messages, sr.Message)
				budget.Reserve("semantic", msgTokens)
				seenIDs[sr.MessageID] = true
			}
		}
	}

	slidingWindowSize := manifest.ContextManagement.SlidingWindowSize
	if slidingWindowSize <= 0 {
		slidingWindowSize = 10
	}

	recentMessages := store.GetRecent(slidingWindowSize)
	recentIDs := b.getMessageIDs(store, len(recentMessages))

	for i, msg := range recentMessages {
		msgID := ""
		if i < len(recentIDs) {
			msgID = recentIDs[i]
		}

		if seenIDs[msgID] {
			continue
		}

		msgTokens := b.counter.Count(msg.Content)
		//nolint:nestif // Token budget fitting logic requires these checks
		if budget.CanFit(msgTokens) {
			messages = append(messages, msg)
			budget.Reserve("sliding", msgTokens)
		} else if msgTokens > 0 {
			availableTokens := budget.Remaining()
			if availableTokens > 0 {
				truncatedContent := b.truncateToFit(msg.Content, availableTokens)
				if truncatedContent != "" {
					messages = append(messages, provider.Message{Role: msg.Role, Content: truncatedContent})
					budget.Reserve("sliding", b.counter.Count(truncatedContent))
					truncated = true
				}
			}
		}
	}

	return BuildResult{
		Messages:        messages,
		TokensUsed:      budget.Used,
		BudgetRemaining: budget.Remaining(),
		Truncated:       truncated,
	}
}

func (b *ContextWindowBuilder) getMessageIDs(store *FileContextStore, count int) []string {
	total := store.Count()
	start := total - count
	if start < 0 {
		start = 0
	}

	ids := make([]string, 0, count)
	for i := start; i < total; i++ {
		ids = append(ids, store.GetMessageID(i))
	}
	return ids
}

func (b *ContextWindowBuilder) truncateToFit(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}

	tokens := b.counter.Count(text)
	if tokens <= maxTokens {
		return text
	}

	ratio := float64(maxTokens) / float64(tokens)
	targetLen := int(float64(len(text)) * ratio * 0.9)

	if targetLen <= 0 {
		return ""
	}

	if targetLen >= len(text) {
		return text
	}

	return text[:targetLen]
}
