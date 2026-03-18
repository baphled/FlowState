package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

type SearchContextTool struct {
	store    *FileContextStore
	embedder provider.Provider
	topK     int
}

func NewSearchContextTool(store *FileContextStore, embedder provider.Provider, topK int) *SearchContextTool {
	return &SearchContextTool{
		store:    store,
		embedder: embedder,
		topK:     topK,
	}
}

func (t *SearchContextTool) Name() string {
	return "search_context"
}

func (t *SearchContextTool) Description() string {
	return "Search conversation history semantically"
}

func (t *SearchContextTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Type: "object",
		Properties: map[string]tool.Property{
			"query": {Type: "string", Description: "Search query"},
		},
		Required: []string{"query"},
	}
}

func (t *SearchContextTool) Execute(ctx context.Context, input tool.ToolInput) (tool.ToolResult, error) {
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
		return tool.ToolResult{Output: ""}, nil
	}

	return tool.ToolResult{Output: formatMessages(extractMessages(results))}, nil
}

func (t *SearchContextTool) fallbackToRecent() (tool.ToolResult, error) {
	messages := t.store.GetRecent(t.topK)
	if len(messages) == 0 {
		return tool.ToolResult{Output: ""}, nil
	}
	return tool.ToolResult{Output: formatMessages(messages)}, nil
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

type GetMessagesTool struct {
	store *FileContextStore
}

func NewGetMessagesTool(store *FileContextStore) *GetMessagesTool {
	return &GetMessagesTool{store: store}
}

func (t *GetMessagesTool) Name() string {
	return "get_messages"
}

func (t *GetMessagesTool) Description() string {
	return "Retrieve messages by range or recent count"
}

func (t *GetMessagesTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Type: "object",
		Properties: map[string]tool.Property{
			"count": {Type: "integer", Description: "Number of recent messages to retrieve"},
			"start": {Type: "integer", Description: "Start index for range"},
			"end":   {Type: "integer", Description: "End index for range"},
		},
		Required: []string{},
	}
}

func (t *GetMessagesTool) Execute(_ context.Context, input tool.ToolInput) (tool.ToolResult, error) {
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

	return tool.ToolResult{Output: formatMessages(messages)}, nil
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

type SummarizeContextTool struct {
	store    *FileContextStore
	provider provider.Provider
	maxDepth int
	counter  TokenCounter
	model    string
}

func NewSummarizeContextTool(store *FileContextStore, p provider.Provider, maxDepth int, counter TokenCounter, model string) *SummarizeContextTool {
	return &SummarizeContextTool{
		store:    store,
		provider: p,
		maxDepth: maxDepth,
		counter:  counter,
		model:    model,
	}
}

func (t *SummarizeContextTool) Name() string {
	return "summarize_context"
}

func (t *SummarizeContextTool) Description() string {
	return "Recursively summarize conversation history"
}

func (t *SummarizeContextTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
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

func (t *SummarizeContextTool) Execute(ctx context.Context, input tool.ToolInput) (tool.ToolResult, error) {
	focus, _ := input.Arguments["focus"].(string) //nolint:errcheck // type assertion without error is intentional
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
		return tool.ToolResult{Output: "No conversation history"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return t.summarize(ctx, messages, focus, depth)
}

func (t *SummarizeContextTool) summarize(ctx context.Context, messages []provider.Message, focus string, depth int) (tool.ToolResult, error) {
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
		return tool.ToolResult{Output: content}, nil
	}

	summary := resp.Message.Content
	summaryTokens := t.counter.Count(summary)

	if float64(summaryTokens) >= float64(inputTokens)*0.9 {
		return tool.ToolResult{Output: summary}, nil
	}

	if depth > 1 {
		return t.summarize(ctx, []provider.Message{{Role: "assistant", Content: summary}}, focus, depth-1)
	}

	return tool.ToolResult{Output: summary}, nil
}

type ContextQueryTools struct {
	Search    *SearchContextTool
	GetMsgs   *GetMessagesTool
	Summarize *SummarizeContextTool
}

func NewContextQueryTools(store *FileContextStore, p provider.Provider, counter TokenCounter, summaryModel string) *ContextQueryTools {
	return &ContextQueryTools{
		Search:    NewSearchContextTool(store, p, 5),
		GetMsgs:   NewGetMessagesTool(store),
		Summarize: NewSummarizeContextTool(store, p, 2, counter, summaryModel),
	}
}
