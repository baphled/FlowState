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

// Engine orchestrates AI agent interactions with providers, tools, and context management.
type Engine struct {
	chatProvider      provider.Provider
	embeddingProvider provider.Provider
	failbackChain     *provider.FailbackChain
	manifest          agent.Manifest
	tools             []tool.Tool
	skills            []skill.Skill
	store             *ctxstore.FileContextStore
	windowBuilder     *ctxstore.WindowBuilder
	tokenCounter      ctxstore.TokenCounter
	streamTimeout     time.Duration
	hookChain         *hook.Chain
	toolRegistry      *tool.Registry
	permissionHandler tool.PermissionHandler
}

// Config holds the configuration for creating a new Engine.
type Config struct {
	ChatProvider      provider.Provider
	EmbeddingProvider provider.Provider
	Registry          *provider.Registry
	Manifest          agent.Manifest
	Tools             []tool.Tool
	Skills            []skill.Skill
	Store             *ctxstore.FileContextStore
	TokenCounter      ctxstore.TokenCounter
	StreamTimeout     time.Duration
	HookChain         *hook.Chain
	ToolRegistry      *tool.Registry
	PermissionHandler tool.PermissionHandler
}

// New creates a new Engine from the given configuration.
//
// Expected:
//   - cfg contains at least a ChatProvider or a Registry for failback.
//
// Returns:
//   - A fully initialised Engine ready for streaming conversations.
//
// Side effects:
//   - None.
func New(cfg Config) *Engine {
	var windowBuilder *ctxstore.WindowBuilder
	if cfg.TokenCounter != nil {
		windowBuilder = ctxstore.NewWindowBuilder(cfg.TokenCounter)
	}

	timeout := cfg.StreamTimeout
	if timeout == 0 {
		timeout = defaultStreamTimeout
	}

	var failbackChain *provider.FailbackChain
	if cfg.Registry != nil {
		prefs := buildModelPreferences(cfg.Manifest)
		if len(prefs) > 0 {
			failbackChain = provider.NewFailbackChain(cfg.Registry, prefs, timeout)
		}
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
		toolRegistry:      cfg.ToolRegistry,
		permissionHandler: cfg.PermissionHandler,
	}
}

