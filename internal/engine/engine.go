package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

const streamBufferSize = 16

type Engine struct {
	chatProvider      provider.Provider
	embeddingProvider provider.Provider
	manifest          agent.AgentManifest
	tools             []tool.Tool
	skills            []skill.Skill
	store             *ctxstore.FileContextStore
	windowBuilder     *ctxstore.ContextWindowBuilder
	tokenCounter      ctxstore.TokenCounter
}

type Config struct {
	ChatProvider      provider.Provider
	EmbeddingProvider provider.Provider
	Manifest          agent.AgentManifest
	Tools             []tool.Tool
	Skills            []skill.Skill
	Store             *ctxstore.FileContextStore
	TokenCounter      ctxstore.TokenCounter
}

func New(cfg Config) *Engine {
	var windowBuilder *ctxstore.ContextWindowBuilder
	if cfg.TokenCounter != nil {
		windowBuilder = ctxstore.NewContextWindowBuilder(cfg.TokenCounter)
	}

	return &Engine{
		chatProvider:      cfg.ChatProvider,
		embeddingProvider: cfg.EmbeddingProvider,
		manifest:          cfg.Manifest,
		tools:             cfg.Tools,
		skills:            cfg.Skills,
		store:             cfg.Store,
		windowBuilder:     windowBuilder,
		tokenCounter:      cfg.TokenCounter,
	}
}

func (e *Engine) BuildSystemPrompt() string {
	var builder strings.Builder
	builder.WriteString(e.manifest.Instructions.SystemPrompt)

	for _, skillName := range e.manifest.Capabilities.AlwaysActiveSkills {
		for _, s := range e.skills {
			if s.Name == skillName && s.Content != "" {
				builder.WriteString("\n\n")
				builder.WriteString(s.Content)
			}
		}
	}

	return builder.String()
}

func (e *Engine) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	_ = agentID

	userMsg := provider.Message{Role: "user", Content: message}
	if e.store != nil {
		e.store.Append(userMsg)
		e.embedMessage(ctx, message)
	}

	messages := e.buildContextWindow(ctx, message)

	providerChunks, err := e.chatProvider.Stream(ctx, provider.ChatRequest{
		Messages: messages,
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

func (e *Engine) streamWithToolLoop(ctx context.Context, messages []provider.Message, providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk) {
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
		providerChunks, streamErr = e.chatProvider.Stream(ctx, provider.ChatRequest{
			Messages: messages,
		})
		if streamErr != nil {
			outChan <- provider.StreamChunk{Error: streamErr, Done: true}
			return
		}
	}
}

func (e *Engine) processStreamChunks(ctx context.Context, providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk) (*provider.ToolCall, string, bool) {
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
				return tool.ToolResult{Output: "", Error: err}, nil
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

func (e *Engine) appendToolResultToMessages(messages []provider.Message, toolCall *provider.ToolCall, result tool.ToolResult) []provider.Message {
	content := result.Output
	if result.Error != nil {
		content = fmt.Sprintf("Error: %s", result.Error.Error())
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
