package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

const (
	streamBufferSize     = 16
	defaultStreamTimeout = 5 * time.Minute
)

// Engine orchestrates AI agent interactions with providers, tools, and context management.
type Engine struct {
	chatProvider         provider.Provider
	embeddingProvider    provider.Provider
	failoverManager      *failover.Manager
	manifest             agent.Manifest
	tools                []tool.Tool
	skills               []skill.Skill
	store                *recall.FileContextStore
	chainStore           recall.ChainContextStore
	windowBuilder        *ctxstore.WindowBuilder
	recallBroker         recall.Broker
	contextAssemblyHooks []plugin.ContextAssemblyHook
	tokenCounter         ctxstore.TokenCounter
	streamTimeout        time.Duration
	hookChain            *hook.Chain
	toolRegistry         *tool.Registry
	permissionHandler    tool.PermissionHandler
	providerRegistry     *provider.Registry
	agentRegistry        *agent.Registry
	agentsFileLoader     *agent.AgentsFileLoader
	lastContextResult    ctxstore.BuildResult
	agentOverrides       map[string]string
	preferredProvider    string
	preferredModel       string
	bus                  *eventbus.EventBus
	mcpServerTools       map[string][]string

	cachedSystemPrompt string
	systemPromptDirty  bool
	cachedToolSchemas  []provider.Tool
	cachedAgentFiles   []agent.InstructionFile
	agentFilesCached   bool
	skipAgentFiles     bool
	currentSessionID   string

	// autoCompactor is the L2 compactor invoked from buildContextWindow
	// when compressionConfig.AutoCompaction.Enabled is true and the recent
	// message token load crosses the configured threshold. Nil disables
	// the feature.
	autoCompactor *ctxstore.AutoCompactor
	// compressionConfig carries the three-layer compression settings.
	// Only AutoCompaction is consumed by the engine directly; L1 wiring
	// flows via WindowBuilder and L3 via a separate injection point.
	compressionConfig ctxstore.CompressionConfig
	// lastCompactionSummary retains the most recent successful auto-
	// compaction summary so that T11 rehydration can read the intent,
	// next_steps, and files_to_restore emitted at compaction time.
	lastCompactionSummary *ctxstore.CompactionSummary
	// compressionMetrics, when non-nil, is shared with the window
	// builder (via WithMetrics) and bumped by maybeAutoCompact on every
	// successful L2 compaction so operators have a single counter set
	// spanning both layers. Nil means no metrics are recorded.
	compressionMetrics *ctxstore.CompressionMetrics

	// knowledgeExtractor is the L3 extractor fired asynchronously from
	// Stream to distil each completed turn into the session memory
	// store. Nil disables the feature.
	knowledgeExtractor *recall.KnowledgeExtractor

	mu sync.RWMutex
}

// Config holds the configuration for creating a new Engine.
type Config struct {
	ChatProvider         provider.Provider
	EmbeddingProvider    provider.Provider
	Registry             *provider.Registry
	AgentRegistry        *agent.Registry
	FailoverManager      *failover.Manager
	Manifest             agent.Manifest
	Tools                []tool.Tool
	Skills               []skill.Skill
	Store                *recall.FileContextStore
	ChainStore           recall.ChainContextStore
	TokenCounter         ctxstore.TokenCounter
	RecallBroker         recall.Broker
	ContextAssemblyHooks []plugin.ContextAssemblyHook
	StreamTimeout        time.Duration
	HookChain            *hook.Chain
	ToolRegistry         *tool.Registry
	PermissionHandler    tool.PermissionHandler
	AgentsFileLoader     *agent.AgentsFileLoader
	EventBus             *eventbus.EventBus
	// MCPServerTools maps MCP server names to the tool names they expose.
	// Used by buildAllowedToolSet to auto-include tools from servers declared
	// in Capabilities.MCPServers without requiring agents to list individual tool names.
	MCPServerTools map[string][]string
	// AutoCompactor is the optional L2 compactor that buildContextWindow
	// invokes when CompressionConfig.AutoCompaction is enabled and the
	// recent-message token load crosses the configured threshold. Nil
	// disables the feature regardless of CompressionConfig.
	AutoCompactor *ctxstore.AutoCompactor
	// CompressionConfig holds the three-layer compression settings. The
	// engine consults it at assembly time to gate L2 (auto-compaction)
	// behaviour. L1 and L3 wiring live in their own injection points.
	CompressionConfig ctxstore.CompressionConfig
	// CompressionMetrics, when non-nil, is attached to the window
	// builder and the engine so L1 offloads and L2 compactions are
	// counted in a single place. Nil disables metrics.
	CompressionMetrics *ctxstore.CompressionMetrics

	// KnowledgeExtractor is the optional L3 extractor fired in a
	// background goroutine after each Stream invocation when
	// CompressionConfig.SessionMemory.Enabled is true. Nil disables the
	// feature.
	KnowledgeExtractor *recall.KnowledgeExtractor

	// SessionMemoryStore is the optional L3 read-side store attached to
	// the WindowBuilder so distilled facts, conventions, and preferences
	// from prior turns (or prior sessions) surface as a
	// "[session memory]:" block immediately after the system prompt.
	// Attachment only happens when CompressionConfig.SessionMemory.Enabled
	// is true; nil disables the feature even when compression is on.
	// The extractor (KnowledgeExtractor) handles the write side; this
	// store handles the read side. The two are independent: either may
	// be nil, and tests typically set one at a time.
	SessionMemoryStore *recall.SessionMemoryStore
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
	windowBuilder := buildWindowBuilder(cfg)

	recall.RegisterRecallTools(&cfg)

	timeout := cfg.StreamTimeout
	if timeout == 0 {
		timeout = defaultStreamTimeout
	}

	bus := cfg.EventBus
	if bus == nil {
		bus = eventbus.NewEventBus()
	}

	var chain *hook.Chain
	if cfg.HookChain != nil {
		chain = cfg.HookChain
	} else if cfg.FailoverManager != nil {
		streamHook := failover.NewStreamHook(cfg.FailoverManager, bus, cfg.Manifest.ID)
		chain = hook.NewChain(func(next hook.HandlerFunc) hook.HandlerFunc {
			return streamHook.Execute(next)
		})
	}

	assemblyHooks := buildContextAssemblyHooks(cfg)

	return &Engine{
		chatProvider:         cfg.ChatProvider,
		embeddingProvider:    cfg.EmbeddingProvider,
		failoverManager:      cfg.FailoverManager,
		manifest:             cfg.Manifest,
		tools:                cfg.Tools,
		skills:               cfg.Skills,
		store:                cfg.Store,
		chainStore:           cfg.ChainStore,
		windowBuilder:        windowBuilder,
		recallBroker:         cfg.RecallBroker,
		contextAssemblyHooks: assemblyHooks,
		tokenCounter:         cfg.TokenCounter,
		streamTimeout:        timeout,
		hookChain:            chain,
		toolRegistry:         cfg.ToolRegistry,
		permissionHandler:    cfg.PermissionHandler,
		providerRegistry:     cfg.Registry,
		agentRegistry:        cfg.AgentRegistry,
		agentsFileLoader:     cfg.AgentsFileLoader,
		agentOverrides:       make(map[string]string),
		bus:                  bus,
		systemPromptDirty:    true,
		mcpServerTools:       cfg.MCPServerTools,
		autoCompactor:        cfg.AutoCompactor,
		compressionConfig:    cfg.CompressionConfig,
		compressionMetrics:   cfg.CompressionMetrics,
		knowledgeExtractor:   cfg.KnowledgeExtractor,
	}
}

