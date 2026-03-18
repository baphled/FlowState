package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

const (
	streamBufferSize     = 16
	defaultStreamTimeout = 60 * time.Second
)

type Engine struct {
	chatProvider      provider.Provider
	embeddingProvider provider.Provider
	failbackChain     *provider.FailbackChain
	manifest          agent.AgentManifest
	tools             []tool.Tool
	skills            []skill.Skill
	store             *ctxstore.FileContextStore
	windowBuilder     *ctxstore.ContextWindowBuilder
	tokenCounter      ctxstore.TokenCounter
	streamTimeout     time.Duration
	hookChain         *hook.Chain
}

type Config struct {
	ChatProvider      provider.Provider
	EmbeddingProvider provider.Provider
	Registry          *provider.Registry
	Manifest          agent.AgentManifest
	Tools             []tool.Tool
	Skills            []skill.Skill
	Store             *ctxstore.FileContextStore
	TokenCounter      ctxstore.TokenCounter
	StreamTimeout     time.Duration
	HookChain         *hook.Chain
}

func New(cfg Config) *Engine {
	var windowBuilder *ctxstore.ContextWindowBuilder
	if cfg.TokenCounter != nil {
		windowBuilder = ctxstore.NewContextWindowBuilder(cfg.TokenCounter)
	}

	timeout := cfg.StreamTimeout
	if timeout == 0 {
		timeout = defaultStreamTimeout
	}

	var failbackChain *provider.FailbackChain
	if cfg.Registry != nil {
		prefs := buildModelPreferences(cfg.Manifest)
		failbackChain = provider.NewFailbackChain(cfg.Registry, prefs, timeout)
	}

	return &Engine{
		chatProvider:      cfg.ChatProvider,
		embeddingProvider: cfg.EmbeddingProvider,
		failbackChain:     failbackChain,
		manifest:          cfg.Manifest,
		tools:             cfg.Tools,
		skills:            cfg.Skills,
		store:             cfg.Store,
		windowBuilder:     windowBuilder,
		tokenCounter:      cfg.TokenCounter,
		streamTimeout:     timeout,
		hookChain:         cfg.HookChain,
	}
}

func buildModelPreferences(manifest agent.AgentManifest) []provider.ModelPreference {
	complexity := manifest.Complexity
	if complexity == "" {
		complexity = "standard"
	}

	prefs, ok := manifest.ModelPreferences[complexity]
	if !ok || len(prefs) == 0 {
		return nil
	}

	result := make([]provider.ModelPreference, len(prefs))
	for i, p := range prefs {
		result[i] = provider.ModelPreference{
			Provider: p.Provider,
			Model:    p.Model,
		}
	}
	return result
}

func (e *Engine) LastProvider() string {
	if e.failbackChain != nil {
		return e.failbackChain.LastProvider()
	}
	if e.chatProvider != nil {
		return e.chatProvider.Name()
	}
	return ""
}

func (e *Engine) BuildSystemPrompt() string {
	var builder strings.Builder
	builder.WriteString(e.manifest.Instructions.SystemPrompt)

	for _, skillName := range e.manifest.Capabilities.AlwaysActiveSkills {
		for i := range e.skills {
			if e.skills[i].Name == skillName && e.skills[i].Content != "" {
				builder.WriteString("\n\n")
				builder.WriteString(e.skills[i].Content)
			}
		}
	}

	return builder.String()
}

func (e *Engine) buildToolSchemas() []provider.Tool {
	tools := make([]provider.Tool, 0, len(e.tools))
	for _, t := range e.tools {
		schema := t.Schema()
		props := make(map[string]interface{})
		for k, v := range schema.Properties {
			props[k] = map[string]interface{}{
				"type":        v.Type,
				"description": v.Description,
			}
			if len(v.Enum) > 0 {
				props[k].(map[string]interface{})["enum"] = v.Enum
			}
		}
		tools = append(tools, provider.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Schema: provider.ToolSchema{
				Type:       schema.Type,
				Properties: props,
				Required:   schema.Required,
			},
		})
	}
	return tools
}

func (e *Engine) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	_ = agentID

	userMsg := provider.Message{Role: "user", Content: message}
	if e.store != nil {
		e.store.Append(userMsg)
		e.embedMessage(ctx, message)
	}

	messages := e.buildContextWindow(ctx, message)

	providerChunks, err := e.streamFromProvider(ctx, provider.ChatRequest{
		Messages: messages,
		Tools:    e.buildToolSchemas(),
	})
	if err != nil {
		return nil, err
	}

	outChan := make(chan provider.StreamChunk, streamBufferSize)

	go func() {
		defer close(outChan)
		e.streamWithToolLoop(ctx, messages, providerChunks, outChan)
	}()

	return outChan, nil
}

func (e *Engine) streamFromProvider(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	handler := e.baseStreamHandler()
	if e.hookChain != nil {
		handler = e.hookChain.Execute(handler)
	}
	return handler(ctx, &req)
}

