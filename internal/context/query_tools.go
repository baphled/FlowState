// Package context provides conversation context management including storage, retrieval, and semantic search.
package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// SearchContextTool searches conversation history semantically using embeddings.
// SearchContextTool provides semantic search over the context store.
type SearchContextTool struct {
	store    *FileContextStore
	embedder provider.Provider
	topK     int
}

// NewSearchContextTool creates a new SearchContextTool with the given store and embedder.
//
// Expected:
//   - store is a valid, non-nil FileContextStore.
//   - embedder is a valid Provider supporting embeddings.
//   - topK is a positive integer.
//
// Returns:
//   - A pointer to an initialised SearchContextTool.
//
// Side effects:
//   - None.
func NewSearchContextTool(store *FileContextStore, embedder provider.Provider, topK int) *SearchContextTool {
	return &SearchContextTool{
		store:    store,
		embedder: embedder,
		topK:     topK,
	}
}

// Name returns the tool name.
//
// Returns:
//   - The string "search_context".
//
// Side effects:
//   - None.
func (t *SearchContextTool) Name() string {
	return "search_context"
}

// Description returns a description of what the tool does.
//
// Returns:
//   - A human-readable description of the tool's purpose.
//
// Side effects:
//   - None.
func (t *SearchContextTool) Description() string {
	return "Search conversation history semantically"
}

// Schema returns the tool's input schema.
//
// Returns:
//   - A Schema describing the expected input format.
//
// Side effects:
//   - None.
func (t *SearchContextTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"query": {Type: "string", Description: "Search query"},
		},
		Required: []string{"query"},
	}
}

// Execute runs the semantic search against conversation history.
//
// Expected:
//   - ctx is a valid context.
//   - input contains a "query" string argument.
//
// Returns:
//   - A Result containing formatted matching messages.
//   - An error if the search fails.
//
// Side effects:
//   - Makes embedding API calls.
func (t *SearchContextTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	query, ok := input.Arguments["query"].(string)
	if !ok || query == "" {
		return t.fallbackToRecent()
	}

	vector, err := t.embedder.Embed(ctx, provider.EmbedRequest{
		Input: query,
		Model: t.store.model,
	})
	if err != nil {
		return t.fallbackToRecent()
	}

	results := t.store.Search(vector, t.topK)
	if len(results) == 0 {
		return tool.Result{Output: ""}, nil
	}

	return tool.Result{Output: formatMessages(extractMessages(results))}, nil
}

func (t *SearchContextTool) fallbackToRecent() (tool.Result, error) {
	messages := t.store.GetRecent(t.topK)
	if len(messages) == 0 {
		return tool.Result{Output: ""}, nil
	}
	return tool.Result{Output: formatMessages(messages)}, nil
}

func extractMessages(results []SearchResult) []provider.Message {
	messages := make([]provider.Message, len(results))
	for i, r := range results {
		messages[i] = r.Message
	}
	return messages
}

func formatMessages(messages []provider.Message) string {
	var parts []string
	for _, m := range messages {
		parts = append(parts, fmt.Sprintf("%s: %s", m.Role, m.Content))
	}
	return strings.Join(parts, "\n---\n")
}

// GetMessagesTool retrieves messages by range or recent count.
type GetMessagesTool struct {
	store *FileContextStore
}

// NewGetMessagesTool creates a new GetMessagesTool with the given store.
//
// Expected:
//   - store is a valid, non-nil FileContextStore.
//
// Returns:
//   - A pointer to an initialised GetMessagesTool.
//
// Side effects:
//   - None.
func NewGetMessagesTool(store *FileContextStore) *GetMessagesTool {
	return &GetMessagesTool{store: store}
}

// Name returns the tool name.
//
// Returns:
//   - The string "get_messages".
//
// Side effects:
//   - None.
func (t *GetMessagesTool) Name() string {
	return "get_messages"
}

// Description returns a description of what the tool does.
//
// Returns:
//   - A human-readable description of the tool's purpose.
//
// Side effects:
//   - None.
func (t *GetMessagesTool) Description() string {
	return "Retrieve messages by range or recent count"
}

// Schema returns the tool's input schema.
//
// Returns:
//   - A Schema describing the expected input format.
//
// Side effects:
//   - None.
func (t *GetMessagesTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"count": {Type: "integer", Description: "Number of recent messages to retrieve"},
			"start": {Type: "integer", Description: "Start index for range"},
			"end":   {Type: "integer", Description: "End index for range"},
		},
		Required: []string{},
	}
}

// Execute retrieves messages based on the input parameters.
//
// Expected:
//   - ctx is a valid context.
//   - input may contain "count", "start", and "end" integer arguments.
//
// Returns:
//   - A Result containing formatted messages.
//   - nil error (this tool does not fail).
//
// Side effects:
//   - None.
func (t *GetMessagesTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	count := extractInt(input.Arguments, "count", 0)
	start := extractInt(input.Arguments, "start", -1)
	end := extractInt(input.Arguments, "end", -1)

	var messages []provider.Message
	if count > 0 {
		messages = t.store.GetRecent(count)
	} else if start >= 0 && end >= 0 {
		messages = t.store.GetRange(start, end)
	} else {
		messages = t.store.GetRecent(10)
	}

	return tool.Result{Output: formatMessages(messages)}, nil
}

