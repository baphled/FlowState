package engine

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/prompt"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

const (
	streamBufferSize     = 16
	defaultStreamTimeout = 5 * time.Minute
)

// Engine orchestrates AI agent interactions with providers, tools, and context management.
type Engine struct {
	chatProvider      provider.Provider
	embeddingProvider provider.Provider
	failbackChain     *provider.FailbackChain
	manifest          agent.Manifest
	tools             []tool.Tool
	skills            []skill.Skill
	store             *recall.FileContextStore
	chainStore        recall.ChainContextStore
	windowBuilder     *ctxstore.WindowBuilder
	tokenCounter      ctxstore.TokenCounter
	streamTimeout     time.Duration
	hookChain         *hook.Chain
	toolRegistry      *tool.Registry
	permissionHandler tool.PermissionHandler
	providerRegistry  *provider.Registry
	agentRegistry     *agent.Registry
	agentsFileLoader  *agent.AgentsFileLoader
	lastContextResult ctxstore.BuildResult
	mu                sync.RWMutex
}

// Config holds the configuration for creating a new Engine.
type Config struct {
	ChatProvider      provider.Provider
	EmbeddingProvider provider.Provider
	Registry          *provider.Registry
	AgentRegistry     *agent.Registry
	Manifest          agent.Manifest
	Tools             []tool.Tool
	Skills            []skill.Skill
	Store             *recall.FileContextStore
	ChainStore        recall.ChainContextStore
	TokenCounter      ctxstore.TokenCounter
	StreamTimeout     time.Duration
	HookChain         *hook.Chain
	ToolRegistry      *tool.Registry
	PermissionHandler tool.PermissionHandler
	AgentsFileLoader  *agent.AgentsFileLoader
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
		chainStore:        cfg.ChainStore,
		windowBuilder:     windowBuilder,
		tokenCounter:      cfg.TokenCounter,
		streamTimeout:     timeout,
		hookChain:         cfg.HookChain,
		toolRegistry:      cfg.ToolRegistry,
		permissionHandler: cfg.PermissionHandler,
		providerRegistry:  cfg.Registry,
		agentRegistry:     cfg.AgentRegistry,
		agentsFileLoader:  cfg.AgentsFileLoader,
	}
}