// buildContextAssemblyHooks constructs the context assembly hook chain from config.
// If a RecallBroker is provided, it is auto-registered as the first hook.
// Any explicitly configured hooks are appended after the broker hook.
//
// Expected:
//   - cfg may contain a RecallBroker and/or ContextAssemblyHooks.
//
// Returns:
//   - A slice of ContextAssemblyHook functions for dispatch during context assembly.
//
// Side effects:
//   - None.
func buildContextAssemblyHooks(cfg Config) []plugin.ContextAssemblyHook {
	var hooks []plugin.ContextAssemblyHook
	if cfg.RecallBroker != nil {
		broker := cfg.RecallBroker
		hooks = append(hooks, func(ctx context.Context, payload *plugin.ContextAssemblyPayload) error {
			observations, err := broker.Query(ctx, payload.UserMessage, 5)
			if err != nil {
				return err
			}
			payload.SearchResults = append(payload.SearchResults, obsToSearchResults(observations)...)
			return nil
		})
	}
	hooks = append(hooks, cfg.ContextAssemblyHooks...)
	return hooks
}

// buildWindowBuilder constructs the engine's window builder from the
// supplied Config, attaching the compression metrics counter set when
// the caller provided one. Extracted from New to keep the constructor
// inside the funlen gate.
//
// Expected:
//   - cfg is the engine Config used to initialise the engine.
//
// Returns:
//   - A *ctxstore.WindowBuilder when cfg.TokenCounter is non-nil; nil
//     otherwise so downstream code can fall back to the simple path.
//
// Side effects:
//   - None.
func buildWindowBuilder(cfg Config) *ctxstore.WindowBuilder {
	if cfg.TokenCounter == nil {
		return nil
	}
	builder := ctxstore.NewWindowBuilder(cfg.TokenCounter)
	if cfg.CompressionMetrics != nil {
		builder = builder.WithMetrics(cfg.CompressionMetrics)
	}
	if cfg.SessionMemoryStore != nil && cfg.CompressionConfig.SessionMemory.Enabled {
		builder = builder.WithSessionMemory(cfg.SessionMemoryStore)
	}
	return builder
}

// SetAgentOverrides sets the agent-specific configuration overrides, such as prompt appends.
//
// Expected:
//   - overrides is a map from agent ID to PromptAppend text.
//
// Side effects:
//   - Modifies e.agentOverrides in place, replacing any existing overrides.
//   - Invalidates the cached system prompt.
func (e *Engine) SetAgentOverrides(overrides map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agentOverrides = overrides
	e.systemPromptDirty = true
}

// SetSkipAgentFiles controls whether agent instruction files (AGENTS.md) are excluded
// from the system prompt. Delegated child engines use this to reduce token usage
// when the parent's project-level instructions are irrelevant.
//
// Expected:
//   - skip is true to exclude agent files, false to include them.
//
// Side effects:
//   - Invalidates the cached system prompt.
func (e *Engine) SetSkipAgentFiles(skip bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skipAgentFiles = skip
	e.systemPromptDirty = true
}

// SkipAgentFiles reports whether agent instruction files are currently excluded
// from the system prompt for this engine.
//
// Returns:
//   - true if agent files are excluded, false if they are included.
//
// Side effects:
//   - None.
func (e *Engine) SkipAgentFiles() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.skipAgentFiles
}

// FailoverManager returns the failover manager as a ModelResolver.
//
// Returns:
//   - The failover.Manager instance used by this engine, or nil if not configured.
//
// Side effects:
//   - None.
func (e *Engine) FailoverManager() *failover.Manager {
	return e.failoverManager
}

// EventBus returns the engine's event bus for plugin event subscriptions.
//
// Returns:
//   - The EventBus instance created at engine construction.
//
// Side effects:
//   - None.
func (e *Engine) EventBus() *eventbus.EventBus {
	return e.bus
}