func extractInt(args map[string]interface{}, key string, defaultVal int) int {
	val, ok := args[key]
	if !ok {
		return defaultVal
	}
	floatVal, ok := val.(float64)
	if !ok {
		return defaultVal
	}
	return int(floatVal)
}

// SummarizeContextTool recursively summarizes conversation history.
type SummarizeContextTool struct {
	store    *FileContextStore
	provider provider.Provider
	maxDepth int
	counter  TokenCounter
	model    string
}

// NewSummarizeContextTool creates a new SummarizeContextTool with the given configuration.
//
// Expected:
//   - store is a valid, non-nil FileContextStore.
//   - p is a valid Provider supporting chat completions.
//   - maxDepth is a positive integer.
//   - counter is a valid TokenCounter.
//   - model is a non-empty model identifier.
//
// Returns:
//   - A pointer to an initialised SummarizeContextTool.
//
// Side effects:
//   - None.
func NewSummarizeContextTool(
	store *FileContextStore, p provider.Provider, maxDepth int, counter TokenCounter, model string,
) *SummarizeContextTool {
	return &SummarizeContextTool{
		store:    store,
		provider: p,
		maxDepth: maxDepth,
		counter:  counter,
		model:    model,
	}
}

// Name returns the tool name.
//
// Returns:
//   - The string "summarize_context".
//
// Side effects:
//   - None.
func (t *SummarizeContextTool) Name() string {
	return "summarize_context"
}

// Description returns a description of what the tool does.
//
// Returns:
//   - A human-readable description of the tool's purpose.
//
// Side effects:
//   - None.
func (t *SummarizeContextTool) Description() string {
	return "Recursively summarize conversation history"
}

// Schema returns the tool's input schema.
//
// Returns:
//   - A Schema describing the expected input format.
//
// Side effects:
//   - None.
func (t *SummarizeContextTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"focus": {Type: "string", Description: "Optional focus area for summarization"},
			"depth": {Type: "integer", Description: "Recursion depth (default 1)"},
			"start": {Type: "integer", Description: "Start index for range"},
			"end":   {Type: "integer", Description: "End index for range"},
		},
		Required: []string{},
	}
}

// Execute runs the summarize context tool.
//
// Expected:
//   - ctx is a valid context.
//   - input may contain "focus", "depth", "start", and "end" arguments.
//
// Returns:
//   - A Result containing the summary.
//   - An error if summarisation fails.
//
// Side effects:
//   - Makes LLM API calls.
func (t *SummarizeContextTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	focus, ok := input.Arguments["focus"].(string)
	if !ok {
		focus = ""
	}
	depth := extractInt(input.Arguments, "depth", 1)
	start := extractInt(input.Arguments, "start", -1)
	end := extractInt(input.Arguments, "end", -1)

	if depth > t.maxDepth {
		depth = t.maxDepth
	}
	if depth < 1 {
		depth = 1
	}

	var messages []provider.Message
	if start >= 0 && end >= 0 {
		messages = t.store.GetRange(start, end)
	} else {
		messages = t.store.AllMessages()
	}

	if len(messages) == 0 {
		return tool.Result{Output: "No conversation history"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return t.summarize(ctx, messages, focus, depth)
}

func (t *SummarizeContextTool) summarize(
	ctx context.Context, messages []provider.Message, focus string, depth int,
) (tool.Result, error) {
	content := formatMessages(messages)
	inputTokens := t.counter.Count(content)

	prompt := "Summarize the following conversation:\n\n" + content
	if focus != "" {
		prompt += "\n\nFocus on: " + focus
	}

	resp, err := t.provider.Chat(ctx, provider.ChatRequest{
		Model: t.model,
		Messages: []provider.Message{
			{Role: "system", Content: "You are a helpful assistant that summarizes conversations concisely."},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("summarizing context: %w", err)
	}

	summary := resp.Message.Content
	summaryTokens := t.counter.Count(summary)

	if float64(summaryTokens) >= float64(inputTokens)*0.9 {
		return tool.Result{Output: summary}, nil
	}

	if depth > 1 {
		return t.summarize(ctx, []provider.Message{{Role: "assistant", Content: summary}}, focus, depth-1)
	}

	return tool.Result{Output: summary}, nil
}

// QueryTools provides combined context query operations.
type QueryTools struct {
	Search    *SearchContextTool
	GetMsgs   *GetMessagesTool
	Summarize *SummarizeContextTool
}

// NewContextQueryTools creates a new context query tools instance.
//
// Expected:
//   - store is a valid, non-nil FileContextStore.
//   - p is a valid Provider.
//   - counter is a valid TokenCounter.
//   - summaryModel is a non-empty model identifier.
//
// Returns:
//   - A pointer to an initialised QueryTools.
//
// Side effects:
//   - None.
func NewContextQueryTools(store *FileContextStore, p provider.Provider, counter TokenCounter, summaryModel string) *QueryTools {
	return &QueryTools{
		Search:    NewSearchContextTool(store, p, 5),
		GetMsgs:   NewGetMessagesTool(store),
		Summarize: NewSummarizeContextTool(store, p, 2, counter, summaryModel),
	}
}
