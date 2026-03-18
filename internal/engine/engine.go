package engine

import (
	"context"
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

		var responseContent strings.Builder

		for {
			select {
			case <-ctx.Done():
				outChan <- provider.StreamChunk{Error: ctx.Err(), Done: true}
				return
			case chunk, ok := <-providerChunks:
				if !ok {
					e.storeResponse(ctx, responseContent.String())
					return
				}
				responseContent.WriteString(chunk.Content)
				outChan <- chunk
				if chunk.Done {
					e.storeResponse(ctx, responseContent.String())
					return
				}
			}
		}
	}()

	return outChan, nil
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