func (e *Engine) baseStreamHandler() hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		if e.failbackChain != nil {
			return e.failbackChain.Stream(ctx, *req)
		}
		return e.chatProvider.Stream(ctx, *req)
	}
}

func (e *Engine) streamWithToolLoop(
	ctx context.Context, messages []provider.Message,
	providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk,
) {
	for {
		toolCall, responseContent, done := e.processStreamChunks(ctx, providerChunks, outChan)
		if done {
			e.storeResponse(ctx, responseContent)
			return
		}

		if toolCall == nil {
			e.storeResponse(ctx, responseContent)
			return
		}

		toolResult, err := e.executeToolCall(ctx, toolCall)
		if err != nil {
			outChan <- provider.StreamChunk{Error: err, Done: true}
			return
		}

		e.storeToolResult(toolCall.ID, toolResult)

		messages = e.appendToolResultToMessages(messages, toolCall, toolResult)

		var streamErr error
		providerChunks, streamErr = e.streamFromProvider(ctx, provider.ChatRequest{
			Messages: messages,
			Tools:    e.buildToolSchemas(),
		})
		if streamErr != nil {
			outChan <- provider.StreamChunk{Error: streamErr, Done: true}
			return
		}
	}
}

func (e *Engine) processStreamChunks(
	ctx context.Context, providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk,
) (*provider.ToolCall, string, bool) {
	var responseContent strings.Builder

	for {
		select {
		case <-ctx.Done():
			outChan <- provider.StreamChunk{Error: ctx.Err(), Done: true}
			return nil, responseContent.String(), true
		case chunk, ok := <-providerChunks:
			if !ok {
				return nil, responseContent.String(), false
			}

			if chunk.EventType == "tool_call" && chunk.ToolCall != nil {
				return chunk.ToolCall, responseContent.String(), false
			}

			responseContent.WriteString(chunk.Content)
			outChan <- chunk

			if chunk.Done {
				return nil, responseContent.String(), true
			}
		}
	}
}

func (e *Engine) executeToolCall(ctx context.Context, toolCall *provider.ToolCall) (tool.ToolResult, error) {
	for _, t := range e.tools {
		if t.Name() == toolCall.Name {
			input := tool.ToolInput{
				Name:      toolCall.Name,
				Arguments: toolCall.Arguments,
			}
			result, err := t.Execute(ctx, input)
			if err != nil {
				return tool.ToolResult{Output: "", Error: err}, nil //nolint:nilerr // Error captured in ToolResult.Error
			}
			return result, nil
		}
	}
	return tool.ToolResult{}, fmt.Errorf("tool not found: %s", toolCall.Name)
}

func (e *Engine) storeToolResult(toolCallID string, result tool.ToolResult) {
	if e.store == nil {
		return
	}

	content := result.Output
	if result.Error != nil {
		content = result.Error.Error()
	}

	e.store.Append(provider.Message{
		Role:    "tool",
		Content: content,
		ToolCalls: []provider.ToolCall{
			{ID: toolCallID},
		},
	})
}

func (e *Engine) appendToolResultToMessages(
	messages []provider.Message, toolCall *provider.ToolCall, result tool.ToolResult,
) []provider.Message {
	content := result.Output
	if result.Error != nil {
		content = "Error: " + result.Error.Error()
	}

	toolResultMsg := provider.Message{
		Role:    "tool",
		Content: content,
		ToolCalls: []provider.ToolCall{
			{ID: toolCall.ID, Name: toolCall.Name},
		},
	}

	return append(messages, toolResultMsg)
}

func (e *Engine) buildContextWindow(ctx context.Context, userMessage string) []provider.Message {
	if e.windowBuilder == nil || e.store == nil {
		systemPrompt := e.BuildSystemPrompt()
		return []provider.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		}
	}

	tokenBudget := 4096
	if e.tokenCounter != nil {
		tokenBudget = e.tokenCounter.ModelLimit("")
	}

	return e.windowBuilder.BuildContext(ctx, &e.manifest, userMessage, e.store, tokenBudget)
}

func (e *Engine) embedMessage(ctx context.Context, content string) {
	if e.embeddingProvider == nil {
		return
	}

	_, err := e.embeddingProvider.Embed(ctx, provider.EmbedRequest{Input: content})
	if err != nil {
		return
	}
}

func (e *Engine) storeResponse(ctx context.Context, content string) {
	if e.store == nil || content == "" {
		return
	}

	e.store.Append(provider.Message{Role: "assistant", Content: content})
	e.embedMessage(ctx, content)
}

// SetContextStore sets the context store for session persistence.
func (e *Engine) SetContextStore(store *ctxstore.FileContextStore) {
	e.store = store
}

// ContextStore returns the current context store.
func (e *Engine) ContextStore() *ctxstore.FileContextStore {
	return e.store
}