// buildModelPreferences constructs model preferences from the agent manifest's complexity tier.
//
// Expected:
//   - manifest contains a Complexity field and ModelPreferences mapping.
//
// Returns:
//   - A slice of ModelPreference values for the manifest's complexity tier, or nil if not found.
//
// Side effects:
//   - None.
func buildModelPreferences(manifest agent.Manifest) []provider.ModelPreference {
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

// LastProvider returns the name of the most recently used provider.
//
// Returns:
//   - The provider name string, or empty if no provider has been used.
//
// Side effects:
//   - None.
func (e *Engine) LastProvider() string {
	if e.failbackChain != nil {
		if p := e.failbackChain.LastProvider(); p != "" {
			return p
		}
		return e.failbackChain.DefaultProvider()
	}
	if e.chatProvider != nil {
		return e.chatProvider.Name()
	}
	return ""
}

// LastModel returns the model name used by the most recently active provider.
// Falls back to the first configured preference if no stream has run yet.
//
// Returns:
//   - The model name string, or empty string if no provider is configured.
//
// Side effects:
//   - None.
func (e *Engine) LastModel() string {
	if e.failbackChain != nil {
		if m := e.failbackChain.LastModel(); m != "" {
			return m
		}
		return e.failbackChain.DefaultModel()
	}
	return ""
}

// SetModelPreference updates the engine's model preference to prioritise the given provider and model.
//
// Expected:
//   - providerName is a non-empty string.
//   - modelName is a non-empty string.
//
// Side effects:
//   - Modifies the failback chain's preferences to use the specified model first.
func (e *Engine) SetModelPreference(providerName string, modelName string) {
	if e.failbackChain != nil {
		e.failbackChain.SetPreferences([]provider.ModelPreference{
			{Provider: providerName, Model: modelName},
		})
	}
}

// ListAvailableModels returns all available models from configured providers.
//
// Returns:
//   - A slice of available Model values from all providers.
//   - An error if model listing fails.
//
// Side effects:
//   - May make network calls to providers to fetch model lists.
func (e *Engine) ListAvailableModels() ([]provider.Model, error) {
	if e.failbackChain != nil {
		return e.failbackChain.ListModels()
	}
	if e.chatProvider != nil {
		return e.chatProvider.Models()
	}
	return nil, nil
}

// BuildSystemPrompt constructs the system prompt from the agent manifest and active skills.
//
// Returns:
//   - The concatenated system prompt string including always-active skill content.
//
// Side effects:
//   - None.
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

// buildToolSchemas converts the engine's tools into provider-compatible tool schemas.
//
// Returns:
//   - A slice of provider.Tool values with schema information for each tool.
//
// Side effects:
//   - None.
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
				if propMap, ok := props[k].(map[string]interface{}); ok {
					propMap["enum"] = v.Enum
				}
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

// Stream sends a message and returns a channel of streamed response chunks.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - agentID identifies the agent (currently unused, reserved for future routing).
//   - message is the user's input text.
//
// Returns:
//   - A channel of StreamChunk values containing the response.
//   - An error if the initial provider stream fails.
//
// Side effects:
//   - Appends the user message to the context store.
//   - Embeds the user message if an embedding provider is configured.
//   - Spawns a goroutine to process the stream and handle tool calls.
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

// streamFromProvider initiates a streaming chat request with the provider, applying any configured hooks.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - req contains the chat request with messages and tools.
//
// Returns:
//   - A channel of StreamChunk values from the provider.
//   - An error if the stream fails to initialise.
//
// Side effects:
//   - Executes hook chain if configured.
func (e *Engine) streamFromProvider(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	handler := e.baseStreamHandler()
	if e.hookChain != nil {
		handler = e.hookChain.Execute(handler)
	}
	return handler(ctx, &req)
}

// baseStreamHandler returns the base handler function for streaming chat requests.
//
// Returns:
//   - A hook.HandlerFunc that delegates to the failback chain or direct chat provider.
//
// Side effects:
//   - None.
func (e *Engine) baseStreamHandler() hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		if e.failbackChain != nil {
			return e.failbackChain.Stream(ctx, *req)
		}
		return e.chatProvider.Stream(ctx, *req)
	}
}

