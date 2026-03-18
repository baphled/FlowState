package context

import (
	"context"
	"log"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// BuildResult contains the outcome of building a context window.
type BuildResult struct {
	Messages        []provider.Message
	TokensUsed      int
	BudgetRemaining int
	Truncated       bool
}

// buildOptions configures the internal context window build.
type buildOptions struct {
	summary         string
	semanticResults []SearchResult
	logWarnings     bool
}

// WindowBuilder constructs context windows for agent conversations.
type WindowBuilder struct {
	counter TokenCounter
}

// NewWindowBuilder creates a new context window builder with the given token counter.
func NewWindowBuilder(counter TokenCounter) *WindowBuilder {
	return &WindowBuilder{counter: counter}
}

// Build constructs a context window from the manifest and store within the token budget.
func (b *WindowBuilder) Build(manifest *agent.Manifest, store *FileContextStore, tokenBudget int) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, buildOptions{})
}

// BuildContext constructs a context window and appends the user message.
func (b *WindowBuilder) BuildContext(
	ctx context.Context,
	manifest *agent.Manifest,
	userMessage string,
	store *FileContextStore,
	tokenBudget int,
) []provider.Message {
	_ = ctx
	result := b.buildInternal(manifest, store, tokenBudget, buildOptions{logWarnings: true})

	if userMessage != "" {
		result.Messages = append(result.Messages, provider.Message{
			Role:    "user",
			Content: userMessage,
		})
	}

	return result.Messages
}

// BuildWithSummary constructs a context window including a conversation summary.
func (b *WindowBuilder) BuildWithSummary(
	manifest *agent.Manifest,
	store *FileContextStore,
	tokenBudget int,
	summary string,
) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, buildOptions{summary: summary})
}

// BuildWithSemanticResults constructs a context window including semantic search results.
func (b *WindowBuilder) BuildWithSemanticResults(
	manifest *agent.Manifest,
	store *FileContextStore,
	tokenBudget int,
	semanticResults []SearchResult,
) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, buildOptions{semanticResults: semanticResults})
}

func (b *WindowBuilder) buildInternal(
	manifest *agent.Manifest,
	store *FileContextStore,
	tokenBudget int,
	opts buildOptions,
) BuildResult {
	budget := NewTokenBudget(tokenBudget)
	var messages []provider.Message
	truncated := false

	systemPrompt, systemTruncated := b.prepareSystemPrompt(manifest, tokenBudget, opts.logWarnings)
	truncated = truncated || systemTruncated
	messages = append(messages, provider.Message{Role: "system", Content: systemPrompt})
	budget.Reserve("system", b.counter.Count(systemPrompt))

	messages, budget = b.appendSummary(messages, budget, opts.summary)

	seenIDs := make(map[string]bool)
	messages, seenIDs, budget = b.appendSemanticResults(messages, seenIDs, budget, opts.semanticResults)

	state := &messageState{messages: messages, seenIDs: seenIDs, budget: budget, truncated: truncated}
	b.appendRecentMessages(store, manifest, state)
	messages = state.messages
	truncated = state.truncated

	return BuildResult{
		Messages:        messages,
		TokensUsed:      budget.Used,
		BudgetRemaining: budget.Remaining(),
		Truncated:       truncated,
	}
}

func (b *WindowBuilder) prepareSystemPrompt(manifest *agent.Manifest, tokenBudget int, logWarnings bool) (string, bool) {
	systemPrompt := manifest.Instructions.SystemPrompt
	systemTokens := b.counter.Count(systemPrompt)

	if systemTokens <= tokenBudget {
		return systemPrompt, false
	}

	if logWarnings {
		log.Printf("warning: system prompt truncated from %d to %d tokens (budget: %d)", systemTokens, tokenBudget, tokenBudget)
	}
	return b.truncateToFit(systemPrompt, tokenBudget), true
}

func (b *WindowBuilder) appendSummary(messages []provider.Message, budget *TokenBudget, summary string) ([]provider.Message, *TokenBudget) {
	if summary == "" {
		return messages, budget
	}
	summaryTokens := b.counter.Count(summary)
	if !budget.CanFit(summaryTokens) {
		return messages, budget
	}
	messages = append(messages, provider.Message{Role: "assistant", Content: summary})
	budget.Reserve("summary", summaryTokens)
	return messages, budget
}

func (b *WindowBuilder) appendSemanticResults(
	messages []provider.Message,
	seenIDs map[string]bool,
	budget *TokenBudget,
	semanticResults []SearchResult,
) ([]provider.Message, map[string]bool, *TokenBudget) {
	for _, sr := range semanticResults {
		msgTokens := b.counter.Count(sr.Message.Content)
		if budget.CanFit(msgTokens) {
			messages = append(messages, sr.Message)
			budget.Reserve("semantic", msgTokens)
			seenIDs[sr.MessageID] = true
		}
	}
	return messages, seenIDs, budget
}

// messageState tracks the state of message assembly during context window building.
type messageState struct {
	messages  []provider.Message
	seenIDs   map[string]bool
	budget    *TokenBudget
	truncated bool
}

func (b *WindowBuilder) appendRecentMessages(
	store *FileContextStore,
	manifest *agent.Manifest,
	state *messageState,
) {
	slidingWindowSize := manifest.ContextManagement.SlidingWindowSize
	if slidingWindowSize <= 0 {
		slidingWindowSize = 10
	}

	recentMessages := store.GetRecent(slidingWindowSize)
	recentIDs := b.getMessageIDs(store, len(recentMessages))

	for i, msg := range recentMessages {
		msgID := b.getMessageIDAtIndex(recentIDs, i)
		if state.seenIDs[msgID] {
			continue
		}

		var msgTruncated bool
		state.messages, msgTruncated = b.appendMessageWithBudget(state.messages, msg, state.budget)
		state.truncated = state.truncated || msgTruncated
	}
}

func (b *WindowBuilder) getMessageIDAtIndex(recentIDs []string, i int) string {
	if i < len(recentIDs) {
		return recentIDs[i]
	}
	return ""
}

func (b *WindowBuilder) appendMessageWithBudget(
	messages []provider.Message,
	msg provider.Message,
	budget *TokenBudget,
) ([]provider.Message, bool) {
	msgTokens := b.counter.Count(msg.Content)

	if budget.CanFit(msgTokens) {
		messages = append(messages, msg)
		budget.Reserve("sliding", msgTokens)
		return messages, false
	}

	if msgTokens == 0 {
		return messages, false
	}

	availableTokens := budget.Remaining()
	if availableTokens <= 0 {
		return messages, false
	}

	truncatedContent := b.truncateToFit(msg.Content, availableTokens)
	if truncatedContent == "" {
		return messages, false
	}

	messages = append(messages, provider.Message{Role: msg.Role, Content: truncatedContent})
	budget.Reserve("sliding", b.counter.Count(truncatedContent))
	return messages, true
}

func (b *WindowBuilder) getMessageIDs(store *FileContextStore, count int) []string {
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

func (b *WindowBuilder) truncateToFit(text string, maxTokens int) string {
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
