package context

import (
	"context"
	"log"
	"sync"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
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
	semanticResults []recall.SearchResult
	logWarnings     bool
}

// WindowBuilder constructs context windows for agent conversations.
type WindowBuilder struct {
	counter    TokenCounter
	chainLocks map[string]*sync.RWMutex
	chainMu    sync.Mutex
}

// NewWindowBuilder creates a new context window builder with the given token counter.
//
// Expected:
//   - counter is a non-nil TokenCounter implementation.
//
// Returns:
//   - A configured WindowBuilder instance.
//
// Side effects:
//   - None.
func NewWindowBuilder(counter TokenCounter) *WindowBuilder {
	return &WindowBuilder{counter: counter, chainLocks: make(map[string]*sync.RWMutex)}
}

// Build constructs a context window from the manifest and store within the token budget.
//
// Expected:
//   - manifest is a non-nil agent manifest with instructions and context settings.
//   - store is a non-nil recall.FileContextStore containing conversation messages.
//   - tokenBudget is the maximum number of tokens allowed in the window.
//
// Returns:
//   - A BuildResult containing the assembled messages and budget usage.
//
// Side effects:
//   - None.
func (b *WindowBuilder) Build(manifest *agent.Manifest, store *recall.FileContextStore, tokenBudget int) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, buildOptions{})
}

// BuildContext constructs a context window and appends the user message.
//
// Expected:
//   - ctx is a valid context.Context.
//   - manifest is a non-nil agent manifest.
//   - userMessage is the user's input text; may be empty.
//   - store is a non-nil recall.FileContextStore.
//   - tokenBudget is the maximum number of tokens allowed.
//
// Returns:
//   - A slice of messages forming the complete context window.
//
// Side effects:
//   - Logs a warning if the system prompt exceeds the token budget.
func (b *WindowBuilder) BuildContext(
	ctx context.Context,
	manifest *agent.Manifest,
	userMessage string,
	store *recall.FileContextStore,
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

// BuildContextResult constructs a context window and returns the full BuildResult including token counts.
//
// Expected:
//   - ctx is a valid context.Context.
//   - manifest is a non-nil agent manifest.
//   - userMessage is the user's input text; may be empty.
//   - store is a non-nil recall.FileContextStore.
//   - tokenBudget is the maximum number of tokens allowed.
//
// Returns:
//   - A BuildResult containing the assembled messages and budget usage including user message tokens.
//
// Side effects:
//   - Logs a warning if the system prompt exceeds the token budget.
func (b *WindowBuilder) BuildContextResult(
	ctx context.Context,
	manifest *agent.Manifest,
	userMessage string,
	store *recall.FileContextStore,
	tokenBudget int,
) BuildResult {
	_ = ctx
	result := b.buildInternal(manifest, store, tokenBudget, buildOptions{logWarnings: true})

	if userMessage != "" {
		userTokens := b.counter.Count(userMessage)
		result.Messages = append(result.Messages, provider.Message{
			Role:    "user",
			Content: userMessage,
		})
		result.TokensUsed += userTokens
		result.BudgetRemaining -= userTokens
	}

	return result
}

// BuildContextWithChainResult constructs a context window, appends chain context, and returns token usage.
func (b *WindowBuilder) BuildContextWithChainResult(
	ctx context.Context,
	manifest *agent.Manifest,
	userMessage string,
	store *recall.FileContextStore,
	chainStore recall.ChainContextStore,
	tokenBudget int,
) BuildResult {
	_ = ctx
	result := b.buildInternalWithChain(manifest, store, chainStore, tokenBudget, buildOptions{logWarnings: true})

	if userMessage != "" {
		userTokens := b.counter.Count(userMessage)
		result.Messages = append(result.Messages, provider.Message{Role: "user", Content: userMessage})
		result.TokensUsed += userTokens
		result.BudgetRemaining -= userTokens
	}

	return result
}

// BuildWithSummary constructs a context window including a conversation summary.
//
// Expected:
//   - manifest is a non-nil agent manifest.
//   - store is a non-nil recall.FileContextStore.
//   - tokenBudget is the maximum number of tokens allowed.
//   - summary is the conversation summary text to include.
//
// Returns:
//   - A BuildResult containing the assembled messages with the summary.
//
// Side effects:
//   - None.
func (b *WindowBuilder) BuildWithSummary(
	manifest *agent.Manifest,
	store *recall.FileContextStore,
	tokenBudget int,
	summary string,
) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, buildOptions{summary: summary})
}