// LastProvider returns the name of the most recently used provider.
//
// Returns:
//   - The provider name string, or empty if no provider has been used.
//
// Side effects:
//   - None.
func (e *Engine) LastProvider() string {
	e.mu.RLock()
	if e.preferredProvider != "" {
		providerName := e.preferredProvider
		e.mu.RUnlock()
		return providerName
	}
	e.mu.RUnlock()

	if e.failoverManager != nil {
		if p := e.failoverManager.LastProvider(); p != "" {
			return p
		}
		prefs := e.failoverManager.Preferences()
		if len(prefs) > 0 {
			return prefs[0].Provider
		}
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
	e.mu.RLock()
	if e.preferredModel != "" {
		modelName := e.preferredModel
		e.mu.RUnlock()
		return modelName
	}
	e.mu.RUnlock()

	if e.failoverManager != nil {
		if m := e.failoverManager.LastModel(); m != "" {
			return m
		}
		prefs := e.failoverManager.Preferences()
		if len(prefs) > 0 {
			return prefs[0].Model
		}
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
//   - Modifies the failover manager's preferences to use the specified model first.
func (e *Engine) SetModelPreference(providerName string, modelName string) {
	e.mu.Lock()
	e.preferredProvider = providerName
	e.preferredModel = modelName
	e.mu.Unlock()

	if e.failoverManager != nil {
		e.failoverManager.SetOverride(provider.ModelPreference{
			Provider: providerName, Model: modelName,
		})
		return
	}
}

// SetManifest updates the engine to use a different agent manifest.
//
// Expected:
//   - manifest is a valid agent.Manifest with required fields populated.
//
// Side effects:
//   - Replaces the engine's active manifest for subsequent chat operations.
//   - Invalidates the cached system prompt.
func (e *Engine) SetManifest(manifest agent.Manifest) {
	e.mu.Lock()
	oldID := e.manifest.ID
	e.manifest = manifest
	e.systemPromptDirty = true
	e.cachedToolSchemas = nil
	sessionID := e.currentSessionID

	if dt, ok := e.getDelegateToolLocked(); ok {
		dt.SetDelegation(manifest.Delegation)
		dt.SetSourceAgentID(manifest.ID)
	}
	e.mu.Unlock()

	if e.bus != nil && oldID != manifest.ID && oldID != "" {
		e.bus.Publish(events.EventAgentSwitched, events.NewAgentSwitchedEvent(events.AgentSwitchedEventData{
			SessionID: sessionID,
			FromAgent: oldID,
			ToAgent:   manifest.ID,
		}))
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
	if e.failoverManager != nil {
		return e.failoverManager.ListModels()
	}
	if e.chatProvider != nil {
		return e.chatProvider.Models()
	}
	return nil, nil
}

// BuildSystemPrompt constructs the system prompt from the agent manifest and active skills.
//
// The composition order is: base prompt → agent files → delegation sections → prompt_append (last).
// Returns a cached result when the prompt inputs have not changed since the last build.
// The cache is invalidated by SetManifest and SetAgentOverrides.
//
// Returns:
//   - The concatenated system prompt string including always-active and agent-level skill content.
//
// Side effects:
//   - Caches the built prompt and loaded agent files for subsequent calls.
func (e *Engine) BuildSystemPrompt() string {
	e.mu.RLock()
	if !e.systemPromptDirty {
		cached := e.cachedSystemPrompt
		e.mu.RUnlock()
		return cached
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.systemPromptDirty {
		return e.cachedSystemPrompt
	}

	base := e.manifest.Instructions.SystemPrompt

	if e.agentsFileLoader != nil && !e.skipAgentFiles {
		if !e.agentFilesCached {
			e.cachedAgentFiles = e.agentsFileLoader.LoadFiles()
			e.agentFilesCached = true
		}
		for _, f := range e.cachedAgentFiles {
			base = base + "\n\nInstructions from: " + f.Path + "\n" + f.Content
		}
	}

	for i := range e.skills {
		base = base + "\n\n# Skill: " + e.skills[i].Name + "\n\n" + e.skills[i].Content
	}

	if e.manifest.Delegation.CanDelegate {
		base = e.appendDelegationSections(base)
	}

	if e.agentOverrides != nil {
		if appendText, ok := e.agentOverrides[e.manifest.ID]; ok && appendText != "" {
			base = base + "\n\n" + appendText
		}
	}

	e.cachedSystemPrompt = base
	e.systemPromptDirty = false

	return base
}

// appendDelegationSections builds and appends delegation sections from agent metadata or fallback table.
//
// Expected:
//   - base is the current system prompt string.
//
// Returns:
//   - The base string with appended delegation sections.
//
// Side effects:
//   - None.
func (e *Engine) appendDelegationSections(base string) string {
	if e.agentRegistry == nil {
		return base
	}

	agents := e.agentRegistry.List()

	allowlist := e.manifest.Delegation.DelegationAllowlist
	if len(allowlist) > 0 {
		agents = filterByAllowlist(agents, allowlist)
	}

	keyTriggers := buildKeyTriggersSection(agents)
	if keyTriggers != "" {
		base = base + "\n\n" + keyTriggers
	}

	toolSelection := buildToolSelectionSection(agents)
	if toolSelection != "" {
		base = base + "\n\n" + toolSelection
	}

	delegation := buildDelegationSection(agents)
	if delegation != "" {
		base = base + "\n\n" + delegation
	}
	return base
}

// buildAllowedToolSet returns the set of tool names allowed by the current manifest.
//
// Expected:
//   - e.manifest is the current agent manifest.
//   - e.mcpServerTools maps server names to their available tool names.
//
// Returns:
//   - A map of allowed tool names, or nil when the manifest does not restrict tools
//     (empty Capabilities.Tools means all tools are allowed for backward compatibility).
//   - All MCP server tools unconditionally bypass manifest filtering because they are
//     user-configured external tools; the manifest only controls built-in tool access.
//
// Side effects:
//   - None.
func (e *Engine) buildAllowedToolSet() map[string]bool {
	manifestTools := e.manifest.Capabilities.Tools
	if len(manifestTools) == 0 {
		return nil
	}

	allowed := make(map[string]bool, len(manifestTools))
	for _, mt := range manifestTools {
		switch mt {
		case "file":
			allowed["read"] = true
			allowed["write"] = true
		case "delegate":
			allowed["delegate"] = true
			allowed["background_output"] = true
			allowed["background_cancel"] = true
		default:
			allowed[mt] = true
		}
	}

	for _, toolNames := range e.mcpServerTools {
		for _, name := range toolNames {
			allowed[name] = true
		}
	}

	return allowed
}

// buildPropertyMap converts a map of tool.Property definitions into the
// JSON Schema property map expected by provider.ToolSchema.
//
// Expected:
//   - properties contains valid tool.Property entries with Type and Description set.
//
// Returns:
//   - A map of property names to their JSON Schema representations.
//
// Side effects:
//   - None; this is a pure transformation function.
func buildPropertyMap(properties map[string]tool.Property) map[string]interface{} {
	props := make(map[string]interface{}, len(properties))
	for k, v := range properties {
		propMap := map[string]interface{}{
			"type":        v.Type,
			"description": v.Description,
		}
		if len(v.Enum) > 0 {
			propMap["enum"] = v.Enum
		}
		if len(v.Items) > 0 {
			propMap["items"] = v.Items
		}
		props[k] = propMap
	}
	return props
}

// buildToolSchemas constructs provider-compatible tool schemas from registered tools.
//
// Returns:
//   - A slice of provider.Tool values with schema information for each tool.
//   - Returns a cached result when tools have not changed since the last call.
//
// Side effects:
//   - Caches the built schemas for subsequent calls.
func (e *Engine) buildToolSchemas() []provider.Tool {
	e.mu.RLock()
	if e.cachedToolSchemas != nil {
		cached := e.cachedToolSchemas
		e.mu.RUnlock()
		return cached
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cachedToolSchemas != nil {
		return e.cachedToolSchemas
	}

	allowedSet := e.buildAllowedToolSet()

	tools := make([]provider.Tool, 0, len(e.tools))
	for _, t := range e.tools {
		if allowedSet != nil && !allowedSet[t.Name()] {
			continue
		}
		schema := t.Schema()
		props := buildPropertyMap(schema.Properties)
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

	e.cachedToolSchemas = tools
	return tools
}

// ToolSchemas returns the current tool schemas filtered by the active manifest.
//
// Returns:
//   - A slice of provider.Tool representing the tools available under the current manifest.
//
// Side effects:
//   - May cache the schemas internally for subsequent calls.
func (e *Engine) ToolSchemas() []provider.Tool {
	return e.buildToolSchemas()
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
	sessionID := sessionIDFromContext(ctx)

	e.mu.Lock()
	e.currentSessionID = sessionID
	e.mu.Unlock()

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

	messages := e.buildContextWindow(ctx, sessionID, message)

	userMsg := provider.Message{Role: "user", Content: message}
	if e.store != nil {
		msgID := e.store.AppendReturningID(userMsg)
		e.embedMessage(ctx, message, msgID)
	}

	req := provider.ChatRequest{
		Provider: e.LastProvider(),
		Model:    e.LastModel(),
		Messages: messages,
		Tools:    e.buildToolSchemas(),
	}

	providerChunks, err := e.streamFromProvider(ctx, &req)
	e.publishProviderRequestEvent(sessionID, req)
	if err != nil {
		e.publishProviderErrorEvent(sessionID, "stream_init", err)
		return nil, err
	}

	outChan := make(chan provider.StreamChunk, streamBufferSize)

	go func() {
		defer close(outChan)
		e.streamWithToolLoop(ctx, sessionID, messages, providerChunks, outChan)
		//nolint:contextcheck // intentional: extraction uses fresh Background so stream ctx cancellation does not cut it short
		e.dispatchKnowledgeExtraction(messages)
	}()

	return outChan, nil
}

// dispatchKnowledgeExtraction fires the Phase 3 knowledge extractor on
// a background goroutine when one is configured and Layer 3 is enabled.
// The caller's messages slice is copied so the extractor never races
// with subsequent assembly runs on the shared slab. A fresh
// context.Background with a 30-second deadline is used so the stream's
// original ctx — which closes when the channel drains — does not
// cancel the extraction mid-flight.
//
// Expected:
//   - messages is the final message slice the stream ran against. The
//     caller must not mutate it after calling this method; the copy
//     inside protects only the in-flight extraction, not the caller.
//
// Returns:
//   - None. Errors from the extractor are logged at WARN and do not
//     propagate.
//
// Side effects:
//   - Spawns a goroutine when the extractor is wired and enabled.
func (e *Engine) dispatchKnowledgeExtraction(messages []provider.Message) {
	if e.knowledgeExtractor == nil || !e.compressionConfig.SessionMemory.Enabled {
		return
	}

	msgsCopy := make([]provider.Message, len(messages))
	copy(msgsCopy, messages)

	go runKnowledgeExtraction(e.knowledgeExtractor, msgsCopy)
}

// runKnowledgeExtraction is the body of the background goroutine spawned
// by dispatchKnowledgeExtraction. It uses a deliberately fresh
// context.Background so the stream's ctx — which is cancelled when the
// channel closes — cannot cut the extraction short.
//
// Expected:
//   - extractor is a non-nil KnowledgeExtractor.
//   - msgs is a defensive copy of the caller's slice — safe to read
//     from a goroutine without further coordination.
//
// Returns:
//   - None. Errors are logged at WARN and discarded.
//
// Side effects:
//   - One LLM call and at most one store save through the extractor.
func runKnowledgeExtraction(extractor *recall.KnowledgeExtractor, msgs []provider.Message) {
	extractCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := extractor.Extract(extractCtx, msgs); err != nil {
		slog.Warn("engine knowledge extraction failed",
			"error", err,
		)
	}
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
		if req.Provider != "" && e.providerRegistry != nil {
			p, err := e.providerRegistry.Get(req.Provider)
			if err == nil {
				return p.Stream(ctx, *req)
			}
		}
		if e.chatProvider != nil {
			return e.chatProvider.Stream(ctx, *req)
		}
		return nil, errors.New("no provider available: configure either ChatProvider or FailoverManager")
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
	ctx context.Context, sessionID string, messages []provider.Message,
	providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk,
) {
	defer e.evictCompletedBackgroundTasks()

	attempt := 0
	for {
		result := e.processStreamChunks(ctx, sessionID, providerChunks, outChan)
		if result.done {
			e.completeResponse(ctx, sessionID, result.responseContent, result.thinkingContent)
			return
		}

		if result.toolCall == nil {
			e.completeResponse(ctx, sessionID, result.responseContent, result.thinkingContent)
			return
		}

		if denied := e.checkToolPermission(result.toolCall, outChan); denied {
			return
		}

		toolResult, err := e.executeToolCall(WithStreamOutput(ctx, outChan), sessionID, result.toolCall)
		if err != nil {
			outChan <- provider.StreamChunk{Error: err, Done: true}
			return
		}

		e.storeAssistantToolUse(result.toolCall, result.responseContent)
		e.storeToolResult(result.toolCall.ID, toolResult)

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

		messages = e.appendToolResultToMessages(messages, result.toolCall, toolResult)

		attempt++
		var streamErr error
		providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
		if streamErr != nil {
			outChan <- provider.StreamChunk{Error: streamErr, Done: true}
			return
		}
	}
}

// evictCompletedBackgroundTasks calls EvictCompleted on the delegate tool's background manager
// if one is configured, preventing unbounded memory growth from completed task entries.
// Called via defer at the top of streamWithToolLoop so that tasks remain accessible
// throughout the entire tool loop and eviction covers all exit paths.
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

// retryStreamForToolResult publishes a retry event and opens a new provider stream
// after a tool call completes, continuing the tool loop.
//
// Expected:
//   - sessionID identifies the current session.
//   - messages includes the updated conversation with the tool result appended.
//   - attempt is the 1-based retry counter for observability.
//
// Returns:
//   - A channel of provider stream chunks for the next loop iteration.
//   - An error if the new stream cannot be initialised.
//
// Side effects:
//   - Publishes provider.request.retry and provider.request events on the bus.
func (e *Engine) retryStreamForToolResult(
	ctx context.Context, sessionID string, messages []provider.Message, attempt int,
) (<-chan provider.StreamChunk, error) {
	e.bus.Publish(events.EventProviderRequestRetry, events.NewProviderRequestRetryEvent(events.ProviderRequestRetryEventData{
		SessionID:    sessionID,
		AgentID:      e.manifest.ID,
		ProviderName: e.LastProvider(),
		ModelName:    e.LastModel(),
		Reason:       "tool_loop_retry",
		Attempt:      attempt,
	}))
	toolReq := provider.ChatRequest{
		Provider: e.LastProvider(),
		Model:    e.LastModel(),
		Messages: messages,
		Tools:    e.buildToolSchemas(),
	}
	chunks, streamErr := e.streamFromProvider(ctx, &toolReq)
	e.publishProviderRequestEvent(sessionID, toolReq)
	if streamErr != nil {
		e.publishProviderErrorEvent(sessionID, "stream_init", streamErr)
		return nil, streamErr
	}
	return chunks, nil
}

// streamChunkResult carries the assembled output from processStreamChunks.
type streamChunkResult struct {
	toolCall        *provider.ToolCall
	responseContent string
	thinkingContent string
	done            bool
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
	ctx context.Context, sessionID string, providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk,
) streamChunkResult {
	var responseContent strings.Builder
	var thinkingContent strings.Builder

	for {
		select {
		case <-ctx.Done():
			outChan <- provider.StreamChunk{Error: ctx.Err(), Done: true, ModelID: e.LastModel()}
			return streamChunkResult{responseContent: responseContent.String(), thinkingContent: thinkingContent.String(), done: true}
		case chunk, ok := <-providerChunks:
			if !ok {
				return streamChunkResult{responseContent: responseContent.String(), thinkingContent: thinkingContent.String()}
			}

			chunk.ModelID = e.LastModel()

			// Dispatch by chunk shape rather than EventType so the loop
			// matches the session accumulator (internal/session/accumulator.go:98)
			// and cannot silently drop tool calls when a provider forgets to
			// stamp EventType. Non-anthropic OpenAI-compatible providers hit this
			// path; anthropic and ollama continue to stamp EventType so this check
			// is strictly more permissive for them without behavioural change.
			if chunk.ToolCall != nil {
				if e.bus != nil && responseContent.Len() > 0 {
					e.bus.Publish(events.EventToolReasoning, events.NewToolReasoningEvent(events.ToolReasoningEventData{
						SessionID:        sessionID,
						AgentID:          e.manifest.ID,
						ToolName:         chunk.ToolCall.Name,
						ReasoningContent: responseContent.String(),
					}))
				}
				outChan <- chunk
				return streamChunkResult{
					toolCall:        chunk.ToolCall,
					responseContent: responseContent.String(),
					thinkingContent: thinkingContent.String(),
				}
			}

			thinkingContent.WriteString(chunk.Thinking)
			responseContent.WriteString(chunk.Content)
			outChan <- chunk

			if chunk.Done {
				return streamChunkResult{responseContent: responseContent.String(), thinkingContent: thinkingContent.String(), done: true}
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
func (e *Engine) executeToolCall(ctx context.Context, sessionID string, toolCall *provider.ToolCall) (tool.Result, error) {
	for _, t := range e.tools {
		if t.Name() != toolCall.Name {
			continue
		}
		slog.Info("engine tool call", "tool", toolCall.Name)
		e.publishToolBeforeEvent(sessionID, toolCall.Name, toolCall.Arguments)
		input := tool.Input{
			Name:      toolCall.Name,
			Arguments: toolCall.Arguments,
		}
		result, err := t.Execute(ctx, input)
		result.Error = err
		e.publishToolAfterEvent(sessionID, toolCall.Name, toolCall.Arguments, result.Output, err)
		return result, nil
	}
	return tool.Result{}, fmt.Errorf("%w: %s", tool.ErrToolNotFound, toolCall.Name)
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

// obsToSearchResults converts Observation objects from RecallBroker to SearchResult format.
// Observations don't have scores, so we use a default score of 1.0.
//
// Expected: Slice of Observation objects from RecallBroker.Query().
// Returns: Slice of SearchResult objects with default score of 1.0.
// Side effects: None.
func obsToSearchResults(observations []recall.Observation) []recall.SearchResult {
	searchResults := make([]recall.SearchResult, 0, len(observations))
	for _, obs := range observations {
		searchResults = append(searchResults, recall.SearchResult{
			MessageID: obs.ID,
			Score:     1.0,
			Message: provider.Message{
				Role:    "assistant",
				Content: obs.Content,
			},
		})
	}
	return searchResults
}

// buildContextWindow assembles context for the language model, including system prompt, chat history, and observations from RecallBroker.
// It queries RecallBroker for relevant observations if available and merges them into the context window.
// If RecallBroker is unavailable or fails, context assembly degrades gracefully to normal operation.
//
// Expected: sessionID and userMessage are non-empty strings.
// Returns: Slice of provider.Message objects forming the context window for the language model.
// Side effects: Logs query failures without crashing; uses RecallBroker if available.
func (e *Engine) buildContextWindow(ctx context.Context, sessionID string, userMessage string) []provider.Message {
	if e.windowBuilder == nil || e.store == nil {
		systemPrompt := e.BuildSystemPrompt()
		return []provider.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		}
	}

	tokenBudget := e.ModelContextLimit()
	systemPrompt := e.BuildSystemPrompt()

	e.mu.RLock()
	defer e.mu.RUnlock()

	manifestCopy := e.manifest
	manifestCopy.Instructions.SystemPrompt = systemPrompt

	searchResults := e.dispatchContextAssemblyHooks(ctx, sessionID, userMessage, tokenBudget)

	compactedSummary := e.maybeAutoCompact(ctx, sessionID, &manifestCopy, tokenBudget)

	var result ctxstore.BuildResult
	switch {
	case len(searchResults) > 0:
		result = e.windowBuilder.BuildWithSemanticResults(&manifestCopy, e.store, tokenBudget, searchResults)
		if userMessage != "" {
			userTokens := e.tokenCounter.Count(userMessage)
			result.Messages = append(result.Messages, provider.Message{
				Role:    "user",
				Content: userMessage,
			})
			result.TokensUsed += userTokens
			result.BudgetRemaining -= userTokens
		}
	case compactedSummary != "":
		result = e.windowBuilder.BuildWithSummary(&manifestCopy, e.store, tokenBudget, compactedSummary)
		if userMessage != "" {
			userTokens := e.tokenCounter.Count(userMessage)
			result.Messages = append(result.Messages, provider.Message{
				Role:    "user",
				Content: userMessage,
			})
			result.TokensUsed += userTokens
			result.BudgetRemaining -= userTokens
		}
	default:
		result = e.windowBuilder.BuildContextResult(ctx, &manifestCopy, userMessage, e.store, tokenBudget)
	}

	slog.Info("engine context window", "tokenBudget", tokenBudget, "messages", len(result.Messages))

	e.lastContextResult = result
	e.publishContextWindowEvents(sessionID, manifestCopy.Instructions.SystemPrompt, tokenBudget, result)

	return result.Messages
}

// maybeAutoCompact runs the Phase 2 auto-compaction trigger when the
// engine is configured with an AutoCompactor, the feature is enabled,
// and the recent-message token load exceeds the configured threshold.
//
// Expected:
//   - ctx carries cancellation/deadline for the LLM call.
//   - sessionID identifies the active session; threaded through to the
//     T10b ContextCompactedEvent so subscribers can correlate emitted
//     events with session telemetry.
//   - manifest has been prepared with the current system prompt (used to
//     determine SlidingWindowSize).
//   - tokenBudget is the full model context limit.
//
// Returns:
//   - The summary text ("[auto-compacted summary]: <json>") when
//     compaction fired and succeeded; empty otherwise.
//   - The built window falls back to the normal path on:
//   - feature disabled,
//   - compactor nil,
//   - token load under threshold,
//   - compactor error (logged, not fatal).
//
// Side effects:
//   - Issues one LLM call via the injected AutoCompactor when fired.
//   - Updates e.lastCompactionSummary on success; cleared on non-fire.
//   - Publishes a pluginevents.ContextCompactedEvent on the engine bus
//     on successful compaction (T10b per ADR - Tool-Call Atomicity).
func (e *Engine) maybeAutoCompact(ctx context.Context, sessionID string, manifest *agent.Manifest, tokenBudget int) string {
	e.lastCompactionSummary = nil

	threshold, ok := e.autoCompactionThreshold(tokenBudget)
	if !ok {
		return ""
	}

	recent, recentTokens, fire := e.autoCompactionCandidates(manifest, tokenBudget, threshold)
	if !fire {
		return ""
	}

	start := time.Now()
	summary, err := e.autoCompactor.Compact(ctx, recent)
	if err != nil {
		slog.Warn("engine auto-compaction failed; falling back to uncompacted window",
			"error", err,
			"recentTokens", recentTokens,
			"tokenBudget", tokenBudget,
			"threshold", threshold,
		)
		return ""
	}
	latency := time.Since(start)

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		slog.Warn("engine auto-compaction produced unmarshallable summary", "error", err)
		return ""
	}

	summaryCopy := summary
	e.lastCompactionSummary = &summaryCopy

	summaryText := "[auto-compacted summary]: " + string(summaryJSON)
	e.publishContextCompactedEvent(sessionID, manifest.ID, recentTokens, summaryText, latency)
	return summaryText
}

// autoCompactionThreshold returns the configured auto-compaction ratio
// threshold when every prerequisite for compaction is met. The second
// return value is false when the feature is disabled or a dependency is
// missing, so the caller can short-circuit without inspecting fields
// individually.
//
// Expected:
//   - tokenBudget is the model context limit passed through from
//     buildContextWindow.
//
// Returns:
//   - (threshold, true) when the feature is enabled, the AutoCompactor
//     is wired, the store and counter are present, tokenBudget is
//     positive, and the configured threshold is positive.
//   - (0, false) otherwise.
//
// Side effects:
//   - None.
func (e *Engine) autoCompactionThreshold(tokenBudget int) (float64, bool) {
	if e.autoCompactor == nil || !e.compressionConfig.AutoCompaction.Enabled {
		return 0, false
	}
	if e.store == nil || e.tokenCounter == nil {
		return 0, false
	}
	if tokenBudget <= 0 {
		return 0, false
	}
	threshold := e.compressionConfig.AutoCompaction.Threshold
	if threshold <= 0 {
		return 0, false
	}
	return threshold, true
}

// autoCompactionCandidates pulls the recent-message slice from the
// store, counts its tokens, and decides whether the load crosses the
// threshold. Split out of maybeAutoCompact so the decision logic can
// be unit-tested independently of the LLM call and so the trigger
// function stays inside the funlen gate.
//
// Expected:
//   - manifest carries ContextManagement to pick the sliding window size.
//   - tokenBudget is the model context limit.
//   - threshold is the ratio above which compaction fires.
//
// Returns:
//   - recent: the recent-message slice counted against the budget.
//   - recentTokens: sum of token counts for those messages.
//   - fire: true when the ratio exceeds threshold and there is content
//     to summarise; false when compaction should be skipped.
//
// Side effects:
//   - None.
func (e *Engine) autoCompactionCandidates(manifest *agent.Manifest, tokenBudget int, threshold float64) ([]provider.Message, int, bool) {
	slidingWindowSize := manifest.ContextManagement.SlidingWindowSize
	if slidingWindowSize <= 0 {
		slidingWindowSize = 10
	}
	recent := e.store.GetRecent(slidingWindowSize)
	if len(recent) == 0 {
		return nil, 0, false
	}
	var recentTokens int
	for i := range recent {
		recentTokens += e.tokenCounter.Count(recent[i].Content)
	}
	ratio := float64(recentTokens) / float64(tokenBudget)
	if ratio <= threshold {
		return nil, 0, false
	}
	return recent, recentTokens, true
}

// publishContextCompactedEvent emits the T10b ContextCompactedEvent on
// the engine bus when compaction succeeds. Counted as observability:
// failed or no-op compactions are not emitted so subscribers do not see
// phantom events.
//
// Expected:
//   - sessionID and agentID identify the emission source.
//   - recentTokens is the pre-compaction token count the summary replaces.
//   - summaryText is the final "[auto-compacted summary]: <json>" string
//     injected into the built window.
//   - latency is the wall-clock duration of the Compact call.
//
// Side effects:
//   - Publishes one event on the engine bus if non-nil; otherwise no-op.
func (e *Engine) publishContextCompactedEvent(sessionID, agentID string, recentTokens int, summaryText string, latency time.Duration) {
	summaryTokens := e.tokenCounter.Count(summaryText)
	if e.compressionMetrics != nil {
		e.compressionMetrics.AutoCompactionCount++
		if delta := recentTokens - summaryTokens; delta > 0 {
			e.compressionMetrics.TokensSaved += delta
		}
	}
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
		SessionID:      sessionID,
		AgentID:        agentID,
		OriginalTokens: recentTokens,
		SummaryTokens:  summaryTokens,
		LatencyMS:      latency.Milliseconds(),
	}))
}

// LastCompactionSummary returns the most recent auto-compaction summary
// produced by buildContextWindow, or nil if compaction has not fired
// since the engine was created (or since the last non-firing build).
//
// Expected:
//   - The engine has been used to assemble at least one context window.
//
// Returns:
//   - A pointer to the stored summary. The caller must not mutate it;
//     it is the same value persisted on the engine.
//   - nil when compaction has not fired on the most recent build.
//
// Side effects:
//   - None.
func (e *Engine) LastCompactionSummary() *ctxstore.CompactionSummary {
	return e.lastCompactionSummary
}

// dispatchContextAssemblyHooks fires all registered context assembly hooks, collecting search results.
// Each hook receives a mutable ContextAssemblyPayload and may populate SearchResults.
// Hook errors are logged but do not prevent subsequent hooks or assembly from proceeding.
//
// Expected:
//   - sessionID identifies the active session.
//   - userMessage is the user's input text.
//   - tokenBudget is the configured token limit.
//
// Returns:
//   - Aggregated search results from all hooks.
//
// Side effects:
//   - Logs warnings for hooks that return errors.
func (e *Engine) dispatchContextAssemblyHooks(
	ctx context.Context, sessionID string, userMessage string, tokenBudget int,
) []recall.SearchResult {
	if len(e.contextAssemblyHooks) == 0 {
		return nil
	}
	payload := &plugin.ContextAssemblyPayload{
		SessionID:   sessionID,
		AgentID:     e.manifest.ID,
		UserMessage: userMessage,
		TokenBudget: tokenBudget,
	}
	for _, h := range e.contextAssemblyHooks {
		if err := h(ctx, payload); err != nil {
			slog.Warn("context.assembly hook failed", "error", err)
		}
	}
	return payload.SearchResults
}

// publishContextWindowEvents emits prompt and context window events when the event bus is configured.
//
// Expected:
//   - sessionID identifies the active session.
//   - systemPrompt is the assembled system prompt text.
//   - tokenBudget is the configured token limit.
//   - result contains the build outcome with token usage.
//
// Side effects:
//   - Publishes EventPromptGenerated and EventContextWindowBuilt if bus is non-nil.
func (e *Engine) publishContextWindowEvents(sessionID string, systemPrompt string, tokenBudget int, result ctxstore.BuildResult) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventPromptGenerated, events.NewPromptEvent(events.PromptEventData{
		SessionID:  sessionID,
		AgentID:    e.manifest.ID,
		FullPrompt: systemPrompt,
		TokenCount: result.TokensUsed,
		Truncated:  result.Truncated,
	}))
	e.bus.Publish(events.EventContextWindowBuilt, events.NewContextWindowEvent(events.ContextWindowEventData{
		SessionID:       sessionID,
		AgentID:         e.manifest.ID,
		TokenBudget:     tokenBudget,
		TokensUsed:      result.TokensUsed,
		BudgetRemaining: result.BudgetRemaining,
		MessageCount:    len(result.Messages),
		Truncated:       result.Truncated,
	}))
}

// embedMessage sends the message content to the embedding provider if configured and stores the vector.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - content is the message text to embed.
//   - msgID is the unique identifier of the stored message.
//
// Side effects:
//   - Calls the embedding provider if configured and stores the vector.
//   - Publishes a recall.embedding.stored event if the event bus is configured.
func (e *Engine) embedMessage(ctx context.Context, content string, msgID string) {
	if e.embeddingProvider == nil || e.store == nil {
		return
	}

	start := time.Now()
	vec, err := e.embeddingProvider.Embed(ctx, provider.EmbedRequest{Input: content})
	if err != nil {
		return
	}

	dimensions := len(vec)
	e.store.StoreEmbedding(msgID, vec, e.store.GetModel(), dimensions)

	if e.bus != nil {
		e.bus.Publish(events.EventRecallEmbeddingStored, events.NewRecallEmbeddingStoredEvent(events.RecallEmbeddingStoredEventData{
			SessionID:  e.currentSessionID,
			MessageID:  msgID,
			Dimensions: dimensions,
			LatencyMS:  time.Since(start).Milliseconds(),
		}))
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
func (e *Engine) storeResponse(ctx context.Context, content, thinking string) {
	if e.store == nil || (content == "" && thinking == "") {
		return
	}

	assistantMsg := provider.Message{Role: "assistant", Content: content, Thinking: thinking}
	msgID := e.store.AppendReturningID(assistantMsg)
	e.dualWriteToChainStore(assistantMsg)
	e.embedMessage(ctx, content, msgID)
}

// completeResponse stores the assistant response and publishes a provider response event.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - sessionID identifies the current session.
//   - content is the assistant's response text.
//
// Returns:
//   - None.
//
// Side effects:
//   - Stores the response via storeResponse.
//   - Publishes a provider.response event on the engine bus.
func (e *Engine) completeResponse(ctx context.Context, sessionID string, content, thinking string) {
	e.storeResponse(ctx, content, thinking)
	e.publishProviderResponseEvent(sessionID, content)
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
	if err := e.chainStore.Append(e.manifest.ID, msg); err != nil {
		slog.Warn("chain store dual-write failed", "agentID", e.manifest.ID, "error", err)
	}
}

// SetContextStore sets the context store for session persistence.
//
// Expected:
//   - store is a FileContextStore instance, or nil to clear the store.
//   - sessionID identifies the session associated with this store.
//
// Side effects:
//   - Replaces the engine's current context store reference.
//   - Publishes session.created when store is non-nil.
//   - Publishes session.ended when store is nil and a previous store existed.
func (e *Engine) SetContextStore(store *recall.FileContextStore, sessionID string) {
	hadStore := e.store != nil
	e.store = store
	if store != nil {
		e.publishSessionEvent(sessionID, "created")
	} else if hadStore {
		e.publishSessionEvent(sessionID, "ended")
	}
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

// ChainStore returns the current chain context store.
//
// Returns:
//   - The ChainContextStore currently attached to this engine, or nil.
//
// Side effects:
//   - None.
func (e *Engine) ChainStore() recall.ChainContextStore {
	return e.chainStore
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
	if e.failoverManager != nil {
		prefs := e.failoverManager.Preferences()
		if len(prefs) > 0 {
			return e.failoverManager.ResolveContextLength(prefs[0].Provider, prefs[0].Model)
		}
	}
	if e.tokenCounter != nil {
		return e.tokenCounter.ModelLimit(e.LastModel())
	}
	return 4096
}

// ResolveContextLength returns the context window limit for the given provider/model.
// It delegates to the failover manager's resolver if available, or returns 4096.
//
// Expected:
//   - providerName and model identify a known provider/model pair.
//
// Returns:
//   - The context length in tokens, or 4096 if the provider/model is unknown.
//
// Side effects:
//   - None.
func (e *Engine) ResolveContextLength(providerName, model string) int {
	if e.failoverManager != nil {
		return e.failoverManager.ResolveContextLength(providerName, model)
	}
	return 4096
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
	e.mu.RLock()
	defer e.mu.RUnlock()
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
//   - Invalidates the cached tool schemas.
func (e *Engine) AddTool(t tool.Tool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tools = append(e.tools, t)
	e.cachedToolSchemas = nil
}

// GetDelegateTool returns the DelegateTool from the engine's tool set, if present.
//
// Returns:
//   - The DelegateTool and true when registered, or nil and false otherwise.
//
// Side effects:
//   - None.
func (e *Engine) GetDelegateTool() (*DelegateTool, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.getDelegateToolLocked()
}

// getDelegateToolLocked returns the DelegateTool without acquiring the lock.
// Caller must hold e.mu (read or write).
//
// Returns:
//   - The DelegateTool and true when registered, or nil and false otherwise.
//
// Side effects:
//   - None.
func (e *Engine) getDelegateToolLocked() (*DelegateTool, bool) {
	for _, t := range e.tools {
		if dt, ok := t.(*DelegateTool); ok {
			return dt, true
		}
	}
	return nil, false
}

// publishSessionEvent publishes a session lifecycle event to the engine bus.
//
// Expected:
//   - action is the session lifecycle transition name.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes an event on the engine bus when one is configured.
func (e *Engine) publishSessionEvent(sessionID string, action string) {
	var topic string
	switch action {
	case "created":
		topic = events.EventSessionCreated
	case "ended":
		topic = events.EventSessionEnded
	default:
		topic = "session." + action
	}
	e.bus.Publish(topic, events.NewSessionEvent(events.SessionEventData{
		SessionID: sessionID,
		Action:    action,
	}))
}

// publishToolBeforeEvent publishes a tool execution start event to the engine bus.
//
// Expected:
//   - toolName identifies the tool being executed.
//   - args contains the tool arguments.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a tool execution start event on the engine bus.
func (e *Engine) publishToolBeforeEvent(sessionID string, toolName string, args map[string]interface{}) {
	e.bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{
		SessionID: sessionID,
		ToolName:  toolName,
		Args:      args,
	}))
}

// publishToolAfterEvent publishes a tool execution completion event to the engine bus.
//
// Expected:
//   - toolName identifies the tool being executed.
//   - args contains the tool arguments.
//   - result contains the tool output.
//   - execErr contains the execution error, if any.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a tool execution completion event on the engine bus.
func (e *Engine) publishToolAfterEvent(sessionID string, toolName string, args map[string]interface{}, result string, execErr error) {
	e.bus.Publish(events.EventToolExecuteAfter, events.NewToolEvent(events.ToolEventData{
		SessionID: sessionID,
		ToolName:  toolName,
		Args:      args,
		Result:    result,
		Error:     execErr,
	}))
	if execErr == nil {
		e.bus.Publish(events.EventToolExecuteResult, events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
			SessionID: sessionID,
			ToolName:  toolName,
			Args:      args,
			Result:    result,
		}))
	} else {
		e.bus.Publish(events.EventToolExecuteError, events.NewToolExecuteErrorEvent(events.ToolExecuteErrorEventData{
			SessionID: sessionID,
			ToolName:  toolName,
			Args:      args,
			Error:     execErr,
		}))
	}
}

// publishProviderErrorEvent publishes a typed provider error event to the engine bus.
//
// Expected:
//   - sessionID identifies the session where the error occurred.
//   - phase describes the streaming phase when the error happened.
//   - err describes the provider failure.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a provider.error event on the engine bus.
func (e *Engine) publishProviderErrorEvent(sessionID string, phase string, err error) {
	if e.bus == nil {
		return
	}

	data := events.ProviderErrorEventData{
		SessionID:    sessionID,
		AgentID:      e.manifest.ID,
		ProviderName: e.LastProvider(),
		ModelName:    e.LastModel(),
		Error:        err,
		Phase:        phase,
	}

	var provErr *provider.Error
	if errors.As(err, &provErr) {
		data.ErrorType = string(provErr.ErrorType)
		data.ErrorCode = provErr.ErrorCode
		data.HTTPStatus = provErr.HTTPStatus
		data.IsRetriable = provErr.IsRetriable
	}
	e.bus.Publish(events.EventProviderError, events.NewProviderErrorEvent(data))
}

// publishProviderRequestEvent publishes a provider request event to the engine bus
// before each outbound provider call.
//
// Expected:
//   - req contains the full ChatRequest being sent to the provider.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a provider.request event on the engine bus.
func (e *Engine) publishProviderRequestEvent(sessionID string, req provider.ChatRequest) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventProviderRequest, events.NewProviderRequestEvent(events.ProviderRequestEventData{
		SessionID:    sessionID,
		AgentID:      e.manifest.ID,
		ProviderName: req.Provider,
		ModelName:    req.Model,
		Request:      req,
	}))
}

// publishProviderResponseEvent publishes a provider response event to the engine bus
// after a provider stream completes successfully.
//
// Expected:
//   - sessionID identifies the session that received the response.
//   - responseContent is the assembled response text from the stream.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a provider.response event on the engine bus.
func (e *Engine) publishProviderResponseEvent(sessionID string, responseContent string) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventProviderResponse, events.NewProviderResponseEvent(events.ProviderResponseEventData{
		SessionID:       sessionID,
		AgentID:         e.manifest.ID,
		ProviderName:    e.LastProvider(),
		ModelName:       e.LastModel(),
		ResponseContent: responseContent,
	}))
}

// sessionIDFromContext extracts the session ID from the context, returning
// an empty string if no session ID is present.
//
// Expected:
//   - ctx is a valid context that may carry a session.IDKey value.
//
// Returns:
//   - The session ID string, or empty if not set.
//
// Side effects:
//   - None.
func sessionIDFromContext(ctx context.Context) string {
	id, ok := ctx.Value(session.IDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}