// streamWithToolLoop processes streaming chunks, handles tool calls, and loops until completion.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - messages contains the conversation history.
//   - providerChunks is a channel of chunks from the provider.
//   - outChan is the output channel for processed chunks.
//
// Side effects:
//   - Sends chunks to outChan.
//   - Executes tool calls and appends results to messages.
//   - Stores responses in the context store.
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

		if denied := e.checkToolPermission(toolCall, outChan); denied {
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

// processStreamChunks reads chunks from the provider stream until a tool call or completion.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - providerChunks is a channel of chunks from the provider.
//   - outChan is the output channel for forwarding chunks.
//
// Returns:
//   - A ToolCall if one was encountered, or nil.
//   - The accumulated response content as a string.
//   - A boolean indicating whether streaming is complete.
//
// Side effects:
//   - Forwards chunks to outChan.
//   - Sends error chunks if context is cancelled.
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

// executeToolCall finds and executes the specified tool with the given arguments.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - toolCall contains the tool name and arguments.
//
// Returns:
//   - A tool.Result with output or error.
//   - An error if the tool is not found.
//
// Side effects:
//   - Executes the tool, which may have its own side effects.
func (e *Engine) executeToolCall(ctx context.Context, toolCall *provider.ToolCall) (tool.Result, error) {
	for _, t := range e.tools {
		if t.Name() == toolCall.Name {
			input := tool.Input{
				Name:      toolCall.Name,
				Arguments: toolCall.Arguments,
			}
			result, err := t.Execute(ctx, input)
			result.Error = err
			return result, nil
		}
	}
	return tool.Result{}, fmt.Errorf("tool not found: %s", toolCall.Name)
}

// checkToolPermission verifies the tool has permission to execute.
//
// Expected:
//   - toolCall is the pending tool invocation.
//   - outChan is the output channel for error reporting.
//
// Returns:
//   - true if the tool was denied (caller should return), false to proceed.
//
// Side effects:
//   - Sends an error chunk to outChan if the tool is denied.
//   - Invokes the permission handler for Ask permission.
func (e *Engine) checkToolPermission(toolCall *provider.ToolCall, outChan chan<- provider.StreamChunk) bool {
	if e.toolRegistry == nil {
		return false
	}

	perm := e.toolRegistry.CheckPermission(toolCall.Name)

	switch perm {
	case tool.Allow:
		return false
	case tool.Deny:
		outChan <- provider.StreamChunk{
			Error: fmt.Errorf("tool %q denied by permission policy", toolCall.Name),
			Done:  true,
		}
		return true
	case tool.Ask:
		return e.handleAskPermission(toolCall, outChan)
	}

	return false
}

// handleAskPermission prompts the user for tool execution approval.
//
// Expected:
//   - toolCall is the pending tool invocation.
//   - outChan is the output channel for error reporting.
//
// Returns:
//   - true if denied (caller should return), false if approved.
//
// Side effects:
//   - Invokes the permission handler callback.
//   - Sends an error chunk to outChan if denied or handler is absent.
func (e *Engine) handleAskPermission(toolCall *provider.ToolCall, outChan chan<- provider.StreamChunk) bool {
	if e.permissionHandler == nil {
		outChan <- provider.StreamChunk{
			Error: fmt.Errorf("tool %q denied: no permission handler configured", toolCall.Name),
			Done:  true,
		}
		return true
	}

	req := tool.PermissionRequest{
		ToolName:  toolCall.Name,
		Arguments: toolCall.Arguments,
	}

	approved, err := e.permissionHandler(req)
	if err != nil || !approved {
		outChan <- provider.StreamChunk{
			Error: fmt.Errorf("tool %q denied by user", toolCall.Name),
			Done:  true,
		}
		return true
	}

	return false
}

// storeToolResult appends a tool result message to the context store.
//
// Expected:
//   - toolCallID is the identifier of the tool call.
//   - result contains the tool's output or error.
//
// Side effects:
//   - Appends a message to the context store if configured.
func (e *Engine) storeToolResult(toolCallID string, result tool.Result) {
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

// appendToolResultToMessages adds a tool result message to the message history.
//
// Expected:
//   - messages is the current conversation history.
//   - toolCall contains the tool call identifier and name.
//   - result contains the tool's output or error.
//
// Returns:
//   - A new message slice with the tool result appended.
//
// Side effects:
//   - None.
func (e *Engine) appendToolResultToMessages(
	messages []provider.Message, toolCall *provider.ToolCall, result tool.Result,
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

// buildContextWindow constructs the message window for the provider, including system prompt and history.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - userMessage is the current user input.
//
// Returns:
//   - A slice of messages including system prompt, history, and user message.
//
// Side effects:
//   - None.
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

// embedMessage sends the message content to the embedding provider if configured.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - content is the message text to embed.
//
// Side effects:
//   - Calls the embedding provider if configured; errors are silently ignored.
func (e *Engine) embedMessage(ctx context.Context, content string) {
	if e.embeddingProvider == nil {
		return
	}

	_, err := e.embeddingProvider.Embed(ctx, provider.EmbedRequest{Input: content})
	if err != nil {
		return
	}
}

// storeResponse appends the assistant's response to the context store and embeds it.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - content is the assistant's response text.
//
// Side effects:
//   - Appends a message to the context store if configured.
//   - Embeds the response if an embedding provider is configured.
func (e *Engine) storeResponse(ctx context.Context, content string) {
	if e.store == nil || content == "" {
		return
	}

	e.store.Append(provider.Message{Role: "assistant", Content: content})
	e.embedMessage(ctx, content)
}

// SetContextStore sets the context store for session persistence.
//
// Expected:
//   - store is a FileContextStore instance, or nil to clear the store.
//
// Side effects:
//   - Replaces the engine's current context store reference.
func (e *Engine) SetContextStore(store *ctxstore.FileContextStore) {
	e.store = store
}

// ContextStore returns the current context store.
//
// Returns:
//   - The FileContextStore currently attached to this engine, or nil.
//
// Side effects:
//   - None.
func (e *Engine) ContextStore() *ctxstore.FileContextStore {
	return e.store
}