// BuildWithSemanticResults constructs a context window including semantic search results.
//
// Expected:
//   - manifest is a non-nil agent manifest.
//   - store is a non-nil recall.FileContextStore.
//   - tokenBudget is the maximum number of tokens allowed.
//   - semanticResults is a slice of recall.SearchResult to include in the window.
//
// Returns:
//   - A BuildResult containing the assembled messages with semantic results.
//
// Side effects:
//   - None.
func (b *WindowBuilder) BuildWithSemanticResults(
	manifest *agent.Manifest,
	store *recall.FileContextStore,
	tokenBudget int,
	semanticResults []recall.SearchResult,
) BuildResult {
	return b.buildInternal(manifest, store, tokenBudget, buildOptions{semanticResults: semanticResults})
}

// buildInternalWithChain constructs a context window with chain context assembly.
func (b *WindowBuilder) buildInternalWithChain(
	manifest *agent.Manifest,
	store *recall.FileContextStore,
	chainStore recall.ChainContextStore,
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
	messages, budget = b.appendChainContext(messages, budget, chainStore)

	state := &messageState{messages: messages, seenIDs: seenIDs, budget: budget, truncated: truncated}
	b.appendRecentMessages(store, manifest, state)
	messages = state.messages
	truncated = state.truncated

	return BuildResult{Messages: messages, TokensUsed: budget.Used, BudgetRemaining: budget.Remaining(), Truncated: truncated}
}

// appendChainContext appends recent chain messages under a per-chain read lock.
func (b *WindowBuilder) appendChainContext(
	messages []provider.Message,
	budget *TokenBudget,
	chainStore recall.ChainContextStore,
) ([]provider.Message, *TokenBudget) {
	if chainStore == nil {
		return messages, budget
	}

	lock := b.chainLock(chainStore.ChainID())
	lock.RLock()
	defer lock.RUnlock()

	chainMessages, err := chainStore.GetByAgent("", 10)
	if err != nil {
		return messages, budget
	}

	for _, msg := range chainMessages {
		msgTokens := b.counter.Count(msg.Content)
		if budget.CanFit(msgTokens) {
			messages = append(messages, msg)
			budget.Reserve("chain", msgTokens)
		}
	}

	return messages, budget
}

// chainLock returns the per-chain RWMutex for the given chain identifier.
func (b *WindowBuilder) chainLock(chainID string) *sync.RWMutex {
	b.chainMu.Lock()
	defer b.chainMu.Unlock()

	if lock, ok := b.chainLocks[chainID]; ok {
		return lock
	}

	lock := &sync.RWMutex{}
	b.chainLocks[chainID] = lock
	return lock
}

