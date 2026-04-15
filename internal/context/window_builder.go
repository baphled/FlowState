package context

import (
	"context"
	"log"
	"log/slog"
	"strings"
	"sync"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tracer"
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
	// splitter, when non-nil, applies L1 micro-compaction to the recent
	// messages slice before they are appended to the window. When nil,
	// the existing skill-pair-aware path runs unchanged.
	splitter *HotColdSplitter
	// sessionMemory, when non-nil, causes buildInternal to prepend a
	// "[session memory]:" block after the system prompt so facts,
	// conventions, and preferences distilled in prior turns are visible
	// to the model. See WithSessionMemory for attachment semantics.
	sessionMemory *recall.SessionMemoryStore
	// metrics, when non-nil, is updated by the compression helpers and
	// logged via slog.Info at the end of every Build call. See
	// WithMetrics.
	metrics *CompressionMetrics
	// recorder, when non-nil, receives a context-window gauge update
	// (RecordContextWindowTokens) at the end of every Build call with
	// the assembled window's token count and the manifest agent ID.
	// Deployments that never enable Prometheus metrics pay no overhead
	// because the field stays nil. See WithRecorder.
	recorder tracer.Recorder
}

// NewWindowBuilder creates a new context window builder with the given token counter.
//
// Expected:
//   - counter is a non-nil TokenCounter implementation.
//
// Returns:
//   - A configured WindowBuilder instance with no L1 splitter attached.
//     Use WithSplitter to enable micro-compaction.
//
// Side effects:
//   - None.
func NewWindowBuilder(counter TokenCounter) *WindowBuilder {
	return &WindowBuilder{counter: counter, chainLocks: make(map[string]*sync.RWMutex)}
}

// WithSplitter attaches an L1 HotColdSplitter to the builder. Subsequent
// appendRecentMessages calls will route the recent slice through the
// splitter before assembly.
//
// Known race: this setter mutates a shared field on a builder that is
// reused across sessions. Correctness today depends on every caller
// holding engine.buildWindowMu around the WithSplitter→Build* pair
// (see internal/engine/engine.go:1682 "Attach-splitter-then-build must
// be a single critical section"). A future code path that bypasses
// buildWindowMu will race silently. M2 of the adversarial review
// proposed moving the splitter to a per-call BuildOption; the refactor
// touches seven public Build* signatures and is deferred for its own
// delivery rather than expanding the scope of the current Medium-fix
// cycle. See docs at the `splitter` field and the engine call site
// for the interim constraint.
//
// Follow-up M2 (deferred): replace this setter with a per-call
// BuildOption so the shared WindowBuilder can be used concurrently
// without the engine's external lock.
//
// Expected:
//   - splitter may be nil to detach a previously-attached splitter.
//
// Returns:
//   - The receiver, for chaining.
//
// Side effects:
//   - Mutates the builder. Callers MUST coordinate concurrent use.
func (b *WindowBuilder) WithSplitter(splitter *HotColdSplitter) *WindowBuilder {
	b.splitter = splitter
	return b
}

// WithMetrics attaches a *CompressionMetrics counter set to the
// builder. When non-nil, Build logs the current counter values via
// slog.Info so downstream log processors can scrape compression
// activity without touching the builder's internals.
//
// Expected:
//   - metrics may be nil to detach a previously-attached counter set.
//
// Returns:
//   - The receiver, for chaining.
//
// Side effects:
//   - Mutates the builder. Callers MUST coordinate concurrent use.
func (b *WindowBuilder) WithMetrics(metrics *CompressionMetrics) *WindowBuilder {
	b.metrics = metrics
	return b
}

// WithRecorder attaches a tracer.Recorder that receives the
// context-window gauge after every Build call. When non-nil, Build
// invokes RecordContextWindowTokens with the manifest agent ID and the
// assembled window's token count so operators can watch window size in
// real time alongside the existing Prometheus metrics.
//
// Expected:
//   - recorder may be nil to detach a previously-attached recorder.
//
// Returns:
//   - The receiver, for chaining.
//
// Side effects:
//   - Mutates the builder. Callers MUST coordinate concurrent use.
func (b *WindowBuilder) WithRecorder(recorder tracer.Recorder) *WindowBuilder {
	b.recorder = recorder
	return b
}