// buildModelPreferences constructs model preferences from the agent manifest.
//
// The manifest uses a provider-keyed format where keys are provider names
// (e.g., "ollama", "anthropic", "openai") and values are model preference lists.
// This function flattens all preferences in a deterministic order.
//
// Expected:
//   - manifest contains a valid ModelPreferences map with provider keys.
//
// Returns:
//   - A slice of provider.ModelPreference with all preferences flattened, or empty if none exist.
//
// Side effects:
//   - None.
func buildModelPreferences(manifest agent.Manifest) []provider.ModelPreference {
	order := []string{"anthropic", "ollama", "openai"}

	var result []provider.ModelPreference
	seen := make(map[string]bool)

	for _, key := range order {
		prefs, ok := manifest.ModelPreferences[key]
		if ok {
			for _, p := range prefs {
				result = append(result, provider.ModelPreference{
					Provider: p.Provider,
					Model:    p.Model,
				})
			}
		}
		seen[key] = true
	}

	for key, prefs := range manifest.ModelPreferences {
		if seen[key] {
			continue
		}
		for _, p := range prefs {
			result = append(result, provider.ModelPreference{
				Provider: p.Provider,
				Model:    p.Model,
			})
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

// SetManifest updates the engine to use a different agent manifest.
//
// Expected:
//   - manifest is a valid agent.Manifest with required fields populated.
//
// Side effects:
//   - Replaces the engine's active manifest for subsequent chat operations.
func (e *Engine) SetManifest(manifest agent.Manifest) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.manifest = manifest
	if e.providerRegistry != nil {
		prefs := buildModelPreferences(manifest)
		if len(prefs) > 0 {
			e.failbackChain = provider.NewFailbackChain(e.providerRegistry, prefs, e.streamTimeout)
		}
	}
}

// Manifest returns the current agent manifest.
//
// Returns:
//   - The current agent.Manifest in use by the engine.
//
// Side effects:
//   - None.
func (e *Engine) Manifest() agent.Manifest {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.manifest
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

// buildDelegationTableSection constructs a markdown delegation table section.
//
// If the delegation table is nil or empty, returns an empty string.
// Otherwise, returns a formatted markdown section with the agent targets
// sorted alphabetically by key.
//
// Expected:
//   - delegationTable is a map of agent target names to task_type values.
//
// Returns:
//   - A markdown-formatted delegation table section, or empty string if no delegations.
//
// Side effects:
//   - None.
func buildDelegationTableSection(delegationTable map[string]string) string {
	if len(delegationTable) == 0 {
		return ""
	}

	// Extract keys and sort alphabetically
	keys := make([]string, 0, len(delegationTable))
	for k := range delegationTable {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	// Build markdown table
	var sb strings.Builder
	sb.WriteString("## Delegation Targets\n\n")
	sb.WriteString("When delegating, use these exact task_type values:\n\n")
	sb.WriteString("| Delegation Target | task_type |\n")
	sb.WriteString("|---|---|\n")

	for _, target := range keys {
		taskType := delegationTable[target]
		sb.WriteString("| ")
		sb.WriteString(target)
		sb.WriteString(" | `")
		sb.WriteString(taskType)
		sb.WriteString("` |\n")
	}

	return sb.String()
}

// buildDynamicDelegationTable constructs a markdown delegation table from prompt frontmatter and agent registry.
//
// This method:
// 1. Reads the prompt's YAML frontmatter to extract delegation_allowlist
// 2. Queries the agent registry for all available agents
// 3. Filters to only agents in the allowlist
// 4. Returns a formatted markdown table, sorted alphabetically
//
// If no frontmatter is found, no allowlist is configured, or no agents match: returns empty string.
//
// Returns:
//   - A markdown-formatted delegation table section, or empty string if delegation is not applicable.
//
// Side effects:
//   - None (read-only).
func (e *Engine) buildDynamicDelegationTable() string {
	// Get prompt with metadata
	promptContent, err := prompt.GetPromptWithMetadata(e.manifest.ID)
	if err != nil || promptContent == nil || promptContent.Metadata == nil {
		return ""
	}

	// Check if allowlist is configured
	allowlist := promptContent.Metadata.DelegationAllowlist
	if len(allowlist) == 0 {
		return ""
	}

	// Query agent registry for all available agents
	if e.agentRegistry == nil {
		return ""
	}

	availableAgents := e.agentRegistry.List()
	if len(availableAgents) == 0 {
		return ""
	}

	// Build a map of available agent IDs for quick lookup
	availableByID := make(map[string]*agent.Manifest)
	for _, a := range availableAgents {
		availableByID[a.ID] = a
	}

	// Filter to only agents in allowlist that are available
	var delegationAgents []string
	for _, agentID := range allowlist {
		if _, exists := availableByID[agentID]; exists {
			delegationAgents = append(delegationAgents, agentID)
		}
	}

	if len(delegationAgents) == 0 {
		return ""
	}

	// Sort alphabetically
	slices.Sort(delegationAgents)

	// Build markdown table
	var sb strings.Builder
	sb.WriteString("## Available Delegation Targets\n\n")
	sb.WriteString("You can delegate tasks to the following agents:\n\n")
	sb.WriteString("| Agent ID | Agent Name |\n")
	sb.WriteString("|---|---|\n")

	for _, agentID := range delegationAgents {
		agentManifest := availableByID[agentID]
		sb.WriteString("| `")
		sb.WriteString(agentID)
		sb.WriteString("` | ")
		sb.WriteString(agentManifest.Name)
		sb.WriteString(" |\n")
	}

	return sb.String()
}

// BuildSystemPrompt constructs the system prompt from the agent manifest and active skills.
//
// Returns:
//   - The concatenated system prompt string including always-active and agent-level skill content.
//
// Side effects:
//   - None.
func (e *Engine) BuildSystemPrompt() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var base string
	if prompt.HasPrompt(e.manifest.ID) {
		promptContent, err := prompt.GetPromptWithMetadata(e.manifest.ID)
		if err == nil {
			base = promptContent.Body
		} else {
			base = e.manifest.Instructions.SystemPrompt
		}
	} else {
		base = e.manifest.Instructions.SystemPrompt
	}

	if e.agentsFileLoader != nil {
		for _, f := range e.agentsFileLoader.LoadFiles() {
			base = base + "\n\nInstructions from: " + f.Path + "\n" + f.Content
		}
	}

	// Inject delegation table if agent can delegate
	if e.manifest.Delegation.CanDelegate {
		// Try dynamic delegation from frontmatter + agent registry first
		dynamicTable := e.buildDynamicDelegationTable()
		if dynamicTable != "" {
			base = base + "\n\n" + dynamicTable
		} else if len(e.manifest.Delegation.DelegationTable) > 0 {
			// Fall back to static delegation table from manifest (backwards compatibility)
			base = base + "\n\n" + buildDelegationTableSection(e.manifest.Delegation.DelegationTable)
		}
	}

	return base
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
	if agentID != "" && e.agentRegistry != nil {
		if manifest, found := e.agentRegistry.Get(agentID); found {
			e.mu.RLock()
			currentID := e.manifest.ID
			e.mu.RUnlock()
			if manifest.ID != currentID {
				e.SetManifest(*manifest)
			}
		}
	}

	messages := e.buildContextWindow(ctx, message)

	userMsg := provider.Message{Role: "user", Content: message}
	if e.store != nil {
		e.store.Append(userMsg)
		e.embedMessage(ctx, message)
	}

	req := provider.ChatRequest{
		Messages: messages,
		Tools:    e.buildToolSchemas(),
	}

	providerChunks, err := e.streamFromProvider(ctx, &req)
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
//   - req is a pointer to a chat request with messages and tools.
//
// Returns:
//   - A channel of StreamChunk values from the provider.
//   - An error if the stream fails to initialise.
//
// Side effects:
//   - Executes hook chain if configured. Hooks may mutate req.
func (e *Engine) streamFromProvider(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	slog.Info("engine stream request", "provider", e.LastProvider(), "model", e.LastModel(), "messages", len(req.Messages))
	handler := e.baseStreamHandler()
	if e.hookChain != nil {
		handler = e.hookChain.Execute(handler)
	}
	return handler(ctx, req)
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

		toolResult, err := e.executeToolCall(WithStreamOutput(ctx, outChan), toolCall)
		if err != nil {
			outChan <- provider.StreamChunk{Error: err, Done: true}
			return
		}

		e.storeAssistantToolUse(toolCall, responseContent)
		e.storeToolResult(toolCall.ID, toolResult)

		resultContent := toolResult.Output
		isError := toolResult.Error != nil
		if isError {
			resultContent = "Error: " + toolResult.Error.Error()
		}
		outChan <- provider.StreamChunk{
			EventType: "tool_result",
			ToolResult: &provider.ToolResultInfo{
				Content: resultContent,
				IsError: isError,
			},
		}

		messages = e.appendToolResultToMessages(messages, toolCall, toolResult)

		e.evictCompletedBackgroundTasks()

		var streamErr error
		toolReq := provider.ChatRequest{
			Messages: messages,
			Tools:    e.buildToolSchemas(),
		}
		providerChunks, streamErr = e.streamFromProvider(ctx, &toolReq)
		if streamErr != nil {
			outChan <- provider.StreamChunk{Error: streamErr, Done: true}
			return
		}
	}
}

// evictCompletedBackgroundTasks calls EvictCompleted on the delegate tool's background manager
// if one is configured, preventing unbounded memory growth from completed task entries.
//
// Side effects:
//   - Removes terminal tasks from the background task manager if a delegate tool is present.
func (e *Engine) evictCompletedBackgroundTasks() {
	dt, ok := e.GetDelegateTool()
	if !ok {
		return
	}
	bm := dt.BackgroundManager()
	if bm != nil {
		bm.EvictCompleted()
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
				outChan <- chunk
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
		if t.Name() != toolCall.Name {
			continue
		}
		slog.Info("engine tool call", "tool", toolCall.Name)
		input := tool.Input{
			Name:      toolCall.Name,
			Arguments: toolCall.Arguments,
		}
		result, err := t.Execute(ctx, input)
		result.Error = err
		return result, nil
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

// storeAssistantToolUse appends the assistant message containing a tool_use block to the context store.
//
// Expected:
//   - toolCall contains the tool call identifier, name, and arguments.
//   - content is the assistant's text content accumulated before the tool call (may be empty).
//
// Side effects:
//   - Appends an assistant message with ToolCalls to the context store if configured.
func (e *Engine) storeAssistantToolUse(toolCall *provider.ToolCall, content string) {
	if e.store == nil {
		return
	}
	e.store.Append(provider.Message{
		Role:    "assistant",
		Content: content,
		ToolCalls: []provider.ToolCall{
			{ID: toolCall.ID, Name: toolCall.Name, Arguments: toolCall.Arguments},
		},
	})
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
	assistantMsg := provider.Message{
		Role: "assistant",
		ToolCalls: []provider.ToolCall{
			{ID: toolCall.ID, Name: toolCall.Name, Arguments: toolCall.Arguments},
		},
	}
	messages = append(messages, assistantMsg)

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

	tokenBudget := e.ModelContextLimit()

	e.mu.RLock()
	manifestCopy := e.manifest
	e.mu.RUnlock()
	manifestCopy.Instructions.SystemPrompt = e.BuildSystemPrompt()
	result := e.windowBuilder.BuildContextResult(ctx, &manifestCopy, userMessage, e.store, tokenBudget)
	slog.Info("engine context window", "tokenBudget", tokenBudget, "messages", len(result.Messages))

	e.mu.Lock()
	e.lastContextResult = result
	e.mu.Unlock()

	return result.Messages
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
//   - Dual-writes to the chain store if one is configured (assistant messages only).
//   - Embeds the response if an embedding provider is configured.
func (e *Engine) storeResponse(ctx context.Context, content string) {
	if e.store == nil || content == "" {
		return
	}

	assistantMsg := provider.Message{Role: "assistant", Content: content}
	e.store.Append(assistantMsg)
	e.dualWriteToChainStore(assistantMsg)
	e.embedMessage(ctx, content)
}

// dualWriteToChainStore appends an assistant message to the chain store if one is configured.
//
// Expected:
//   - msg is the assistant message to dual-write.
//
// Side effects:
//   - Appends msg to chainStore if non-nil.
//   - Logs a warning if the chain store append fails.
func (e *Engine) dualWriteToChainStore(msg provider.Message) {
	if e.chainStore == nil {
		return
	}
	agentID := e.manifest.ID
	if err := e.chainStore.Append(agentID, msg); err != nil {
		slog.Warn("chain store dual-write failed", "agentID", agentID, "error", err)
	}
}

// SetContextStore sets the context store for session persistence.
//
// Expected:
//   - store is a FileContextStore instance, or nil to clear the store.
//
// Side effects:
//   - Replaces the engine's current context store reference.
func (e *Engine) SetContextStore(store *recall.FileContextStore) {
	e.store = store
}

// ContextStore returns the current context store.
//
// Returns:
//   - The FileContextStore currently attached to this engine, or nil.
//
// Side effects:
//   - None.
func (e *Engine) ContextStore() *recall.FileContextStore {
	return e.store
}

// LoadedSkills returns the skills stored when the engine was created.
//
// Returns:
//   - The slice of skill.Skill values assigned from cfg.Skills, or nil if none were provided.
//
// Side effects:
//   - None.
func (e *Engine) LoadedSkills() []skill.Skill {
	return e.skills
}

// LastContextResult returns the most recent context window build result.
//
// Returns:
//   - The BuildResult from the last call to buildContextWindow.
//
// Side effects:
//   - None.
func (e *Engine) LastContextResult() ctxstore.BuildResult {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastContextResult
}

// ModelContextLimit returns the context window token limit for the configured model.
//
// Returns:
//   - The token limit from the token counter using the first configured preference (DefaultModel).
//   - Falls back to the last-used model if no configured preference exists.
//   - Falls back to 4096 if no token counter is configured.
//
// Side effects:
//   - None.
func (e *Engine) ModelContextLimit() int {
	if e.tokenCounter == nil {
		return 4096
	}
	if e.failbackChain != nil {
		if m := e.failbackChain.DefaultModel(); m != "" {
			return e.tokenCounter.ModelLimit(m)
		}
	}
	return e.tokenCounter.ModelLimit(e.LastModel())
}

// HasTool reports whether the engine has a tool with the given name.
//
// Expected:
//   - name is the tool name to look up.
//
// Returns:
//   - true if a tool matching name is registered, false otherwise.
//
// Side effects:
//   - None.
func (e *Engine) HasTool(name string) bool {
	for _, t := range e.tools {
		if t.Name() == name {
			return true
		}
	}
	return false
}

// AddTool appends a tool to the engine's tool set.
//
// Expected:
//   - t is a non-nil tool implementing the tool.Tool interface.
//
// Returns:
//   - None.
//
// Side effects:
//   - Modifies the engine's internal tools slice.
func (e *Engine) AddTool(t tool.Tool) {
	e.tools = append(e.tools, t)
}

// GetDelegateTool returns the DelegateTool from the engine's tool set, if present.
//
// Returns:
//   - The DelegateTool and true when registered, or nil and false otherwise.
//
// Side effects:
//   - None.
func (e *Engine) GetDelegateTool() (*DelegateTool, bool) {
	for _, t := range e.tools {
		if dt, ok := t.(*DelegateTool); ok {
			return dt, true
		}
	}
	return nil, false
}