// buildInternal constructs a context window with the given options, assembling system prompt,
// summary, semantic results, and recent messages.
//
// Expected:
//   - manifest is a non-nil agent manifest with instructions and context settings.
//   - store is a non-nil recall.FileContextStore containing conversation messages.
//   - tokenBudget is the maximum number of tokens allowed in the window.
//   - opts contains optional summary, semantic results, and logging configuration.
//
// Returns:
//   - A BuildResult containing the assembled messages and budget usage.
//
// Side effects:
//   - None.
func (b *WindowBuilder) buildInternal(
	manifest *agent.Manifest,
	store *recall.FileContextStore,
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

// prepareSystemPrompt extracts and optionally truncates the system prompt to fit within the token budget.
//
// Expected:
//   - manifest is a non-nil agent manifest with instructions.
//   - tokenBudget is the maximum number of tokens allowed for the system prompt.
//   - logWarnings indicates whether to log truncation warnings.
//
// Returns:
//   - The system prompt (possibly truncated).
//   - A boolean indicating whether truncation occurred.
//
// Side effects:
//   - Logs a warning if truncation occurs and logWarnings is true.
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

// appendSummary appends a conversation summary to the message list if it fits within the token budget.
//
// Expected:
//   - messages is a slice of provider.Message to append to.
//   - budget is a non-nil TokenBudget tracking token allocation.
//   - summary is the summary text; may be empty.
//
// Returns:
//   - The updated message slice (with summary appended if it fits).
//   - The updated budget.
//
// Side effects:
//   - Reserves tokens in the budget if the summary is appended.
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

// appendSemanticResults appends semantic search results to the message list, tracking seen message IDs.
//
// Expected:
//   - messages is a slice of provider.Message to append to.
//   - seenIDs is a map tracking message IDs already included in the window.
//   - budget is a non-nil TokenBudget tracking token allocation.
//   - semanticResults is a slice of recall.SearchResult to append.
//
// Returns:
//   - The updated message slice.
//   - The updated seenIDs map.
//   - The updated budget.
//
// Side effects:
//   - Reserves tokens in the budget for each appended message.
//   - Marks appended message IDs as seen.
func (b *WindowBuilder) appendSemanticResults(
	messages []provider.Message,
	seenIDs map[string]bool,
	budget *TokenBudget,
	semanticResults []recall.SearchResult,
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

// appendRecentMessages appends recent messages from the store to the context window, respecting the token budget.
//
// Expected:
//   - store is a non-nil recall.FileContextStore containing conversation messages.
//   - manifest is a non-nil agent manifest with context management settings.
//   - state is a non-nil messageState tracking the current window assembly.
//
// Side effects:
//   - Appends messages to state.messages.
//   - Updates state.budget with token reservations.
//   - Sets state.truncated if any message is truncated.
//   - Skips messages already seen in the window.
func (b *WindowBuilder) appendRecentMessages(
	store *recall.FileContextStore,
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

// getMessageIDAtIndex retrieves the message ID at the given index from a slice of IDs.
//
// Expected:
//   - recentIDs is a slice of message ID strings.
//   - i is the index to retrieve.
//
// Returns:
//   - The message ID at index i, or an empty string if the index is out of bounds.
//
// Side effects:
//   - None.
func (b *WindowBuilder) getMessageIDAtIndex(recentIDs []string, i int) string {
	if i < len(recentIDs) {
		return recentIDs[i]
	}
	return ""
}

// appendMessageWithBudget appends a message to the list, truncating if necessary to fit the token budget.
//
// Expected:
//   - messages is a slice of provider.Message to append to.
//   - msg is the provider.Message to append.
//   - budget is a non-nil TokenBudget tracking token allocation.
//
// Returns:
//   - The updated message slice.
//   - A boolean indicating whether the message was truncated.
//
// Side effects:
//   - Reserves tokens in the budget for the appended message.
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

// getMessageIDs retrieves the message IDs for the most recent count messages from the store.
//
// Expected:
//   - store is a non-nil recall.FileContextStore containing messages.
//   - count is the number of recent message IDs to retrieve.
//
// Returns:
//   - A slice of message ID strings for the most recent count messages.
//
// Side effects:
//   - None.
func (b *WindowBuilder) getMessageIDs(store *recall.FileContextStore, count int) []string {
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

// truncateToFit truncates text to fit within a maximum token count using character-based estimation.
//
// Expected:
//   - text is the string to truncate.
//   - maxTokens is the maximum number of tokens allowed.
//
// Returns:
//   - The original text if it fits within maxTokens.
//   - A truncated substring if the text exceeds the token limit.
//   - An empty string if maxTokens is zero or negative.
//
// Side effects:
//   - None.
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