// WithSessionMemory attaches a SessionMemoryStore whose entries are
// prepended as a single assistant-role "[session memory]:" message
// immediately after the system prompt. Attaching a nil store detaches
// any previously-attached store.
//
// Expected:
//   - store may be nil to disable the feature.
//
// Returns:
//   - The receiver, for chaining.
//
// Side effects:
//   - Mutates the builder. Callers MUST coordinate concurrent use.
func (b *WindowBuilder) WithSessionMemory(store *recall.SessionMemoryStore) *WindowBuilder {
	b.sessionMemory = store
	return b
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

// ChainBuildOptions groups the inputs for building a context window with chain context.
type ChainBuildOptions struct {
	store       *recall.FileContextStore
	chainStore  recall.ChainContextStore
	tokenBudget int
}

// NewChainBuildOptions creates options for BuildContextWithChainResult.
//
// Expected:
//   - store may be nil or a valid FileContextStore.
//   - chainStore may be nil or a valid ChainContextStore.
//   - tokenBudget is the maximum token budget for the build.
//
// Returns:
//   - A ChainBuildOptions value containing the supplied inputs.
//
// Side effects:
//   - None.
func NewChainBuildOptions(store *recall.FileContextStore, chainStore recall.ChainContextStore, tokenBudget int) ChainBuildOptions {
	return ChainBuildOptions{store: store, chainStore: chainStore, tokenBudget: tokenBudget}
}

// BuildContextWithChainResult constructs a context window, appends chain context, and returns token usage.
//
// Expected:
//   - manifest is a non-nil agent manifest.
//   - opts contains the stores and token budget used for assembly.
//   - userMessage may be empty.
//
// Returns:
//   - A BuildResult containing the assembled messages and token usage.
//
// Side effects:
//   - Appends user content to the assembled messages when provided.
func (b *WindowBuilder) BuildContextWithChainResult(
	ctx context.Context,
	manifest *agent.Manifest,
	userMessage string,
	opts ChainBuildOptions,
) BuildResult {
	_ = ctx
	result := b.buildInternalWithChain(manifest, opts.store, opts.chainStore, opts.tokenBudget, buildOptions{logWarnings: true})

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
//
// Expected:
//   - manifest is a non-nil agent manifest.
//   - store is a valid FileContextStore.
//   - chainStore may be nil or a valid ChainContextStore.
//   - opts contains build flags and optional content.
//
// Returns:
//   - A BuildResult containing the assembled messages and token usage.
//
// Side effects:
//   - None.
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

	messages, budget = b.appendSessionMemory(messages, budget)

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
//
// Expected:
//   - messages and budget are valid build state.
//   - chainStore may be nil or a valid ChainContextStore.
//
// Returns:
//   - Updated messages and budget after appending chain context.
//
// Side effects:
//   - Reads from the chain store.
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
//
// Expected:
//   - chainID identifies the chain lock to retrieve.
//
// Returns:
//   - The RWMutex for the chain identifier.
//
// Side effects:
//   - Creates and caches a new lock when one does not already exist.
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

	messages, budget = b.appendSessionMemory(messages, budget)

	messages, budget = b.appendSummary(messages, budget, opts.summary)

	seenIDs := make(map[string]bool)
	messages, seenIDs, budget = b.appendSemanticResults(messages, seenIDs, budget, opts.semanticResults)

	state := &messageState{messages: messages, seenIDs: seenIDs, budget: budget, truncated: truncated}
	b.appendRecentMessages(store, manifest, state)
	messages = state.messages
	truncated = state.truncated

	b.logCompressionMetrics()
	b.emitContextWindowGauge(manifest, budget.Used)

	return BuildResult{
		Messages:        messages,
		TokensUsed:      budget.Used,
		BudgetRemaining: budget.Remaining(),
		Truncated:       truncated,
	}
}

// emitContextWindowGauge pushes the assembled window's token count into
// the attached tracer.Recorder as a context-window gauge observation.
// The agent label is sourced from the manifest so operators can
// distinguish windows built for different agents during the same run.
// No-op when no recorder has been attached via WithRecorder.
//
// Expected:
//   - manifest carries the agent ID used as the gauge label.
//   - tokensUsed is the final budget.Used value for the window.
//
// Side effects:
//   - Invokes recorder.RecordContextWindowTokens when the recorder is
//     non-nil; otherwise no-op.
func (b *WindowBuilder) emitContextWindowGauge(manifest *agent.Manifest, tokensUsed int) {
	if b.recorder == nil || manifest == nil {
		return
	}
	b.recorder.RecordContextWindowTokens(manifest.ID, tokensUsed)
}

// logCompressionMetrics emits a single slog.Info record naming the four
// compression counter keys whenever a CompressionMetrics has been
// attached via WithMetrics. Emitted once per Build so downstream log
// processors can scrape the counter deltas over a known cadence.
//
// Expected:
//   - None; the method is a no-op when b.metrics is nil so deployments
//     that never enable compression pay no log noise.
//
// Returns:
//   - None.
//
// Side effects:
//   - Writes one slog.Info record at default level when metrics are set.
func (b *WindowBuilder) logCompressionMetrics() {
	if b.metrics == nil {
		return
	}
	// A cache_hits key is intentionally omitted. Per ADR - View-Only
	// Context Compaction §3 the compacted-view cache is a permitted but
	// deferred extension; emitting a zero stub would mislead operators
	// into diagnosing a broken cache that does not exist.
	slog.Info("compression metrics",
		"micro_compaction_count", b.metrics.MicroCompactionCount,
		"auto_compaction_count", b.metrics.AutoCompactionCount,
		"tokens_saved", b.metrics.TokensSaved,
		"compression_overhead_tokens", b.metrics.OverheadTokens,
	)
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

// appendSessionMemory prepends a "[session memory]:" block immediately
// after the system prompt when a SessionMemoryStore is attached. The
// block aggregates up to 5 facts, 3 conventions, and 3 preferences
// (matching plan T16 guidance) retrieved via the store's Retrieve
// method. When no store is attached, or the store is empty, the method
// is a no-op.
//
// Expected:
//   - messages is the slice being assembled; the session memory block
//     is appended as a single assistant-role message so it reads as
//     agent-provided context rather than a user instruction.
//   - budget is the active TokenBudget; the block is only appended if
//     it fits under the "session_memory" category.
//
// Returns:
//   - The possibly-augmented message slice.
//   - The updated budget.
//
// Side effects:
//   - Reserves tokens under the "session_memory" category when the
//     block is appended.
func (b *WindowBuilder) appendSessionMemory(messages []provider.Message, budget *TokenBudget) ([]provider.Message, *TokenBudget) {
	if b.sessionMemory == nil {
		return messages, budget
	}
	facts := b.sessionMemory.Retrieve("fact", 5)
	conventions := b.sessionMemory.Retrieve("convention", 3)
	preferences := b.sessionMemory.Retrieve("preference", 3)
	if len(facts)+len(conventions)+len(preferences) == 0 {
		return messages, budget
	}

	var builder strings.Builder
	builder.WriteString("[session memory]:\n")
	for _, e := range facts {
		builder.WriteString("- fact: ")
		builder.WriteString(e.Content)
		builder.WriteString("\n")
	}
	for _, e := range conventions {
		builder.WriteString("- convention: ")
		builder.WriteString(e.Content)
		builder.WriteString("\n")
	}
	for _, e := range preferences {
		builder.WriteString("- preference: ")
		builder.WriteString(e.Content)
		builder.WriteString("\n")
	}

	content := builder.String()
	cost := b.counter.Count(content)
	if !budget.CanFit(cost) {
		return messages, budget
	}
	messages = append(messages, provider.Message{Role: "assistant", Content: content})
	budget.Reserve("session_memory", cost)
	return messages, budget
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

// isSkillLoadPair reports whether messages[i] and messages[i+1] form a skill_load tool call pair.
// A skill_load pair consists of an assistant message with a skill_load ToolCall followed
// immediately by a tool-role result message.
//
// Expected:
//   - i is a valid index into messages (0-based).
//   - messages has at least i+2 elements for a true result.
//
// Returns:
//   - true when messages[i] is an assistant message with ToolCalls[0].Name == "skill_load"
//     AND messages[i+1].Role == "tool".
//   - false otherwise.
//
// Side effects:
//   - None.
func isSkillLoadPair(messages []provider.Message, i int) bool {
	if i+1 >= len(messages) {
		return false
	}
	msg := messages[i]
	if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
		return false
	}
	if msg.ToolCalls[0].Name != "skill_load" {
		return false
	}
	return messages[i+1].Role == "tool"
}

// identifySkillPairIndices returns a set of message indices that belong to skill_load tool call pairs.
// Both the assistant message (with ToolCalls[0].Name="skill_load") and the following
// tool-result message are included.
//
// Expected:
//   - messages is a slice of provider.Message (may be empty).
//
// Returns:
//   - A map of indices that should be treated as skill tool results for eviction purposes.
//
// Side effects:
//   - None.
func (b *WindowBuilder) identifySkillPairIndices(messages []provider.Message) map[int]bool {
	skillIdx := make(map[int]bool)
	for i := 0; i < len(messages)-1; i++ {
		if isSkillLoadPair(messages, i) {
			skillIdx[i] = true
			skillIdx[i+1] = true
			i++
		}
	}
	return skillIdx
}

// appendRecentMessages appends recent messages from the store to the context window,
// preferentially evicting skill_load tool result pairs when the token budget is tight.
// Non-skill messages are processed first to guarantee their inclusion; remaining budget
// is then filled with skill pairs starting from the most recent.
//
// Expected:
//   - store is a non-nil recall.FileContextStore containing conversation messages.
//   - manifest is a non-nil agent manifest with context management settings.
//   - state is a non-nil messageState tracking the current window assembly.
//
// Returns:
//   - None.
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

	if b.splitter != nil {
		b.appendCompactedRecentMessages(recentMessages, state)
		return
	}

	recentIDs := b.getMessageIDs(store, len(recentMessages))
	skillPairIndices := b.identifySkillPairIndices(recentMessages)

	b.appendNonSkillMessages(recentMessages, recentIDs, skillPairIndices, state)
	b.appendSkillPairsWithBudget(recentMessages, recentIDs, state)
}

// appendCompactedRecentMessages routes the recent messages through the
// attached HotColdSplitter and appends the resulting hot+placeholder
// stream within the token budget.
//
// Expected:
//   - recentMessages is the unmodified slice from the recall store. Treated
//     as immutable: the splitter copies before transforming, per ADR -
//     View-Only Context Compaction.
//   - state is a non-nil messageState tracking the build.
//
// Returns:
//   - None.
//
// Side effects:
//   - Appends to state.messages and reserves tokens via state.budget under
//     the "sliding" and "compressed_micro" categories. Sets state.truncated
//     when any message is truncated to fit.
func (b *WindowBuilder) appendCompactedRecentMessages(
	recentMessages []provider.Message,
	state *messageState,
) {
	split := b.splitter.Split(recentMessages)

	if b.metrics != nil {
		b.metrics.MicroCompactionCount += len(split.ColdRecords)
	}

	for _, msg := range split.HotMessages {
		var msgTruncated bool
		state.messages, msgTruncated = b.appendMessageWithBudget(state.messages, msg, state.budget)
		state.truncated = state.truncated || msgTruncated
	}
}

// appendNonSkillMessages appends messages that are not part of skill_load pairs,
// preserving their original chronological order.
//
// Expected:
//   - messages is a slice of recent provider.Message instances.
//   - ids is a parallel slice of message ID strings.
//   - skillIndices identifies indices belonging to skill_load pairs.
//   - state is a non-nil messageState tracking the current window assembly.
//
// Returns:
//   - None.
//
// Side effects:
//   - Appends non-skill messages to state.messages within the token budget.
//   - Updates state.budget and state.truncated accordingly.
func (b *WindowBuilder) appendNonSkillMessages(
	messages []provider.Message,
	ids []string,
	skillIndices map[int]bool,
	state *messageState,
) {
	for i, msg := range messages {
		if skillIndices[i] {
			continue
		}
		msgID := b.getMessageIDAtIndex(ids, i)
		if state.seenIDs[msgID] {
			continue
		}
		var msgTruncated bool
		state.messages, msgTruncated = b.appendMessageWithBudget(state.messages, msg, state.budget)
		state.truncated = state.truncated || msgTruncated
	}
}

// appendSkillPairsWithBudget appends skill_load pairs to the context window in
// chronological order. Most recent pairs are given priority when the budget is
// tight, so older pairs are evicted first. Each pair is added atomically: both
// the assistant and tool messages must fit within the remaining budget or
// neither is added.
//
// Expected:
//   - messages is a slice of recent provider.Message instances.
//   - ids is a parallel slice of message ID strings.
//   - state is a non-nil messageState tracking the current window assembly.
//
// Returns:
//   - None.
//
// Side effects:
//   - Appends skill pair messages to state.messages in chronological order.
//   - Reserves tokens under the "skill-result" budget category.
func (b *WindowBuilder) appendSkillPairsWithBudget(
	messages []provider.Message,
	ids []string,
	state *messageState,
) {
	var pairStarts []int
	for i := 0; i < len(messages)-1; i++ {
		if isSkillLoadPair(messages, i) {
			pairStarts = append(pairStarts, i)
			i++
		}
	}

	var keptStarts []int
	for j := len(pairStarts) - 1; j >= 0; j-- {
		idx := pairStarts[j]
		assistantID := b.getMessageIDAtIndex(ids, idx)
		toolID := b.getMessageIDAtIndex(ids, idx+1)
		if state.seenIDs[assistantID] || state.seenIDs[toolID] {
			continue
		}
		pairTokens := b.counter.Count(messages[idx].Content) + b.counter.Count(messages[idx+1].Content)
		if !state.budget.CanFit(pairTokens) {
			continue
		}
		state.budget.Reserve("skill-result", b.counter.Count(messages[idx].Content))
		state.budget.Reserve("skill-result", b.counter.Count(messages[idx+1].Content))
		keptStarts = append([]int{idx}, keptStarts...)
	}

	for _, idx := range keptStarts {
		state.messages = append(state.messages, messages[idx], messages[idx+1])
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
