package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/context/compaction"
	"github.com/baphled/flowstate/internal/context/factstore"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/sessionid"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tracer"
)

const (
	streamBufferSize     = 16
	defaultStreamTimeout = 5 * time.Minute
	defaultToolTimeout   = 2 * time.Minute
)

// Engine orchestrates AI agent interactions with providers, tools, and context management.
type Engine struct {
	chatProvider         provider.Provider
	embeddingProvider    provider.Provider
	failoverManager      *failover.Manager
	manifest             agent.Manifest
	tools                []tool.Tool
	skills               []skill.Skill
	skillsResolver       func(agent.Manifest) []skill.Skill
	store                *recall.FileContextStore
	chainStore           recall.ChainContextStore
	windowBuilder        *ctxstore.WindowBuilder
	recallBroker         recall.Broker
	contextAssemblyHooks []plugin.ContextAssemblyHook
	tokenCounter         ctxstore.TokenCounter
	systemPromptBudget   int
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
	toolTimeout          time.Duration

	// toolCallCorrelator assigns a stable FlowState-internal identifier to
	// every tool call observed on the stream path and reuses it whenever
	// the same logical call is referenced again — whether by the same
	// provider on a later chunk or by a different provider after a
	// failover (the P14 contract). Emitted on StreamChunk.InternalToolCallID
	// so downstream consumers (activity pane coalesce, event details modal,
	// persisted SwarmEvent entries) can pair tool_call / tool_result events
	// without tripping over the disjoint native ID spaces the providers use.
	// Lazily constructed if not supplied in Config.
	toolCallCorrelator *streaming.ToolCallCorrelator

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
	// This is a cross-session view: the most recent compaction from
	// ANY session is surfaced here, consistent with the rest of the
	// engine's cross-session aggregate state (e.compressionMetrics,
	// e.lastContextResult).
	lastCompactionSummary *ctxstore.CompactionSummary
	// sessionCompactionMemo is the H2 per-session memoisation keyed
	// by sessionID. Each entry holds the cold-range hash whose
	// compaction produced the cached summary. A subsequent
	// maybeAutoCompact call with an identical hash for the same
	// session reuses the cached summary instead of re-invoking the
	// summariser. Per-session so a hash collision across sessions
	// does not rob session B of its own ContextCompactedEvent and
	// per-session metrics bump.
	sessionCompactionMemo map[string]sessionCompactionMemoEntry
	// sessionRehydrated tracks which sessions have already consumed
	// their compaction summary's FilesToRestore, so the next turn
	// after a compaction rehydrates exactly once rather than re-
	// reading the same files on every subsequent build. The set
	// invalidates when the compaction summary changes (a fresh
	// compaction produced a new summary with its own FilesToRestore)
	// and on session.ended.
	sessionRehydrated map[string]struct{}
	// compressionMetrics, when non-nil, is shared with the window
	// builder (via WithMetrics) and bumped by maybeAutoCompact on every
	// successful L2 compaction so operators have a single counter set
	// spanning both layers. Nil means no metrics are recorded.
	compressionMetrics *ctxstore.CompressionMetrics

	// sessionCompressionMetrics partitions the cumulative
	// compressionMetrics counters by sessionID so user-facing surfaces
	// (flowstate run --stats, the slog compression-metrics line) can
	// report per-session figures instead of the ever-growing aggregate
	// a single engine accumulates across many sessions. The aggregate
	// struct is still bumped in lockstep — it is the cumulative view a
	// flowstate serve dashboard needs. Nil entries are treated as zero
	// by SessionCompressionMetrics so a just-started session reports
	// empty counters rather than stale state from an earlier session.
	sessionCompressionMetrics   map[string]*ctxstore.CompressionMetrics
	sessionCompressionMetricsMu sync.Mutex

	// recorder, when non-nil, receives RecordCompressionTokensSaved on
	// every successful L2 compaction. The delta is OriginalTokens -
	// SummaryTokens — the same figure the ContextCompactedEvent carries
	// — so Prometheus time series and event-bus subscribers stay in
	// sync. Nil leaves the counter untouched (no-op wiring).
	recorder tracer.Recorder

	// knowledgeExtractor is the L3 extractor fired asynchronously from
	// Stream to distil each completed turn into the session memory
	// store. Nil disables the feature. When knowledgeExtractorFactory
	// is non-nil it takes precedence — see dispatchKnowledgeExtraction.
	knowledgeExtractor        *recall.KnowledgeExtractor
	knowledgeExtractorFactory func(sessionID string) *recall.KnowledgeExtractor

	// extractionWG tracks in-flight knowledge-extraction goroutines so
	// short-lived CLI entry points (flowstate run) can block until the
	// background writers finish before the process exits. Without this,
	// every L3 save dispatched from a one-shot run is orphaned at
	// process termination.
	extractionWG sync.WaitGroup

	// sessionSplitters caches one HotColdSplitter per sessionID when
	// Compression.MicroCompaction.Enabled. Splitters own a persist
	// worker goroutine plus a buffered channel, so sharing a single
	// splitter across sessions would cross-contaminate storage paths
	// (StorageDir/SessionID is baked in at construction). Lazy
	// construction keeps the common "micro-compaction disabled" path
	// allocation-free. Access is serialised by splitterMu.
	//
	// Values are sessionSplitterEntry, not bare *HotColdSplitter, so
	// Item 4's idle sweeper can evict entries that have not been
	// accessed for longer than compression.micro_compaction.idle_ttl.
	sessionSplitters map[string]*sessionSplitterEntry
	splitterMu       sync.Mutex

	// sweeperStop signals the Item 4 idle-TTL splitter sweeper to
	// exit. Closed exactly once by Shutdown. A channel rather than a
	// context is used deliberately: context.WithCancel's returned
	// cancel function is flagged by gosec G118 when stashed on a
	// struct for later invocation, and the sweeper does not actually
	// need a request-shaped context — only a stop signal. The paired
	// sweeperDone channel signals when the goroutine has finished so
	// Shutdown can return only after the ticker is fully stopped.
	sweeperStop sweeperStopFunc
	sweeperDone chan struct{}

	// Item 3 removed the splitter-scoped buildWindowMu. Per-build
	// engine-owned state (lastContextResult, lastCompactionSummary)
	// is serialised under this narrower mutex instead; the splitter
	// itself is no longer involved because Build* receives it as a
	// per-call option.
	buildStateMu sync.Mutex

	// swarmContext is the T-swarm-2 envelope set when the runner
	// resolves an `@<swarm-id>` invocation. The lead engine reads it
	// (via SwarmContext()) so member-allowlist shadowing, gate
	// dispatch, and chain-prefix namespacing all see the same source
	// of truth. Nil when no swarm is in flight — the engine behaves
	// as a normal delegating agent. Held under mu because the runner
	// may install the context after construction (CLI run path) or
	// at construction time (Config.SwarmContext); both writers must
	// race-cleanly with reads from the streaming hot path.
	swarmContext *swarm.Context

	// microCompactor is the RLM Phase A Layer 1 compactor. It applies the
	// hot/cold tool-result split to the in-flight provider message slice
	// produced by buildContextWindow. Nil disables Phase A regardless of
	// CompactionConfig. The persisted history (Store, session.Messages)
	// stays full and recoverable; Compact only rewrites the request view.
	microCompactor *compaction.MicroCompactor
	// compactionConfig carries the Phase A knobs (MicroEnabled,
	// HotTailMinResults, HotTailSizeBudget). Held alongside the existing
	// CompressionConfig so the two layers can be enabled independently.
	compactionConfig compaction.Config

	// factService is the RLM Phase B Layer 3 service. It exposes a
	// Recall(query, topK) call the engine consults inside
	// buildContextWindow to prepend a "[recalled facts]" system block
	// to the in-flight provider request. Nil disables Phase B
	// regardless of compactionConfig.FactExtractionEnabled.
	factService *factstore.Service

	mu sync.RWMutex
}

// Config holds the configuration for creating a new Engine.
type Config struct {
	ChatProvider      provider.Provider
	EmbeddingProvider provider.Provider
	Registry          *provider.Registry
	AgentRegistry     *agent.Registry
	FailoverManager   *failover.Manager
	Manifest          agent.Manifest
	Tools             []tool.Tool
	Skills            []skill.Skill
	// SkillsResolver re-resolves the default-active skill set for a
	// given manifest when the engine swaps manifests in-place via
	// SetManifest. The CLI's `flowstate run --agent <id>` flow and
	// the TUI's slash-command agent switch both reuse a single root
	// engine across manifests; without this callback the engine's
	// skills slice stays pinned to the construction-time resolution
	// and the newly swapped-in manifest's declared default-active
	// skills silently drop out of LoadedSkills (and out of the
	// session sidecar).
	//
	// Nil disables re-resolution — SetManifest keeps the existing
	// skills slice, preserving historical behaviour for callers that
	// do not provide a resolver (tests, ephemeral engines).
	SkillsResolver       func(agent.Manifest) []skill.Skill
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
	// CompactionConfig holds the RLM Phase A Layer 1 (micro-compaction)
	// knobs. Defaults to disabled when zero-valued; production callers
	// pass compaction.DefaultConfig() and override individual fields.
	CompactionConfig compaction.Config
	// CompactionStoreDir is the absolute parent directory under which
	// per-session cold-storage subdirectories
	// (<dir>/<sessionID>/compacted/) are created. Empty disables disk
	// writes (Compact still rewrites the slice but the .txt payloads
	// are dropped). Typically set to the active sessions dir at App
	// wiring time.
	CompactionStoreDir string
	// FactService is the RLM Phase B Layer 3 service. When non-nil AND
	// CompactionConfig.FactExtractionEnabled is true, the engine
	// consults Recall on every buildContextWindow call to prepend a
	// "[recalled facts]" system block to the provider request. Nil
	// disables Phase B regardless of the toggle.
	FactService *factstore.Service
	// CompressionMetrics, when non-nil, is attached to the window
	// builder and the engine so L1 offloads and L2 compactions are
	// counted in a single place. Nil disables metrics.
	CompressionMetrics *ctxstore.CompressionMetrics

	// KnowledgeExtractor is the optional L3 extractor fired in a
	// background goroutine after each Stream invocation when
	// CompressionConfig.SessionMemory.Enabled is true. Nil disables the
	// feature. Prefer KnowledgeExtractorFactory for production wiring so
	// the stream's live sessionID flows into SessionMemoryStore.Save —
	// this field is retained for single-session tests that bind the
	// sessionID at construction time.
	KnowledgeExtractor *recall.KnowledgeExtractor

	// KnowledgeExtractorFactory, when non-nil, takes precedence over
	// KnowledgeExtractor: dispatchKnowledgeExtraction calls the factory
	// with the current sessionID so each Stream invocation writes its
	// memory under the session actually being streamed. App.New wires
	// this from buildCompressionComponents whenever SessionMemory is
	// enabled; tests that pin a single sessionID can keep using
	// KnowledgeExtractor directly.
	KnowledgeExtractorFactory func(sessionID string) *recall.KnowledgeExtractor

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

	// Recorder is the optional tracer.Recorder the engine uses to emit
	// compression observability metrics. When set, the engine:
	//   - forwards the recorder to the WindowBuilder so every Build call
	//     emits a RecordContextWindowTokens gauge observation; and
	//   - invokes RecordCompressionTokensSaved on every successful L2
	//     auto-compaction with the positive delta of tokens eliminated.
	// Nil leaves both emission sites silent (no-op recorder semantics).
	Recorder tracer.Recorder

	// ToolCallCorrelator is the P14 registry that assigns a stable
	// FlowState-internal identifier to every tool call observed on the
	// stream path. The engine stamps StreamChunk.InternalToolCallID from
	// this registry on every tool-related chunk so downstream consumers
	// can pair tool_call and tool_result events across a provider
	// failover boundary. Nil is tolerated — the engine lazily constructs
	// an internal correlator at New time; prefer passing one explicitly
	// when the registry must outlive a single Engine (e.g. an App that
	// recycles engines across chats within the same session).
	ToolCallCorrelator *streaming.ToolCallCorrelator

	// ToolTimeout is the maximum duration a single tool execution may
	// run before the engine cancels it. Zero falls back to the default
	// of 2 minutes.
	ToolTimeout time.Duration

	// SwarmContext is the T-swarm-2 lead-engine wiring point. When
	// non-nil, the engine treats this run as a swarm invocation: the
	// member roster shadows the lead agent's delegation.allowlist
	// (spec §2), the chain prefix namespaces the coordination_store,
	// and gates (T-swarm-3) consult the carried list. Nil leaves the
	// engine in its historical single-agent shape. Mutable post-
	// construction via SetSwarmContext when the CLI run path resolves
	// `--agent <swarm-id>` after the engine is already up.
	SwarmContext *swarm.Context

	// SystemPromptBudget overrides the model-context fallback the engine
	// returns from ModelContextLimit / ResolveContextLength when the
	// failover manager and token counter cannot supply a concrete cap
	// (no preferences set, no resolver wired, unknown model). Zero
	// inherits ctxstore.DefaultModelContextFallback (16K), which is
	// where the engine settled after replacing the historical 4096
	// default that quietly truncated ~70% of an 11-skill FlowState
	// system prompt to fit. Non-zero values are also propagated into
	// the supplied TokenCounter (when it implements ctxstore.FallbackSetter)
	// and FailoverManager so every fallback site shares the same cap.
	SystemPromptBudget int
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

	resolved := resolvedEngineDeps{
		windowBuilder: windowBuilder,
		bus:           bus,
		chain:         resolveHookChain(cfg, bus),
		assemblyHooks: buildContextAssemblyHooks(cfg),
		streamTimeout: timeout,
	}

	eng := assembleEngine(cfg, resolved)
	propagateSystemPromptBudget(cfg)
	bus.Subscribe(events.EventSessionEnded, eng.handleSessionEnded) // C1 eviction
	maybeStartIdleSweeper(eng, cfg)                                 // Item 4 sweeper
	return eng
}

// propagateSystemPromptBudget pushes the engine's configured fallback
// budget into the supplied TokenCounter (when it implements
// ctxstore.FallbackSetter) and FailoverManager so every fallback site
// agrees on the same cap. Without this, ModelContextLimit could honour
// a custom budget while a downstream WindowBuilder.Build call resolved
// via the counter still returned the package default.
//
// Expected:
//   - cfg.SystemPromptBudget may be zero (no override).
//
// Side effects:
//   - Mutates cfg.TokenCounter and cfg.FailoverManager when both the
//     budget is positive and the targets accept the override.
func propagateSystemPromptBudget(cfg Config) {
	if cfg.SystemPromptBudget <= 0 {
		return
	}
	if setter, ok := cfg.TokenCounter.(ctxstore.FallbackSetter); ok {
		setter.SetFallback(cfg.SystemPromptBudget)
	}
	if cfg.FailoverManager != nil {
		cfg.FailoverManager.SetContextFallback(cfg.SystemPromptBudget)
	}
}

// resolvedEngineDeps groups the dependencies New has already resolved
// (timeouts defaulted, hook chain selected, assembly hooks composed)
// so assembleEngine can accept a single struct argument and stay
// inside the revive argument-limit gate. Not exported — this is a
// purely internal bundle for New → assembleEngine.
type resolvedEngineDeps struct {
	windowBuilder *ctxstore.WindowBuilder
	bus           *eventbus.EventBus
	chain         *hook.Chain
	assemblyHooks []plugin.ContextAssemblyHook
	streamTimeout time.Duration
}

// resolveHookChain picks the hook chain used by New. An explicit
// cfg.HookChain wins; otherwise, if a FailoverManager is configured a
// default chain wrapping the stream-failover hook is constructed.
// Extracted so New stays inside the funlen gate.
//
// Expected:
//   - cfg is the Config being handed to New.
//   - bus is the resolved event bus (non-nil).
//
// Returns:
//   - The hook.Chain to install on the engine, or nil when neither
//     override nor failover manager asks for one.
//
// Side effects:
//   - None; pure wiring.
func resolveHookChain(cfg Config, bus *eventbus.EventBus) *hook.Chain {
	if cfg.HookChain != nil {
		return cfg.HookChain
	}
	if cfg.FailoverManager == nil {
		return nil
	}
	streamHook := failover.NewStreamHook(cfg.FailoverManager, bus, cfg.Manifest.ID)
	return hook.NewChain(func(next hook.HandlerFunc) hook.HandlerFunc {
		return streamHook.Execute(next)
	})
}

// resolveToolTimeout returns the tool-execution timeout from cfg,
// falling back to the package-level default when zero.
//
// Expected:
//   - cfg is a valid Config struct.
//
// Returns:
//   - The configured ToolTimeout, or defaultToolTimeout when zero.
//
// Side effects:
//   - None.
func resolveToolTimeout(cfg Config) time.Duration {
	if cfg.ToolTimeout > 0 {
		return cfg.ToolTimeout
	}
	return defaultToolTimeout
}

// assembleEngine builds the Engine struct literal from the resolved
// components. Separated from New so the constructor's branching is
// isolated from the field wiring and both stay under the funlen gate.
//
// Expected:
//   - cfg is the Config handed to New.
//   - deps carries dependencies New has already resolved (timeouts
//     defaulted, hook chain selected, assembly hooks composed).
//
// Returns:
//   - A newly allocated *Engine with every map initialised.
//
// Side effects:
//   - None; the event subscription and sweeper start are performed by
//     the caller after assembly.
func assembleEngine(cfg Config, deps resolvedEngineDeps) *Engine {
	return &Engine{
		chatProvider:              cfg.ChatProvider,
		embeddingProvider:         cfg.EmbeddingProvider,
		failoverManager:           cfg.FailoverManager,
		manifest:                  cfg.Manifest,
		tools:                     cfg.Tools,
		skills:                    cfg.Skills,
		skillsResolver:            cfg.SkillsResolver,
		store:                     cfg.Store,
		chainStore:                cfg.ChainStore,
		windowBuilder:             deps.windowBuilder,
		recallBroker:              cfg.RecallBroker,
		contextAssemblyHooks:      deps.assemblyHooks,
		tokenCounter:              cfg.TokenCounter,
		systemPromptBudget:        cfg.SystemPromptBudget,
		streamTimeout:             deps.streamTimeout,
		hookChain:                 deps.chain,
		toolRegistry:              cfg.ToolRegistry,
		permissionHandler:         cfg.PermissionHandler,
		providerRegistry:          cfg.Registry,
		agentRegistry:             cfg.AgentRegistry,
		agentsFileLoader:          cfg.AgentsFileLoader,
		agentOverrides:            make(map[string]string),
		bus:                       deps.bus,
		systemPromptDirty:         true,
		mcpServerTools:            cfg.MCPServerTools,
		toolTimeout:               resolveToolTimeout(cfg),
		autoCompactor:             cfg.AutoCompactor,
		compressionConfig:         cfg.CompressionConfig,
		compressionMetrics:        cfg.CompressionMetrics,
		recorder:                  cfg.Recorder,
		knowledgeExtractor:        cfg.KnowledgeExtractor,
		knowledgeExtractorFactory: cfg.KnowledgeExtractorFactory,
		sessionSplitters:          make(map[string]*sessionSplitterEntry),
		sessionCompressionMetrics: make(map[string]*ctxstore.CompressionMetrics),
		sessionCompactionMemo:     make(map[string]sessionCompactionMemoEntry),
		sessionRehydrated:         make(map[string]struct{}),
		toolCallCorrelator:        resolveToolCallCorrelator(cfg),
		swarmContext:              cfg.SwarmContext,
		microCompactor:            resolveMicroCompactor(cfg),
		compactionConfig:          cfg.CompactionConfig,
		factService:               resolveFactService(cfg),
	}
}

// resolveFactService returns the RLM Phase B service the engine should
// attach. Nil when the feature is disabled in CompactionConfig — the
// applyFactRecall call site short-circuits a nil service so feature-
// off has zero overhead on the hot path.
//
// Expected:
//   - cfg is the Config handed to New.
//
// Returns:
//   - cfg.FactService when CompactionConfig.FactExtractionEnabled is
//     true AND the caller wired a non-nil service.
//   - nil otherwise.
//
// Side effects:
//   - None.
func resolveFactService(cfg Config) *factstore.Service {
	if !cfg.CompactionConfig.FactExtractionEnabled {
		return nil
	}
	return cfg.FactService
}

// resolveMicroCompactor returns the RLM Phase A compactor the engine
// should attach. Nil when the feature is disabled in CompactionConfig
// — buildContextWindow short-circuits the call site so a nil compactor
// has zero overhead on the hot path.
//
// Expected:
//   - cfg is the Config handed to New.
//
// Returns:
//   - A configured compaction.MicroCompactor when CompactionConfig.
//     MicroEnabled is true.
//   - nil otherwise.
//
// Side effects:
//   - None.
func resolveMicroCompactor(cfg Config) *compaction.MicroCompactor {
	if !cfg.CompactionConfig.MicroEnabled {
		return nil
	}
	c := cfg.CompactionConfig
	compaction.ApplyDefaults(&c)
	return compaction.NewMicroCompactor(compaction.Options{
		StoreRoot:  cfg.CompactionStoreDir,
		HotTailMin: c.HotTailMinResults,
		SizeBudget: c.HotTailSizeBudget,
	})
}

// resolveToolCallCorrelator returns the ToolCallCorrelator the engine
// should use. An explicit cfg.ToolCallCorrelator wins so callers that
// share a correlator across multiple engines (e.g. an App recycling
// engines) can keep session-scoped registrations alive. Otherwise the
// engine lazy-constructs its own — single-engine workflows and tests
// require no ceremony.
//
// Expected:
//   - cfg is the Config handed to New.
//
// Returns:
//   - A non-nil ToolCallCorrelator.
//
// Side effects:
//   - None; purely functional.
func resolveToolCallCorrelator(cfg Config) *streaming.ToolCallCorrelator {
	if cfg.ToolCallCorrelator != nil {
		return cfg.ToolCallCorrelator
	}
	return streaming.NewToolCallCorrelator()
}

// maybeStartIdleSweeper launches the Item 4 background goroutine when
// MicroCompaction is enabled. Extracted from New so the constructor
// stays under the funlen gate; also makes the enable-guard site
// grep-able.
//
// Expected:
//   - eng is a non-nil, freshly constructed Engine.
//   - cfg is the same Config used to construct eng so the guard's
//     view of the MicroCompaction block matches New's.
//
// Side effects:
//   - Calls startIdleSweeper when MicroCompaction is enabled and its
//     IdleTTL is strictly positive; otherwise no-op.
func maybeStartIdleSweeper(eng *Engine, cfg Config) {
	if !cfg.CompressionConfig.MicroCompaction.Enabled {
		return
	}
	ttl := cfg.CompressionConfig.MicroCompaction.IdleTTL
	if ttl <= 0 {
		return
	}
	eng.startIdleSweeper(ttl)
}

// sessionSplitterEntry pairs a cached HotColdSplitter with the last
// time ensureSessionSplitter touched it. The idle sweeper uses the
// timestamp to decide whether an entry has gone stale under
// compression.micro_compaction.idle_ttl.
type sessionSplitterEntry struct {
	splitter     *ctxstore.HotColdSplitter
	lastAccessed time.Time
}

// sessionCompactionMemoEntry is the per-session H2 memoisation record.
// Hash is the SHA-256 of the cold-range messages; Summary is the
// CompactionSummary that call produced. Both fields are required for
// a hit: reuse is only safe when a summary was actually cached (not
// just the hash of an uncompacted turn).
type sessionCompactionMemoEntry struct {
	hash    [32]byte
	summary *ctxstore.CompactionSummary
}

// sweeperStopFunc is a nil-safe idempotent close-once helper. Wrapping
// a sync.Once keeps Shutdown + a second Shutdown trivially safe
// without exposing the Once to callers.
type sweeperStopFunc func()

// handleSessionEnded is the session.ended subscription registered in
// New. On delivery it looks up the HotColdSplitter cached for the
// ended session and tears it down: delete from the cache under the
// mutex, then Stop the splitter to drain its persist worker. When
// no splitter is cached (MicroCompaction disabled, or the session
// never built a window) the call is a silent no-op.
//
// Expected:
//   - evt is a *events.SessionEvent whose Data.SessionID names the
//     session being closed. Other event types arriving on the topic
//     are ignored defensively.
//
// Returns:
//   - None. Errors from Stop are not propagated — the worker either
//     drains cleanly or logs its own failures via slog.
//
// Side effects:
//   - Removes one entry from sessionSplitters under splitterMu.
//   - Joins the splitter's persist-worker goroutine via Stop.
func (e *Engine) handleSessionEnded(evt any) {
	sessionEvt, ok := evt.(*events.SessionEvent)
	if !ok {
		return
	}
	sessionID := sessionEvt.Data.SessionID
	if sessionID == "" {
		return
	}

	// C2: Stop+delete under the same critical section so concurrent
	// close paths (session.ended handler + StopSessionSplitterForTesting
	// + future Engine.Stop) serialise. Stop is idempotent via sync.Once
	// inside HotColdSplitter — the lock protects map invariants, not
	// Stop correctness.
	// Evict the per-session compression-metrics ledger alongside the
	// splitter cache so long-running flowstate serve processes do not
	// accumulate dead entries forever. Done under its own mutex — the
	// session-metrics map and splitter cache have independent lifetimes
	// (metrics entries exist for sessions that never allocated a
	// splitter, e.g. L2-only auto-compaction).
	e.sessionCompressionMetricsMu.Lock()
	delete(e.sessionCompressionMetrics, sessionID)
	e.sessionCompressionMetricsMu.Unlock()

	// H2 — evict the per-session auto-compaction memo alongside the
	// metrics ledger. Long-running flowstate serve handling many
	// sessions would otherwise accumulate memo entries forever with
	// the same lifetime problem the splitter cache and metrics map
	// had before their own eviction hooks.
	// H1 — same lifetime for the rehydration-consumed flag.
	e.buildStateMu.Lock()
	delete(e.sessionCompactionMemo, sessionID)
	delete(e.sessionRehydrated, sessionID)
	e.buildStateMu.Unlock()

	// P14 — release the tool-call correlator entries owned by the ended
	// session so the registry does not grow unbounded across a long-
	// running process. No-op when no tool calls were observed.
	if e.toolCallCorrelator != nil {
		e.toolCallCorrelator.ForgetSession(sessionID)
	}

	e.splitterMu.Lock()
	defer e.splitterMu.Unlock()

	entry, found := e.sessionSplitters[sessionID]
	if !found {
		return
	}
	delete(e.sessionSplitters, sessionID)
	entry.splitter.Stop()
}

// startIdleSweeper launches the Item 4 background goroutine that
// evicts session-splitter cache entries whose last access exceeds
// idleTTL. The sweep interval is `max(idleTTL/10, 30s)` so small
// TTLs still get a handful of sweeps per TTL (useful for tests) and
// large TTLs do not wake the goroutine needlessly often.
//
// Expected:
//   - idleTTL > 0. Engine.New guards this; Validate rejects zero at
//     config load time.
//
// Side effects:
//   - Starts one goroutine bound to a context that Shutdown cancels.
func (e *Engine) startIdleSweeper(idleTTL time.Duration) {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	e.sweeperStop = func() { once.Do(func() { close(stop) }) }
	e.sweeperDone = done

	interval := idleTTL / 10
	if interval < 30*time.Second {
		// Floor at a sensible minimum for production so the sweeper
		// does not spin; tests override by passing a tiny idleTTL and
		// accepting the correspondingly tiny interval.
		if idleTTL >= 30*time.Second {
			interval = 30 * time.Second
		}
	}

	// Pass stop and done explicitly so the goroutine can close done
	// even after Shutdown has nil-ed e.sweeperDone to mark the sweeper
	// as stopped from the caller's perspective.
	go e.runIdleSweeper(stop, done, idleTTL, interval)
}

// runIdleSweeper is the goroutine body spawned by startIdleSweeper.
// It ticks at `interval` and on each tick evicts every cache entry
// whose lastAccessed is older than `now - idleTTL`. Eviction is Stop
// + delete under splitterMu, mirroring handleSessionEnded so both
// paths are observably identical from the cache's perspective.
//
// Expected:
//   - stop is a non-nil signal channel closed by Shutdown to tell
//     the sweeper to exit.
//   - done is a non-nil completion channel the sweeper closes on
//     exit so Shutdown can block until the ticker is stopped.
//   - idleTTL and interval are both strictly positive.
//
// Side effects:
//   - Closes done exactly once on exit.
//   - Stops the internal ticker.
//   - Invokes sweepIdleSplitters on every tick.
func (e *Engine) runIdleSweeper(stop <-chan struct{}, done chan<- struct{}, idleTTL, interval time.Duration) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			e.sweepIdleSplitters(idleTTL)
		}
	}
}

// sweepIdleSplitters evicts every cached splitter whose lastAccessed
// is older than `time.Now() - idleTTL`. Extracted from runIdleSweeper
// so unit tests can drive the eviction deterministically without
// waiting on a ticker — but the production path is strictly the
// goroutine call site.
//
// Expected:
//   - idleTTL is strictly positive; callers must not pass 0 or
//     negative values (Engine.New guards this and Validate rejects
//     misconfigurations at load).
//
// Side effects:
//   - Calls Stop on each evicted splitter outside splitterMu so the
//     lock hold is proportional to map operations, not persist-worker
//     drain time.
func (e *Engine) sweepIdleSplitters(idleTTL time.Duration) {
	cutoff := time.Now().Add(-idleTTL)

	e.splitterMu.Lock()
	var toStop []*ctxstore.HotColdSplitter
	for sessionID, entry := range e.sessionSplitters {
		if entry.lastAccessed.Before(cutoff) {
			toStop = append(toStop, entry.splitter)
			delete(e.sessionSplitters, sessionID)
		}
	}
	e.splitterMu.Unlock()

	for _, s := range toStop {
		s.Stop()
	}
}

// Shutdown drains every engine-owned background resource so callers
// that are about to exit (flowstate serve on SIGTERM, test teardown)
// can guarantee no orphaned work. It is H3's fix for the gap where
// http.Server.Shutdown drained HTTP handlers but left splitter
// persist workers and L3 knowledge-extraction goroutines to be
// killed at os.Exit, orphaning `.tmp` files on disk.
//
// Steps, in order:
//
//  1. Snapshot the sessionSplitters map under splitterMu and clear
//     it. Callers that call ensureSessionSplitter concurrent with
//     Shutdown and construct a fresh splitter are acceptable — they
//     will not be tracked by this Shutdown invocation, but the
//     engine is at end-of-life so they would be discarded at exit
//     anyway. Production callers serialise Shutdown with the HTTP
//     server's Shutdown so this race does not arise in practice.
//
//  2. Stop every snapshotted splitter outside the lock. Stop is
//     idempotent via sync.Once; any concurrent close (session.ended
//     or StopSessionSplitterForTesting) is a no-op.
//
//  3. Wait for in-flight knowledge-extraction goroutines under the
//     provided ctx deadline. The goroutines are already bounded
//     internally by a 30-second per-extractor timeout; ctx bounds
//     the outer wait.
//
// Expected:
//   - ctx carries the shutdown deadline and MUST be non-nil. Callers
//     that want an unbounded wait should pass context.Background()
//     explicitly; the helper does not silently substitute one so
//     misuse surfaces as a nil-deref rather than an accidental
//     infinite drain.
//
// Returns:
//   - ctx.Err() when the ctx deadline expires before extractions
//     finish; nil otherwise. Splitter Stop does not return errors.
//
// Side effects:
//   - Drains persist workers (blocks until each returns).
//   - Waits up to ctx deadline for extraction goroutines.
//   - Leaves sessionSplitters empty; subsequent ensureSessionSplitter
//     calls reconstruct fresh splitters.
//
// Safe to call multiple times: the second call finds an empty
// splitter map and waits briefly for any late extractions.
func (e *Engine) Shutdown(ctx context.Context) error {
	// Step 0 (Item 4): stop the idle sweeper before draining splitters
	// so it cannot concurrently evict entries we're about to snapshot.
	// The stop helper is idempotent via sync.Once; a second Shutdown
	// call finds the field cleared and skips the wait.
	e.splitterMu.Lock()
	stopSweeper := e.sweeperStop
	sweeperDone := e.sweeperDone
	e.sweeperStop = nil
	e.sweeperDone = nil
	e.splitterMu.Unlock()
	if stopSweeper != nil {
		stopSweeper()
		if sweeperDone != nil {
			select {
			case <-sweeperDone:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// Step 1+2: snapshot, clear, stop.
	e.splitterMu.Lock()
	snapshot := make([]*ctxstore.HotColdSplitter, 0, len(e.sessionSplitters))
	for _, entry := range e.sessionSplitters {
		snapshot = append(snapshot, entry.splitter)
	}
	e.sessionSplitters = make(map[string]*sessionSplitterEntry)
	e.splitterMu.Unlock()

	for _, s := range snapshot {
		s.Stop()
	}

	// Step 3: bounded wait for extractions.
	done := make(chan struct{})
	go func() {
		e.extractionWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// buildContextAssemblyHooks constructs the context assembly hook chain from config.
// If a RecallBroker is provided, it is auto-registered as the first hook —
// but the registered hook itself is a no-op when the engine's manifest does
// not opt in via UsesRecall (P13). The hook inspects payload.AgentID and the
// captured manifest flag so recall fires only for agents that benefit from it.
// Any explicitly configured hooks are appended after the broker hook.
//
// Expected:
//   - cfg may contain a RecallBroker and/or ContextAssemblyHooks.
//   - cfg.Manifest.UsesRecall determines whether the broker hook runs. The
//     default (false) is opt-out — agents must declare uses_recall: true in
//     their manifest to participate.
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
		// Capture the opt-in flag by value so the hook closure does not
		// share mutable state with the Config struct after engine
		// construction — each engine instance is bound to one manifest,
		// so this flag is effectively constant for the engine's
		// lifetime.
		usesRecall := cfg.Manifest.UsesRecall
		hooks = append(hooks, func(ctx context.Context, payload *plugin.ContextAssemblyPayload) error {
			if !usesRecall {
				// P13 opt-in gate: agent did not declare uses_recall:true
				// in its manifest. Skip the broker query entirely — this
				// is the primary win of P13, removing per-turn query
				// overhead and context pollution for agents that do not
				// benefit from recalled observations.
				return nil
			}
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
	if cfg.Recorder != nil {
		builder = builder.WithRecorder(cfg.Recorder)
	}
	// Splitter attachment is deferred to buildContextWindow: each
	// HotColdSplitter is bound to a specific {StorageDir, SessionID}
	// pair, so a process-wide instance cannot serve multiple
	// sessions. Engine.ensureSessionSplitter constructs one splitter
	// per live sessionID, and Item 3 passes it into each Build* call
	// via ctxstore.WithSplitterOption so the shared WindowBuilder is
	// safe to use concurrently.
	return builder
}

// ensureSessionSplitter returns the cached HotColdSplitter for
// sessionID, constructing one on first use. Returns nil when
// MicroCompaction is disabled or the storage configuration is
// incomplete, so callers can branch on "no L1 for this session"
// without inspecting the config themselves.
//
// The splitter's persist worker is started eagerly on construction:
// Split is called non-blockingly on the hot path and would drop
// spillover jobs if the worker were not draining. Stop is NOT called
// by the engine; splitters own goroutines that exit when the process
// terminates, matching the fire-and-forget lifecycle of the recall
// store's writer. Tests that need deterministic flushing use
// SessionSplitterForTest to grab the instance and Stop it explicitly.
//
// Expected:
//   - ctx is the live request context. The persist worker is started
//     with context.WithoutCancel(ctx) so splitter lifetime tracks the
//     engine rather than the originating request — a completed Stream
//     call must not tear down the worker mid-drain.
//   - sessionID is the id of the session currently calling
//     buildContextWindow. An empty sessionID is treated as "no L1"
//     because HotColdSplitter keys its storage path on it.
//
// Returns:
//   - A *HotColdSplitter when MicroCompaction is enabled, a storage
//     directory is configured, and sessionID is non-empty.
//   - nil when any precondition fails.
//
// Side effects:
//   - May spawn one persist worker goroutine per previously-unseen
//     sessionID and take splitterMu briefly.
func (e *Engine) ensureSessionSplitter(ctx context.Context, sessionID string) *ctxstore.HotColdSplitter {
	micro := e.compressionConfig.MicroCompaction
	if !micro.Enabled || micro.StorageDir == "" || sessionID == "" {
		return nil
	}
	// H4 defence-in-depth: the CLI gate in run.go/chat.go rejects
	// path-unsafe --session values before engine methods run, but
	// programmatic callers (serve bus subscribers, integration
	// harnesses) can reach here with arbitrary sessionIDs. Collapse
	// to "no L1 this session" rather than build a filepath.Join that
	// escapes MicroCompaction.StorageDir. Logging is intentional —
	// this branch should never fire in production; if it does, an
	// operator needs to see it in the log stream.
	if err := sessionid.Validate(sessionID); err != nil {
		slog.Warn("engine refused to construct L1 splitter for unsafe session id",
			"session_id", sessionID, "error", err)
		return nil
	}

	e.splitterMu.Lock()
	defer e.splitterMu.Unlock()

	if existing, ok := e.sessionSplitters[sessionID]; ok {
		// Item 4 — refresh the lastAccessed timestamp so the idle
		// sweeper treats active sessions as fresh regardless of how
		// long ago the splitter was constructed.
		existing.lastAccessed = time.Now()
		return existing.splitter
	}

	threshold := micro.TokenThreshold
	if threshold <= 0 {
		threshold = 1000
	}
	compactor := ctxstore.NewDefaultMessageCompactor(threshold)

	splitter := ctxstore.NewHotColdSplitter(ctxstore.HotColdSplitterOptions{
		Compactor:   compactor,
		HotTailSize: micro.HotTailSize,
		StorageDir:  micro.StorageDir,
		SessionID:   sessionID,
	})
	if splitter == nil {
		// Compactor was nil — treat as misconfiguration but don't
		// panic on the hot path. Returning nil collapses to
		// "no L1 this session" which is the safe default.
		return nil
	}
	// Detach from the request cancellation chain: when a Stream ends,
	// its context is cancelled, but the splitter's persist worker
	// must keep draining pending jobs for future turns in the same
	// session. Lifetime is bounded by process termination (Stop is
	// reserved for tests).
	splitter.StartPersistWorker(context.WithoutCancel(ctx))
	e.sessionSplitters[sessionID] = &sessionSplitterEntry{
		splitter:     splitter,
		lastAccessed: time.Now(),
	}
	return splitter
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
//   - When Config.SkillsResolver was provided at construction time, the
//     engine's skills slice is re-resolved against the new manifest so
//     LoadedSkills() reflects the swapped-in manifest's declared
//     default-active skills. The CLI's `flowstate run --agent <id>`
//     flow and the TUI's slash-command agent switch both depend on
//     this: both reuse a single root engine across manifests, and
//     without the re-resolution the skills resolved at construction
//     time stick and the session sidecar records stale loaded_skills.
//     When SkillsResolver is nil the skills slice is left untouched,
//     matching historical behaviour.
func (e *Engine) SetManifest(manifest agent.Manifest) {
	e.mu.Lock()
	oldID := e.manifest.ID
	e.manifest = manifest
	e.systemPromptDirty = true
	e.cachedToolSchemas = nil
	sessionID := e.currentSessionID

	if e.skillsResolver != nil {
		e.skills = e.skillsResolver(manifest)
	}

	if dt, ok := e.getDelegateToolLocked(); ok {
		dt.SetDelegation(manifest.Delegation)
		dt.SetSourceAgentID(manifest.ID)
	}
	if st, ok := e.getSuggestDelegateToolLocked(); ok {
		st.SetSourceAgentID(manifest.ID)
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

// ManifestSnapshot satisfies swarm.DispatchEngine. It returns the
// current manifest as an opaque value the dispatch service can later
// hand back to RestoreManifest. The opaque type keeps the swarm
// package free of an agent-package import.
//
// Returns:
//   - The current agent.Manifest as an `any` value.
//
// Side effects:
//   - None.
func (e *Engine) ManifestSnapshot() any {
	return e.Manifest()
}

// RestoreManifest pairs with ManifestSnapshot to revert the engine's
// active manifest after a swarm dispatch. The dispatch service calls
// this after FlushSwarmLifecycle so the engine returns to its
// pre-dispatch identity — important for the TUI's continuing chat
// session, no-op for one-shot CLI runs.
//
// A nil snapshot or one that does not unwrap to an agent.Manifest is
// silently ignored so a misconfigured caller cannot wipe the engine's
// manifest by accident.
//
// Expected:
//   - snapshot is a value previously produced by ManifestSnapshot.
//
// Returns:
//   - None.
//
// Side effects:
//   - Calls SetManifest with the snapshotted manifest when the value
//     unwraps cleanly.
func (e *Engine) RestoreManifest(snapshot any) {
	m, ok := snapshot.(agent.Manifest)
	if !ok || m.ID == "" {
		return
	}
	e.SetManifest(m)
}

// SetSwarmContext installs the T-swarm-2 envelope on the engine. The
// runner calls this immediately before driving streaming.Run when an
// `@<swarm-id>` invocation lands, so the lead engine's delegate-tool
// allowlist, gate dispatch, and chain-prefix namespacing all see a
// consistent source of truth. Passing nil clears the swarm context
// (the engine reverts to single-agent behaviour).
//
// Expected:
//   - swarmCtx may be nil to clear.
//
// Side effects:
//   - Replaces the engine's swarmContext under the write lock.
//   - Invalidates the cached system prompt so the next BuildSystemPrompt
//     call re-runs appendSwarmLeadSection against the new context.
func (e *Engine) SetSwarmContext(swarmCtx *swarm.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.swarmContext = swarmCtx
	e.systemPromptDirty = true
}

// SwarmContext returns the T-swarm-2 envelope installed on this
// engine, or nil when no swarm is in flight. The pointer is the
// engine's live reference — callers must treat the returned value
// as read-only or copy fields they intend to mutate.
//
// Returns:
//   - The current swarm.Context pointer; nil when no swarm is set.
//
// Side effects:
//   - None.
func (e *Engine) SwarmContext() *swarm.Context {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.swarmContext
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

	base = e.appendSwarmLeadSection(base)

	if e.agentOverrides != nil {
		if appendText, ok := e.agentOverrides[e.manifest.ID]; ok && appendText != "" {
			base = base + "\n\n" + appendText
		}
	}

	e.cachedSystemPrompt = base
	e.systemPromptDirty = false

	return base
}

// appendSwarmLeadSection appends a "Swarm Leadership" block to base when
// the engine holds a swarm.Context AND the engine's manifest is the
// swarm's lead. The block tells the model:
//
//  1. its swarm identity ("You are leading swarm <id>") so it stops
//     behaving as a solo agent;
//  2. the resolved roster of member ids together with each member's
//     human-readable Name and Metadata.Role pulled from agentRegistry
//     when the registry can resolve them — falls back to the bare id
//     when the member has no registered manifest;
//  3. an explicit instruction to call the `delegate` tool with
//     `subagent_type: <member-id>` whenever a task matches a member's
//     specialty, then synthesise findings into a final report;
//  4. the canonical coord-store namespace prefix
//     "<chain_prefix>/<lead-id>/..." so every member writes under a
//     predictable path.
//
// Members who are not the lead receive the swarm.Context for chain-
// prefix namespacing but they are targets, not coordinators — so this
// section is suppressed for them. The function is pure and idempotent:
// repeated calls with the same engine state produce the same string.
//
// Defence-in-depth note (post `ADR - Swarm Dispatch Across Access
// Methods`): the "do not block on user confirmation" directive in
// the prompt body used to be load-bearing — when the TUI persistently
// re-identified the chat as the swarm lead, the lead's LLM saw a
// chat surface and hedged with "Action Required: confirm dispatch"
// preambles. With Phase 2 (one-shot dispatch via pendingSwarmLeadID)
// and Phase 3 (resolver consolidation) landed, the lead is invoked
// the same way the CLI invokes it — as a one-shot per-call Stream.
// The directive is retained as belt-and-braces so a future surface
// that re-introduces a chat-style intake by accident still gets the
// signal pushed through to the model. Same goes for the
// "do not call suggest_delegate" line: the tool already refuses
// lead-self-dispatch (errSuggestDelegateLeadSelfDispatch in
// suggest_delegate.go), but spelling it out at the prompt layer
// keeps the model from wasting tokens trying.
//
// Expected:
//   - base is the partially built system prompt; non-empty in normal
//     use but the function is robust to empty input.
//   - The caller already holds e.mu (BuildSystemPrompt does so).
//
// Returns:
//   - base unchanged when no swarm context is set or the engine is not
//     the swarm's lead.
//   - base with a "\n\n# Swarm Leadership\n..." block appended when the
//     engine is the lead.
//
// Side effects:
//   - None; reads e.swarmContext, e.manifest, and e.agentRegistry.
func (e *Engine) appendSwarmLeadSection(base string) string {
	swarmCtx := e.swarmContext
	if swarmCtx == nil {
		return base
	}
	if swarmCtx.LeadAgent == "" || swarmCtx.LeadAgent != e.manifest.ID {
		return base
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n# Swarm Leadership\n\n")
	b.WriteString("You are leading swarm `")
	b.WriteString(swarmCtx.SwarmID)
	b.WriteString("`. The user's request is owned by this swarm; you coordinate the members below rather than answering alone.\n\n")
	b.WriteString("You have already been dispatched as the lead — the user does NOT need to confirm anything. Do not write \"Action Required: confirm dispatch\", \"Proceed?\", \"Should I continue?\" or any other prompt that asks the user to approve starting the swarm. Begin by delegating to a member immediately. If the user's scope is too vague to act on, delegate the scoping work itself (e.g. to an explorer or analyst member) rather than blocking on the user. Only return to the user with the synthesised final report.\n\n")
	b.WriteString("Do NOT call `suggest_delegate` for this swarm or its members; the dispatch is already in flight, and the tool will refuse a self-dispatch suggestion. Use `delegate` for member calls.\n\n")

	b.WriteString("## Members\n\n")
	if len(swarmCtx.Members) == 0 {
		b.WriteString("- (no members declared)\n")
	}
	for _, memberID := range swarmCtx.Members {
		b.WriteString("- `")
		b.WriteString(memberID)
		b.WriteString("`")
		if name, role, ok := e.resolveSwarmMemberDetails(memberID); ok {
			if name != "" {
				b.WriteString(" — ")
				b.WriteString(name)
			}
			if role != "" {
				b.WriteString(" (")
				b.WriteString(role)
				b.WriteString(")")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Delegation\n\n")
	b.WriteString("Dispatch **independent** members in a **single message** by emitting multiple `delegate` tool calls simultaneously — do NOT wait for one independent member to finish before dispatching the next independent member. The engine runs concurrent tool calls in parallel; sequential dispatch of independent work wastes wall-clock time and burns unnecessary tokens on wait overhead.\n\n")
	b.WriteString("If some members depend on the output of earlier members (e.g. a codebase explorer that writes findings the review members will read), use sequential waves: dispatch the upstream members first, wait for their results, then dispatch the downstream members together in a single parallel message. After all member results are returned, synthesise their findings into a final report for the user.\n")

	chainPrefix := swarmCtx.ChainPrefix
	if chainPrefix == "" {
		chainPrefix = swarmCtx.SwarmID
	}
	b.WriteString("\n## Coordination namespace\n\n")
	b.WriteString("Write outputs to the coordination store under `")
	b.WriteString(chainPrefix)
	b.WriteString("/")
	b.WriteString(e.manifest.ID)
	b.WriteString("/...` so the swarm's members agree on where to read and write.\n")

	return b.String()
}

// resolveSwarmMemberDetails looks up a swarm member id in the engine's
// agent registry and returns its display Name and Metadata.Role. The
// found flag is false when the registry is nil or the member is not
// registered, in which case the caller falls back to printing only the
// bare id.
//
// Expected:
//   - memberID is the swarm member's agent id; non-empty in normal use.
//
// Returns:
//   - name, role, true when the registry resolved the id.
//   - "", "", false otherwise.
//
// Side effects:
//   - None.
func (e *Engine) resolveSwarmMemberDetails(memberID string) (string, string, bool) {
	if e.agentRegistry == nil || memberID == "" {
		return "", "", false
	}
	manifest, ok := e.agentRegistry.Get(memberID)
	if !ok || manifest == nil {
		manifest, ok = e.agentRegistry.GetByNameOrAlias(memberID)
		if !ok || manifest == nil {
			return "", "", false
		}
	}
	return manifest.Name, manifest.Metadata.Role, true
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
//   - A non-nil map of allowed tool names. Empty/nil Capabilities.Tools is
//     treated as "no tools allowed" (fail-closed) — manifests that do not
//     declare tools get nothing beyond the always-on suggest_delegate
//     escape hatch. Legacy manifests without an explicit tools list now
//     surface as "stuck" agents rather than silently inheriting the full
//     toolbelt; the loader emits a warning when such a manifest loads.
//   - When Capabilities.Tools is non-empty, MCP tools are gated by
//     Capabilities.MCPServers: each declared server name has its tools merged into
//     the allowed set. Unknown server names are silently ignored. See
//     ADR - MCP Tool Gating by Agent Manifest for the full contract.
//
// Side effects:
//   - None.
func (e *Engine) buildAllowedToolSet() map[string]bool {
	manifestTools := e.manifest.Capabilities.Tools
	allowed := make(map[string]bool, len(manifestTools)+1)
	for _, mt := range manifestTools {
		switch mt {
		case "file":
			allowed["read"] = true
			allowed["write"] = true
		case "delegate":
			allowed["delegate"] = true
			allowed["background_output"] = true
			allowed["background_cancel"] = true
			allowed["autoresearch_run"] = true
		default:
			allowed[mt] = true
		}
	}

	for _, serverName := range e.manifest.Capabilities.MCPServers {
		for _, toolName := range e.mcpServerTools[serverName] {
			allowed[toolName] = true
		}
	}

	// P12: suggest_delegate is a read-only escape hatch wired into every
	// non-delegating agent's engine. It must always be visible to the
	// model, even when the manifest restricts capabilities.tools to a
	// fixed list — otherwise the model has no legitimate way to signal
	// "the user wants me to delegate but I cannot". The corresponding
	// tool is only attached to the engine for CanDelegate=false agents,
	// so this flag is a no-op when the tool is absent.
	allowed["suggest_delegate"] = true

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
		e.dispatchKnowledgeExtraction(sessionID, messages)
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
func (e *Engine) dispatchKnowledgeExtraction(sessionID string, messages []provider.Message) {
	if !e.compressionConfig.SessionMemory.Enabled {
		return
	}

	extractor := e.resolveKnowledgeExtractor(sessionID)
	if extractor == nil {
		return
	}

	msgsCopy := make([]provider.Message, len(messages))
	copy(msgsCopy, messages)

	e.extractionWG.Add(1)
	go func() {
		defer e.extractionWG.Done()
		runKnowledgeExtraction(extractor, msgsCopy)
	}()
}

// ErrExtractionTimeout is returned by WaitForBackgroundExtractions
// when the wait expired with work still in flight. It is the sentinel
// callers check to distinguish "timed out after actually waiting" from
// every other nil-error outcome (clean finish, or the no-wait skip
// path taken when timeout <= 0). Pre-M7 the method returned a plain
// bool, and `false` conflated "timed out" with "skipped because
// timeout <= 0" — the CLI warn fired on both, producing a spurious
// warning on every run that opted out of waiting.
var ErrExtractionTimeout = errors.New("engine: background extraction wait timed out")

// WaitForBackgroundExtractions blocks until every in-flight L3
// extraction goroutine dispatched by dispatchKnowledgeExtraction has
// returned, or until timeout elapses — whichever comes first. Long-
// running hosts (flowstate serve) need never call this because the
// goroutines eventually complete under their own 30-second timeout and
// the server keeps the process alive. Short-lived CLI hosts (flowstate
// run) must call this before exiting or the extractions are orphaned
// at os.Exit and the session-memory store is never saved.
//
// Expected:
//   - timeout is the maximum wall-clock duration to wait. A non-
//     positive value is treated as "no wait" and the call returns
//     nil immediately (retaining the legacy fire-and-forget contract
//     when the caller does not care).
//
// Returns:
//   - nil when every dispatched goroutine finished within the timeout
//     OR the caller opted out of waiting with timeout <= 0. Both
//     cases share the "no work to warn about" semantics.
//   - ErrExtractionTimeout when the wait expired with work still in
//     flight. In-flight work continues but the caller is free to
//     proceed; the L3 save may be incomplete.
//
// Side effects:
//   - Blocks the caller's goroutine for at most timeout.
func (e *Engine) WaitForBackgroundExtractions(timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	done := make(chan struct{})
	go func() {
		e.extractionWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return ErrExtractionTimeout
	}
}

// resolveKnowledgeExtractor returns the extractor to use for the given
// sessionID. When a factory is wired (production path via
// buildCompressionComponents) it is called with the live sessionID so
// SessionMemoryStore.Save writes under the session actually being
// streamed. When only the static extractor is wired (single-session
// tests) it is returned unchanged. Nil signals "feature disabled for
// this engine".
//
// Expected:
//   - sessionID may be empty; the factory receives it verbatim and is
//     expected to tolerate that (it will produce a memory-dir of "").
//
// Returns:
//   - The extractor to drive, or nil when neither factory nor static
//     extractor is wired.
//
// Side effects:
//   - None at this layer. The factory, if supplied, may allocate.
func (e *Engine) resolveKnowledgeExtractor(sessionID string) *recall.KnowledgeExtractor {
	if e.knowledgeExtractorFactory != nil {
		return e.knowledgeExtractorFactory(sessionID)
	}
	return e.knowledgeExtractor
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

		if len(result.toolCalls) == 0 {
			e.completeResponse(ctx, sessionID, result.responseContent, result.thinkingContent)
			return
		}

		// Persist all tool_use blocks in a single assistant message before any
		// branch that can exit early (permission denied, ErrToolNotFound,
		// execute failure). The transcript must retain the model's intent even
		// when execution is skipped — otherwise the persisted session is
		// indistinguishable from the model having replied with no tool use at
		// all (session-1776623141279480382).
		e.storeAssistantToolUseBatch(result.toolCalls, result.responseContent)

		// Permission checks are sequential and fast — run them before launching
		// any goroutines so a denied call halts the whole batch cleanly.
		for _, tc := range result.toolCalls {
			if denied := e.checkToolPermission(tc, outChan); denied {
				return
			}
		}

		// Execute all tool calls. When the batch has more than one call we fan
		// out into goroutines so the engine's concurrent dispatch path fires.
		// Single-call batches use the same path for uniformity.
		execResults := e.executeToolCallBatch(ctx, sessionID, result.toolCalls, outChan)

		// Emit error and halt on the first failure (preserve original behaviour).
		for _, er := range execResults {
			if er.err != nil {
				outChan <- provider.StreamChunk{Error: er.err, Done: true}
				return
			}
		}

		// Persist results and emit tool_result chunks in deterministic order.
		for _, er := range execResults {
			e.storeToolResult(er.toolCall, er.toolResult)

			resultContent := er.toolResult.Output
			isError := er.toolResult.Error != nil
			if isError {
				resultContent = "Error: " + er.toolResult.Error.Error()
			}
			// P14: re-resolve the internal id so the tool_result chunk carries
			// the same InternalToolCallID as the originating tool_call.
			outChan <- provider.StreamChunk{
				EventType:  "tool_result",
				ToolCallID: er.toolCall.ID,
				InternalToolCallID: e.toolCallCorrelator.InternalID(
					sessionID, er.toolCall.ID, er.toolCall.Name, er.toolCall.Arguments,
				),
				ToolResult: &provider.ToolResultInfo{
					Content: resultContent,
					IsError: isError,
				},
			}
		}

		toolResults := make([]tool.Result, len(execResults))
		for i, er := range execResults {
			toolResults[i] = er.toolResult
		}
		messages = e.appendToolResultsBatchToMessages(messages, result.toolCalls, toolResults)

		attempt++
		var streamErr error
		providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
		if streamErr != nil {
			outChan <- provider.StreamChunk{Error: streamErr, Done: true}
			return
		}
	}
}

// toolCallExecResult holds the outcome of a single tool call execution.
type toolCallExecResult struct {
	toolCall   *provider.ToolCall
	toolResult tool.Result
	err        error
}

// executeToolCallBatch runs all tool calls concurrently and returns results in
// the same order as the input slice. A single-element batch still goes through
// this path so the message-assembly code is uniform.
func (e *Engine) executeToolCallBatch(
	ctx context.Context, sessionID string, toolCalls []*provider.ToolCall, outChan chan<- provider.StreamChunk,
) []toolCallExecResult {
	results := make([]toolCallExecResult, len(toolCalls))
	if len(toolCalls) == 1 {
		tc := toolCalls[0]
		tr, err := e.executeToolCall(WithStreamOutput(ctx, outChan), sessionID, tc)
		results[0] = toolCallExecResult{toolCall: tc, toolResult: tr, err: err}
		return results
	}

	var wg sync.WaitGroup
	for i, tc := range toolCalls {
		wg.Add(1)
		go func(i int, tc *provider.ToolCall) {
			defer wg.Done()
			tr, err := e.executeToolCall(WithStreamOutput(ctx, outChan), sessionID, tc)
			results[i] = toolCallExecResult{toolCall: tc, toolResult: tr, err: err}
		}(i, tc)
	}
	wg.Wait()
	return results
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
	toolCalls       []*provider.ToolCall // all tool calls emitted in one assistant turn
	responseContent string
	thinkingContent string
	done            bool
}

// turnOpenMarker is the thinking payload surfaced on the synthetic flush
// emitted when a provider opens an assistant turn with a bare tool_use.
// The invariant pinned at the engine Stream seam (documented across the
// vault: Chat TUI Message Rendering Order Fix, Session Rendering
// Consistency, ADR - Streaming Architecture) requires the consumer to
// observe a content or thinking artefact before the first tool_use of a
// turn. A single space is the minimum non-empty payload that trips the
// consumer's FlushPartialResponse path without polluting the transcript:
// the session accumulator's thinkingBuf swallows it at the next boundary,
// and chat renderers treat whitespace-only thinking as a no-op.
const turnOpenMarker = " "

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
//   - Forwards a synthetic thinking chunk ahead of the first tool_use of a
//     turn when no content or thinking has yet been surfaced. This pins the
//     canonical thinking/text -> tool_use ordering at the Stream seam so
//     every consumer (TUI, CLI, SSE, WS) observes a flushable artefact
//     before the tool artefact.
func (e *Engine) processStreamChunks(
	ctx context.Context, sessionID string, providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk,
) streamChunkResult {
	var responseContent strings.Builder
	var thinkingContent strings.Builder
	// sawTextOrThinking tracks whether any Content or Thinking chunk has
	// been forwarded to outChan for the current turn. The assistant-turn
	// artefact-ordering invariant requires at least one such artefact to
	// precede the first tool_use surfaced to the consumer.
	var sawTextOrThinking bool
	var toolCalls []*provider.ToolCall

	for {
		select {
		case <-ctx.Done():
			outChan <- provider.StreamChunk{Error: ctx.Err(), Done: true, ModelID: e.LastModel()}
			return streamChunkResult{responseContent: responseContent.String(), thinkingContent: thinkingContent.String(), done: true}
		case chunk, ok := <-providerChunks:
			if !ok {
				return streamChunkResult{
					toolCalls:       toolCalls,
					responseContent: responseContent.String(),
					thinkingContent: thinkingContent.String(),
				}
			}

			chunk.ModelID = e.LastModel()

			// Dispatch by chunk shape rather than EventType so the loop
			// matches the session accumulator (internal/session/accumulator.go:98)
			// and cannot silently drop tool calls when a provider forgets to
			// stamp EventType. Non-anthropic OpenAI-compatible providers hit this
			// path; anthropic and ollama continue to stamp EventType so this check
			// is strictly more permissive for them without behavioural change.
			if chunk.ToolCall != nil {
				e.publishToolReasoningEvent(sessionID, chunk.ToolCall.Name, responseContent.String())
				e.forwardToolCallChunk(sessionID, chunk, &thinkingContent, sawTextOrThinking, outChan)
				sawTextOrThinking = true // the tool_call chunk acts as the turn-open marker
				toolCalls = append(toolCalls, chunk.ToolCall)
				continue // keep reading — there may be more tool calls in this turn
			}

			thinkingContent.WriteString(chunk.Thinking)
			responseContent.WriteString(chunk.Content)
			if chunk.Content != "" || chunk.Thinking != "" {
				sawTextOrThinking = true
			}

			if chunk.Done {
				// Do NOT forward the Done chunk when tool calls are pending.
				// Emitting Done while the tool loop is still running would
				// cause every consumer (TUI, CLI, SSE) to treat the stream
				// as complete before tool results are appended, ending the
				// turn prematurely. The tool loop emits its own terminal
				// events after all results are collected.
				if len(toolCalls) > 0 {
					return streamChunkResult{
						toolCalls:       toolCalls,
						responseContent: responseContent.String(),
						thinkingContent: thinkingContent.String(),
					}
				}
				outChan <- chunk
				return streamChunkResult{
					responseContent: responseContent.String(),
					thinkingContent: thinkingContent.String(),
					done:            true,
				}
			}
			outChan <- chunk
		}
	}
}

// publishToolReasoningEvent announces the reasoning text a provider
// accumulated before calling a tool, so observability plugins can pair
// the tool_call with the model's preceding rationale. No-op when the
// event bus is unset or no reasoning text has accumulated.
//
// Expected:
//   - sessionID identifies the session the reasoning belongs to.
//   - toolName is the name of the tool the model is about to call.
//   - reasoning is the response content accumulated prior to the tool_use.
//
// Side effects:
//   - Publishes EventToolReasoning on e.bus when reasoning is non-empty.
func (e *Engine) publishToolReasoningEvent(sessionID, toolName, reasoning string) {
	if e.bus == nil || reasoning == "" {
		return
	}
	e.bus.Publish(events.EventToolReasoning, events.NewToolReasoningEvent(events.ToolReasoningEventData{
		SessionID:        sessionID,
		AgentID:          e.manifest.ID,
		ToolName:         toolName,
		ReasoningContent: reasoning,
	}))
}

// forwardToolCallChunk emits the ordering-gate flush (when required),
// stamps the FlowState-internal id, and forwards the provider's tool_call
// chunk to the consumer. Extracted from processStreamChunks to keep the
// main loop's cognitive complexity within the project's gocognit budget.
//
// Expected:
//   - chunk.ToolCall is non-nil (caller must have already dispatched by
//     chunk shape).
//   - sawTextOrThinking reports whether any Content or Thinking chunk has
//     been forwarded to outChan for the current turn.
//
// Side effects:
//   - Forwards a synthetic thinking chunk carrying turnOpenMarker ahead of
//     the tool_use when sawTextOrThinking is false, and appends the same
//     marker to thinkingContent so the completeResponse path sees the
//     flushed payload.
//   - Forwards chunk (with InternalToolCallID stamped) to outChan.
func (e *Engine) forwardToolCallChunk(
	sessionID string, chunk provider.StreamChunk, thinkingContent *strings.Builder,
	sawTextOrThinking bool, outChan chan<- provider.StreamChunk,
) {
	// Flush a synthetic turn-open marker when no content or thinking has
	// yet been surfaced to the consumer for this turn. Providers that open
	// a turn with a bare function call (openaicompat with tool-first
	// agents, or any anthropic-shape stream where the model omits
	// preamble) would otherwise violate the canonical thinking/text ->
	// tool_use ordering documented in the vault. Emitting the marker here
	// -- before the tool_use reaches outChan -- gives every downstream
	// consumer a flushable artefact to anchor their partial-response
	// commit against.
	if !sawTextOrThinking {
		outChan <- provider.StreamChunk{
			Thinking: turnOpenMarker,
			ModelID:  e.LastModel(),
		}
		thinkingContent.WriteString(turnOpenMarker)
	}
	// P14: stamp the FlowState-internal id so downstream consumers can
	// pair this call with its eventual result even if a failover rewrites
	// the provider-scoped id.
	chunk.InternalToolCallID = e.toolCallCorrelator.InternalID(
		sessionID, chunk.ToolCallID, chunk.ToolCall.Name, chunk.ToolCall.Arguments,
	)
	outChan <- chunk
}

// deriveToolCtx returns the context a single tool invocation runs under,
// along with the cancel func the caller MUST invoke after Execute returns.
//
// The engine's default behaviour wraps every tool call in a short
// per-tool deadline (e.toolTimeout, typically 2 minutes) — that budget
// fits bash/read/web-style tools whose latency is shell-bounded. Tools
// whose execution is structurally longer — notably DelegateTool, which
// runs a full multi-turn sub-agent conversation — implement
// tool.TimeoutOverrider to opt out of the default budget:
//
//   - Timeout() > 0: the engine applies that duration as the per-tool
//     deadline instead of the default. Parent ctx deadlines still
//     apply; whichever expires first wins.
//   - Timeout() == 0: the engine installs no deadline of its own. The
//     tool inherits the parent context unchanged — parent cancellation
//     still cascades, but no engine-injected wall clock caps execution.
//
// Expected:
//   - parent is the caller's context; never nil.
//   - t is the resolved Tool about to be executed.
//
// Returns:
//   - A context to hand to Tool.Execute.
//   - The cancel func to invoke after Execute returns (safe to call in every path).
//
// Side effects:
//   - None; pure context derivation.
func (e *Engine) deriveToolCtx(parent context.Context, t tool.Tool) (context.Context, context.CancelFunc) {
	if override, ok := t.(tool.TimeoutOverrider); ok {
		if budget := override.Timeout(); budget > 0 {
			return context.WithTimeout(parent, budget)
		}
		// Inherit parent context unchanged, but still return a cancel
		// func the caller can invoke in every path for symmetry.
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, e.toolTimeout)
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

		validated, valErr := ValidateToolArgs(t.Schema(), input.Arguments)
		if valErr != nil {
			slog.Warn("tool argument validation failed", "tool", toolCall.Name, "error", valErr)
			result := tool.Result{Output: valErr.Error(), Error: valErr}
			e.publishToolAfterEvent(sessionID, toolCall.Name, toolCall.Arguments, result.Output, valErr)
			return result, nil
		}
		input.Arguments = validated

		toolCtx, cancel := e.deriveToolCtx(ctx, t)
		result, err := t.Execute(toolCtx, input)
		cancel()
		if err != nil && ctx.Err() == nil {
			// Tool-level timeout, not parent cancellation.
			slog.Warn("tool execution error", "tool", toolCall.Name, "error", err)
		}
		result.Error = err
		e.publishToolAfterEvent(sessionID, toolCall.Name, toolCall.Arguments, result.Output, err)
		// A *swarm.GateError signals that a post-member or post-swarm
		// gate refused this tool call's output (or its preconditions).
		// Returning nil here would let the parent agent's tool loop
		// silently absorb the failure as a tool_result/IsError chunk
		// and continue dispatching to the next member — the failure
		// mode that motivated this branch. Promote the gate error to
		// the outer return so streamWithTools terminates the stream
		// (engine.go around line 2110: `if err != nil { outChan <-
		// {Error, Done: true}; return }`), aborting the swarm
		// dispatch as the bug-hunt manifest's `failurePolicy: halt`
		// (default) intends. Non-gate tool errors keep the historical
		// soft-fail behaviour so a transient bash failure or a tool
		// timeout doesn't take the whole conversation down.
		var gateErr *swarm.GateError
		if errors.As(err, &gateErr) {
			return result, gateErr
		}
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
		ModelID: e.LastModel(),
	})
}

// storeAssistantToolUseBatch appends a single assistant message that contains
// all tool_use blocks from a parallel dispatch turn. Callers with a batch of
// one call may use this instead of storeAssistantToolUse for uniform handling.
func (e *Engine) storeAssistantToolUseBatch(toolCalls []*provider.ToolCall, content string) {
	if e.store == nil || len(toolCalls) == 0 {
		return
	}
	calls := make([]provider.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		calls[i] = provider.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
	}
	e.store.Append(provider.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: calls,
		ModelID:   e.LastModel(),
	})
}

// storeToolResult appends a tool result message to the context store.
//
// Expected:
//   - toolCall carries the upstream tool-use identifier and tool name from
//     the provider stream; both fields are load-bearing for validator output,
//     session rehydration, and cross-provider correlation. The paired
//     assistant tool_use message on the same turn carries ID+Name, so the
//     tool-result message must too. Passing only the ID — as earlier code
//     did — persisted a tool-role message with Name="" which the harness
//     validator surfaces as "WARNING: N tool_call(s) with empty Name".
//   - result contains the tool's output or error.
//
// Side effects:
//   - Appends a message to the context store if configured, carrying both
//     the tool_use ID and the tool name on the persisted ToolCall.
func (e *Engine) storeToolResult(toolCall *provider.ToolCall, result tool.Result) {
	if e.store == nil {
		return
	}
	if toolCall == nil {
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
			{ID: toolCall.ID, Name: toolCall.Name},
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

// appendToolResultsBatchToMessages adds a single assistant message (with all
// tool_calls from a parallel turn) followed by one tool-result message per
// call, preserving the order of toolCalls/results.
//
// The OpenAI-compat protocol requires that tool_result messages follow the
// assistant message that issued the corresponding tool_calls in the same order.
func (e *Engine) appendToolResultsBatchToMessages(
	messages []provider.Message, toolCalls []*provider.ToolCall, results []tool.Result,
) []provider.Message {
	if len(toolCalls) == 0 {
		return messages
	}

	// One assistant message listing ALL tool calls.
	calls := make([]provider.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		calls[i] = provider.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
	}
	messages = append(messages, provider.Message{
		Role:      "assistant",
		ToolCalls: calls,
	})

	// One tool-result message per call, in the same order.
	for i, tc := range toolCalls {
		content := results[i].Output
		if results[i].Error != nil {
			content = "Error: " + results[i].Error.Error()
		}
		messages = append(messages, provider.Message{
			Role:    "tool",
			Content: content,
			ToolCalls: []provider.ToolCall{
				{ID: tc.ID, Name: tc.Name},
			},
		})
	}

	return messages
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

	// Item 3 — splitter is a per-Build option so the shared
	// WindowBuilder no longer needs external serialisation to avoid
	// cross-session contamination. The previous buildWindowMu has
	// been removed; each Build* call receives its own splitter via
	// WithSplitterOption, constructed below.
	splitterOpt := ctxstore.WithSplitterOption(e.ensureSessionSplitter(ctx, sessionID))

	microBefore := e.snapshotAggregateMicroCount()

	e.mu.RLock()
	defer e.mu.RUnlock()

	manifestCopy := e.manifest
	manifestCopy.Instructions.SystemPrompt = systemPrompt

	searchResults := e.dispatchContextAssemblyHooks(ctx, sessionID, userMessage, tokenBudget)

	compactedSummary := e.maybeAutoCompact(ctx, sessionID, &manifestCopy, tokenBudget)

	result := e.assembleBuildResult(buildResultInputs{
		manifest:         &manifestCopy,
		userMessage:      userMessage,
		tokenBudget:      tokenBudget,
		searchResults:    searchResults,
		compactedSummary: compactedSummary,
		splitterOpt:      splitterOpt,
	})

	// H1 — rehydrate FilesToRestore once per compaction summary.
	// Must happen after assembleBuildResult so the rehydrated
	// messages are inserted into the already-formed window rather
	// than participating in token-budget decisions for which the
	// WindowBuilder has no knowledge of the extra content.
	result.Messages = e.maybeRehydrate(sessionID, result.Messages)

	// RLM Phase B — Layer 3 fact recall. Inserted between the system
	// prompt and the rest of history so micro-compaction (next step)
	// sees the recall block in its final position. Recalled facts are
	// system-role and never look like tool results, so Phase A leaves
	// them alone.
	result.Messages = e.applyFactRecall(ctx, sessionID, userMessage, result.Messages)

	// RLM Phase A — Layer 1 micro-compaction. Applied last so the
	// hot/cold split sees the final in-flight slice (system prompt,
	// rehydrated files, recall observations, recent history). The
	// persisted Store is untouched: only the provider request gets
	// the rewritten view.
	result.Messages = e.applyMicroCompaction(ctx, sessionID, result.Messages)

	slog.Info("engine context window", "tokenBudget", tokenBudget, "messages", len(result.Messages))

	e.attributeMicroCompactionToSession(sessionID, microBefore)
	e.logSessionCompressionMetrics(sessionID)

	e.buildStateMu.Lock()
	e.lastContextResult = result
	e.buildStateMu.Unlock()
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
	threshold, ok := e.autoCompactionThreshold(manifest, tokenBudget)
	if !ok {
		// Feature disabled or preconditions unmet — clear the cross-
		// session "last summary" so LastCompactionSummary reflects
		// the current build rather than stale state from earlier
		// turns. The per-session memo is NOT cleared on this branch:
		// disabling compaction for one turn (e.g. tokenBudget <= 0
		// during a degraded build) should not force the next enabled
		// turn to re-summarise if the cold prefix has not changed.
		e.buildStateMu.Lock()
		e.lastCompactionSummary = nil
		e.buildStateMu.Unlock()
		return ""
	}

	recent, recentTokens, fire := e.autoCompactionCandidates(manifest, tokenBudget, threshold)
	if !fire {
		// Below threshold — clear the cross-session pointer as
		// before. Same per-session-memo retention rationale applies.
		e.buildStateMu.Lock()
		e.lastCompactionSummary = nil
		e.buildStateMu.Unlock()
		return ""
	}

	// H2 memoisation. Hash the cold-range identity; if the session's
	// stored entry matches AND a summary is cached, reuse that summary
	// instead of re-invoking the summariser. Per-session keying: a
	// hash collision between two sessions does not rob session B of
	// its own ContextCompactedEvent and per-session metrics bump.
	currentHash := coldRangeHash(recent)
	if reused, hit := e.reuseMemoisedSummary(sessionID, currentHash, recentTokens); hit {
		return reused
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
	e.buildStateMu.Lock()
	e.lastCompactionSummary = &summaryCopy
	e.sessionCompactionMemo[sessionID] = sessionCompactionMemoEntry{
		hash:    currentHash,
		summary: &summaryCopy,
	}
	// H1 — a fresh compaction produces a new summary with its own
	// FilesToRestore. Clear the consumed flag so buildContextWindow
	// knows to rehydrate against this new summary on the next turn.
	delete(e.sessionRehydrated, sessionID)
	e.buildStateMu.Unlock()

	summaryText := "[auto-compacted summary]: " + string(summaryJSON)
	e.publishContextCompactedEvent(sessionID, manifest.ID, recentTokens, summaryText, latency)
	return summaryText
}

// reuseMemoisedSummary looks up the per-session H2 memo and returns a
// previously-produced summary text when the cold-range hash matches
// the cached entry. Extracted from maybeAutoCompact so the funlen gate
// on the trigger stays comfortably green and the reuse policy
// (no event re-emission, no metrics re-bump) is one self-contained
// block.
//
// Expected:
//   - sessionID identifies the active session (keys the memo map).
//   - currentHash is coldRangeHash(recent) for the turn being built.
//   - recentTokens is carried only for logging on the marshal-failure
//     branch.
//
// Returns:
//   - (summaryText, true) on a memo hit; the caller should return
//     summaryText verbatim.
//   - ("", false) on a miss OR on a hit whose remarshal failed (fall
//     through to fresh compaction in the caller — the threshold says
//     a summary is wanted).
//
// Side effects:
//   - Updates e.lastCompactionSummary on hit so the engine-level
//     pointer stays consistent with the summary injected into the
//     assembled window.
//   - Logs a warning on remarshal failure.
func (e *Engine) reuseMemoisedSummary(sessionID string, currentHash [32]byte, recentTokens int) (string, bool) {
	e.buildStateMu.Lock()
	cached, hit := e.sessionCompactionMemo[sessionID]
	e.buildStateMu.Unlock()
	if !hit || cached.summary == nil || cached.hash != currentHash {
		return "", false
	}
	summaryJSON, err := json.Marshal(*cached.summary)
	if err != nil {
		// Marshal failure on a previously-marshalled struct is a
		// programming error; signal a miss so the caller falls through
		// to a fresh compaction rather than returning "" — returning
		// empty would assemble a window without the summary the
		// threshold says we want.
		slog.Warn("engine auto-compaction memo remarshal failed; refreshing",
			"error", err,
			"recentTokens", recentTokens,
		)
		return "", false
	}
	e.buildStateMu.Lock()
	e.lastCompactionSummary = cached.summary
	e.buildStateMu.Unlock()
	return "[auto-compacted summary]: " + string(summaryJSON), true
}

// maybeRehydrate resolves the FilesToRestore listed on the session's
// current CompactionSummary and returns a new message slice with the
// file contents inserted just before the trailing user turn, or
// before the tail if no user turn is present.
//
// Consume-once semantics: the first call after a fresh compaction
// reads the files and sets the sessionRehydrated flag; subsequent
// builds that see the same summary skip the disk I/O and return msgs
// unchanged. The flag clears when a new compaction produces a new
// summary (see maybeAutoCompact) and when the session ends.
//
// Graceful degradation on missing files: the audit flagged re-read
// of moved/deleted files as a real risk. A read failure on any
// listed path logs a warning and skips that file; the rest of the
// rehydration still fires. The build never aborts.
//
// Expected:
//   - sessionID identifies the active session.
//   - msgs is the already-assembled window, including the trailing
//     user turn the Summary path appends via appendUserMessageToResult.
//
// Returns:
//   - msgs unchanged when rehydration is not applicable (no summary,
//     no autoCompactor, no FilesToRestore, already consumed).
//   - A new slice with one provider.Message per rehydrated file
//     inserted before the trailing user turn, when applicable.
//
// Side effects:
//   - One os.ReadFile per listed path on the consume turn.
//   - Sets e.sessionRehydrated[sessionID] on a successful rehydration.
func (e *Engine) maybeRehydrate(sessionID string, msgs []provider.Message) []provider.Message {
	if e.autoCompactor == nil {
		return msgs
	}
	e.buildStateMu.Lock()
	summary := e.lastCompactionSummary
	_, consumed := e.sessionRehydrated[sessionID]
	e.buildStateMu.Unlock()
	if summary == nil || consumed || len(summary.FilesToRestore) == 0 {
		return msgs
	}

	rehydrated, err := e.autoCompactor.Rehydrate(*summary)
	if err != nil {
		// Rehydrate's all-or-nothing contract returns on first
		// read failure. The engine relaxes that into best-effort:
		// we log the failure and fall back to per-file reads so a
		// single missing entry does not rob the turn of the rest.
		slog.Warn("engine rehydration failed; falling back to per-file reads",
			"session_id", sessionID,
			"error", err,
		)
		rehydrated = e.rehydrateBestEffort(sessionID, summary)
	}

	// Mark consumed even on a best-effort path — re-reading next
	// turn will not make missing files suddenly present, and re-
	// reading present files duplicates the content in-window.
	e.buildStateMu.Lock()
	e.sessionRehydrated[sessionID] = struct{}{}
	e.buildStateMu.Unlock()

	if len(rehydrated) == 0 {
		return msgs
	}
	return insertBeforeUserTurn(msgs, rehydrated)
}

// applyMicroCompaction runs the RLM Phase A compactor on the in-flight
// message slice. When the compactor is nil (feature disabled or
// mis-configured at construction), msgs is returned unchanged.
//
// Expected:
//   - ctx is the request context; cancellation aborts compaction with
//     a fall-through to the original slice.
//   - sessionID identifies the session whose cold storage receives any
//     spilled .txt payloads. An empty sessionID is treated as a
//     no-op for safety.
//   - msgs is the finalised provider request slice from
//     assembleBuildResult and maybeRehydrate.
//
// Returns:
//   - The compacted slice on success.
//   - The original slice on compactor failure (the engine prefers a
//     full window over a half-rewritten one — Phase A is best-effort).
//
// Side effects:
//   - May write per-message .txt payloads under
//     <CompactionStoreDir>/<sessionID>/compacted/.
//   - Logs a warning when Compact returns an error; never panics.
func (e *Engine) applyMicroCompaction(ctx context.Context, sessionID string, msgs []provider.Message) []provider.Message {
	if e.microCompactor == nil || sessionID == "" || len(msgs) == 0 {
		return msgs
	}
	out, err := e.microCompactor.Compact(ctx, sessionID, msgs)
	if err != nil {
		slog.Warn("engine micro-compaction failed; using full window",
			"session_id", sessionID,
			"error", err,
		)
		return msgs
	}
	return out
}

// applyFactRecall asks the Phase B service for the top-K facts most
// relevant to userMessage and splices a single "[recalled facts]"
// system message between the system prompt and the rest of msgs.
//
// Expected:
//   - ctx is the request context.
//   - sessionID identifies the session whose facts.jsonl is consulted;
//     an empty sessionID is a no-op.
//   - userMessage is the next user turn — the recall query. Empty
//     queries degrade to "most recent K" inside the store.
//   - msgs is the in-flight slice from assembleBuildResult /
//     maybeRehydrate.
//
// Returns:
//   - msgs with the recall block inserted at index 1 (immediately
//     after the system prompt) when the service yields ≥1 fact.
//   - msgs unchanged when the service is nil, the toggle is off, the
//     session has no facts, or the recall call fails.
//
// Side effects:
//   - May read <sessionsDir>/<sessionID>/facts.jsonl.
//   - Logs a warning on Recall errors; never panics.
func (e *Engine) applyFactRecall(ctx context.Context, sessionID, userMessage string, msgs []provider.Message) []provider.Message {
	if e.factService == nil || sessionID == "" || len(msgs) == 0 {
		return msgs
	}
	hits, err := e.factService.Recall(ctx, sessionID, userMessage, 0)
	if err != nil {
		slog.Warn("engine fact recall failed; using full window",
			"session_id", sessionID,
			"error", err,
		)
		return msgs
	}
	if len(hits) == 0 {
		return msgs
	}
	return insertFactRecallBlock(msgs, formatFactRecallBlock(hits))
}

// formatFactRecallBlock turns the ranked Fact slice into the system-
// message body the engine splices into the request slice.
//
// Returns:
//   - A multi-line string starting with "[recalled facts]" and one
//     "- <text>" per fact.
func formatFactRecallBlock(facts []factstore.Fact) string {
	var b strings.Builder
	b.WriteString("[recalled facts]\n")
	for _, f := range facts {
		b.WriteString("- ")
		b.WriteString(f.Text)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// insertFactRecallBlock splices a "[recalled facts]" system message
// into msgs at index 1 when msgs[0] is the system prompt; falls back
// to prepending when no system prompt is present.
//
// Expected:
//   - msgs is non-empty.
//   - body is the pre-formatted recall block.
//
// Returns:
//   - A new slice with the recall block inserted.
func insertFactRecallBlock(msgs []provider.Message, body string) []provider.Message {
	block := provider.Message{Role: "system", Content: body}
	if len(msgs) > 0 && msgs[0].Role == "system" {
		out := make([]provider.Message, 0, len(msgs)+1)
		out = append(out, msgs[0], block)
		out = append(out, msgs[1:]...)
		return out
	}
	out := make([]provider.Message, 0, len(msgs)+1)
	out = append(out, block)
	out = append(out, msgs...)
	return out
}

// IngestForFactsForTest exposes the Phase B service's IngestSession
// method to wiring tests so they can deterministically populate the
// fact store before asserting against BuildContextWindowForTest. No-op
// when the service is nil.
//
// Expected:
//   - ctx is the test context.
//   - sessionID is non-empty.
//   - msgs is the synthetic message history fed to the extractor.
//
// Returns:
//   - A non-nil error only when the underlying service propagates one.
func (e *Engine) IngestForFactsForTest(ctx context.Context, sessionID string, msgs []provider.Message) error {
	if e.factService == nil {
		return nil
	}
	return e.factService.IngestSession(ctx, sessionID, msgs)
}

// rehydrateBestEffort iterates FilesToRestore and reads each in turn,
// logging missing entries and returning the subset that was readable.
// Used when AutoCompactor.Rehydrate's all-or-nothing contract trips
// on a single missing file but the engine wants to continue with the
// readable remainder.
//
// Expected:
//   - summary carries FilesToRestore the caller has already
//     confirmed is non-empty.
//
// Returns:
//   - A slice of provider.Message (one per successfully-read file,
//     plus a system anchor message matching Rehydrate's shape).
//
// Side effects:
//   - os.ReadFile per path; slog.Warn on per-file failures.
func (e *Engine) rehydrateBestEffort(sessionID string, summary *ctxstore.CompactionSummary) []provider.Message {
	msgs := make([]provider.Message, 0, 1+len(summary.FilesToRestore))
	msgs = append(msgs, provider.Message{
		Role:    "system",
		Content: "Session rehydrated. Continuing from: " + summary.Intent,
	})
	for _, path := range summary.FilesToRestore {
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("engine rehydration: file unreadable, skipping",
				"session_id", sessionID, "path", path, "error", err)
			continue
		}
		msgs = append(msgs, provider.Message{
			Role:    "tool",
			Content: string(data),
		})
	}
	if len(msgs) == 1 {
		// Only the anchor with no files — no point injecting.
		return nil
	}
	return msgs
}

// insertBeforeUserTurn splices rehydrated messages into msgs just
// before the trailing user turn when one exists; appends to the end
// otherwise. Keeps the injected content in the natural position —
// tool contexts sit ahead of the user's current turn, not after it.
//
// Expected:
//   - msgs is non-nil.
//   - rehydrated is non-empty.
//
// Returns:
//   - A new slice with rehydrated messages inserted.
//
// Side effects:
//   - None. Allocates a new slice.
func insertBeforeUserTurn(msgs, rehydrated []provider.Message) []provider.Message {
	idx := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			idx = i
			break
		}
	}
	out := make([]provider.Message, 0, len(msgs)+len(rehydrated))
	out = append(out, msgs[:idx]...)
	out = append(out, rehydrated...)
	out = append(out, msgs[idx:]...)
	return out
}

// coldRangeHash produces a deterministic SHA-256 of the given message
// slice in a form that distinguishes any semantic change: role,
// content, ModelID, tool-call IDs, and tool-call arguments all
// contribute. Stable across runs (no map iteration, no time values)
// so the H2 memoisation decision is reproducible.
//
// Expected:
//   - recent is the cold-range slice passed to autoCompactor.Compact.
//
// Returns:
//   - A 32-byte hash that changes whenever the slice's observable
//     content changes and matches byte-for-byte on identical inputs.
//
// Side effects:
//   - None. Pure function.
func coldRangeHash(recent []provider.Message) [32]byte {
	h := sha256.New()
	for i := range recent {
		m := &recent[i]
		_, _ = h.Write([]byte(m.Role))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m.Content))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m.ModelID))
		_, _ = h.Write([]byte{0})
		for j := range m.ToolCalls {
			tc := &m.ToolCalls[j]
			_, _ = h.Write([]byte(tc.ID))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(tc.Name))
			_, _ = h.Write([]byte{0})
			// Arguments is a map — iterate the keys sorted so the
			// hash does not flap on map-iteration order. Keys are
			// small strings so the sort cost is negligible.
			if len(tc.Arguments) > 0 {
				argsJSON, err := json.Marshal(tc.Arguments)
				if err == nil {
					_, _ = h.Write(argsJSON)
				}
				_, _ = h.Write([]byte{0})
			}
		}
		_, _ = h.Write([]byte{0, 1}) // message separator
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// autoCompactionThreshold returns the configured auto-compaction ratio
// threshold when every prerequisite for compaction is met. The second
// return value is false when the feature is disabled or a dependency is
// missing, so the caller can short-circuit without inspecting fields
// individually.
//
// Precedence (H3 audit — per-agent override):
//
//   - manifest.ContextManagement.CompactionThreshold when > 0 — the
//     per-agent override configured in the agent manifest.
//   - e.compressionConfig.AutoCompaction.Threshold otherwise — the
//     global configuration fallback.
//
// Callers supplying a manifest with CompactionThreshold == 0 inherit
// the global, which is what tests and agents that have not opted in
// want. A negative manifest value is rejected at manifest load; a
// negative global is rejected at config load; this function trusts
// both invariants and only range-checks the final resolved value as
// defence in depth.
//
// Expected:
//   - manifest is the active agent manifest (non-nil — the caller
//     buildContextWindow always hands in a prepared copy).
//   - tokenBudget is the model context limit passed through from
//     buildContextWindow.
//
// Returns:
//   - (threshold, true) when the feature is enabled, the AutoCompactor
//     is wired, the store and counter are present, tokenBudget is
//     positive, and the resolved threshold is positive.
//   - (0, false) otherwise.
//
// Side effects:
//   - None.
func (e *Engine) autoCompactionThreshold(manifest *agent.Manifest, tokenBudget int) (float64, bool) {
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
	if manifest != nil && manifest.ContextManagement.CompactionThreshold > 0 {
		threshold = manifest.ContextManagement.CompactionThreshold
	}
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
	delta := recentTokens - summaryTokens
	if e.compressionMetrics != nil {
		e.compressionMetrics.AutoCompactionCount++
		if delta > 0 {
			e.compressionMetrics.TokensSaved += delta
		} else if delta < 0 {
			// Item 5 — honest accounting for the cost the layer added.
			e.compressionMetrics.OverheadTokens += -delta
		}
	}
	// Mirror the same deltas onto the per-session ledger so
	// flowstate run --stats reports the CURRENT session's numbers
	// instead of the cumulative aggregate. The aggregate above still
	// grows in lockstep because flowstate serve dashboards depend on
	// it.
	e.recordSessionAutoCompaction(sessionID, delta)
	if e.recorder != nil {
		// M3/Item 5 — mutually exclusive emit paths. Delta > 0 fires
		// the savings counter, delta < 0 fires the overhead counter,
		// and delta == 0 (break-even) fires neither so we do not
		// double-count or produce misleading traffic. The Recorder
		// interface contract also mandates implementations ignore
		// non-positive values, so the guards here are defence in depth.
		switch {
		case delta > 0:
			e.recorder.RecordCompressionTokensSaved(agentID, delta)
		case delta < 0:
			e.recorder.RecordCompressionOverheadTokens(agentID, -delta)
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
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	return e.lastCompactionSummary
}

// CompressionMetrics returns a snapshot of the per-engine compression
// counters (micro/auto counts, tokens saved, overhead tokens). The
// returned value is a copy; callers may retain it without affecting
// live accounting. Item 2 exposes this so `flowstate run --stats` can
// emit a per-turn summary before exit, sidestepping the limitation
// that ephemeral CLI processes do not feed the /metrics endpoint.
//
// Expected:
//   - None; safe to call at any point in the engine lifecycle. Returns
//     a zero-valued struct when compression metrics were not wired
//     (e.g. the CompressionMetrics field was nil in Config).
//
// Returns:
//   - A CompressionMetrics value capturing the current counters. Zero
//     when no compression metrics are attached to the engine.
//
// Side effects:
//   - None.
func (e *Engine) CompressionMetrics() ctxstore.CompressionMetrics {
	if e.compressionMetrics == nil {
		return ctxstore.CompressionMetrics{}
	}
	return *e.compressionMetrics
}

// SessionCompressionMetrics returns a snapshot of the compression
// counters accrued under the supplied sessionID only. Unlike
// CompressionMetrics, which mirrors the engine's cumulative aggregate
// (the right surface for `flowstate serve` dashboards that outlive
// any one session), this accessor partitions the counters so that
// user-facing surfaces — `flowstate run --stats` and the slog
// compression-metrics line — can report per-session figures. Before
// this method existed, a single engine handling successive sessions
// accumulated counters forever; operators reading --stats at the
// start of a new session saw the carried-forward totals from every
// previous session and mistook them for current-session values.
//
// Expected:
//   - sessionID identifies the session of interest. An empty string is
//     treated as a distinct (but usable) bucket rather than rejected,
//     matching the engine's existing tolerance for missing session
//     IDs on the read paths.
//
// Returns:
//   - A CompressionMetrics value capturing only the counters that
//     fired under the supplied sessionID. The zero value is returned
//     for unknown sessions (never-compacted or already-evicted) so
//     first-turn --stats calls see honest zeros.
//
// Side effects:
//   - None. The returned value is a copy; mutating it leaves the
//     engine's live ledger untouched.
func (e *Engine) SessionCompressionMetrics(sessionID string) ctxstore.CompressionMetrics {
	e.sessionCompressionMetricsMu.Lock()
	defer e.sessionCompressionMetricsMu.Unlock()
	entry, ok := e.sessionCompressionMetrics[sessionID]
	if !ok || entry == nil {
		return ctxstore.CompressionMetrics{}
	}
	return *entry
}

// recordSessionAutoCompaction mirrors the compressionMetrics bumps
// publishContextCompactedEvent already applies to the cumulative
// aggregate onto the per-session ledger keyed by sessionID. The
// mapping is intentionally lazy — a session id that never fires
// compaction never allocates an entry — so the map only grows for
// sessions that actually produced work. The C1 eviction hook
// (handleSessionEnded) removes the entry when the session ends, so
// long-running flowstate serve processes do not accumulate dead
// per-session ledgers forever.
//
// Expected:
//   - sessionID is the identifier passed into publishContextCompactedEvent.
//     Empty strings map to the "" bucket deliberately — the caller's
//     choice determines whether that is meaningful.
//   - delta is OriginalTokens - SummaryTokens. Positive values bump
//     TokensSaved; negative values bump OverheadTokens. Zero-deltas
//     still count the compaction call itself (AutoCompactionCount),
//     matching the aggregate accounting contract.
//
// Side effects:
//   - Allocates a CompressionMetrics under the supplied sessionID on
//     first use.
func (e *Engine) recordSessionAutoCompaction(sessionID string, delta int) {
	e.sessionCompressionMetricsMu.Lock()
	defer e.sessionCompressionMetricsMu.Unlock()
	entry, ok := e.sessionCompressionMetrics[sessionID]
	if !ok || entry == nil {
		entry = &ctxstore.CompressionMetrics{}
		e.sessionCompressionMetrics[sessionID] = entry
	}
	entry.AutoCompactionCount++
	if delta > 0 {
		entry.TokensSaved += delta
	} else if delta < 0 {
		entry.OverheadTokens += -delta
	}
}

// recordSessionMicroCompaction mirrors the aggregate
// MicroCompactionCount bump WindowBuilder applies via its attached
// *CompressionMetrics onto the per-session ledger. The delta is the
// number of cold messages HotColdSplitter offloaded on the current
// Build call, captured via BuildResult.MicroCompactedCount and
// forwarded here by buildContextWindow.
//
// Expected:
//   - sessionID identifies the active session.
//   - delta is the non-negative number of cold offloads from the most
//     recent Build call; zero-deltas are skipped so the map does not
//     fill with empty entries for sessions that only saw hot-tail
//     messages.
//
// Side effects:
//   - Allocates a CompressionMetrics under the supplied sessionID on
//     first use.
func (e *Engine) recordSessionMicroCompaction(sessionID string, delta int) {
	if delta <= 0 {
		return
	}
	e.sessionCompressionMetricsMu.Lock()
	defer e.sessionCompressionMetricsMu.Unlock()
	entry, ok := e.sessionCompressionMetrics[sessionID]
	if !ok || entry == nil {
		entry = &ctxstore.CompressionMetrics{}
		e.sessionCompressionMetrics[sessionID] = entry
	}
	entry.MicroCompactionCount += delta
}

// buildResultInputs groups the inputs assembleBuildResult needs so
// the method signature stays inside the project's per-function
// argument limit. A struct here is more honest than a free-for-all
// signature: these fields are all parallel context carried between
// buildContextWindow and the WindowBuilder entry points.
type buildResultInputs struct {
	manifest         *agent.Manifest
	userMessage      string
	tokenBudget      int
	searchResults    []recall.SearchResult
	compactedSummary string
	splitterOpt      ctxstore.BuildOption
}

// assembleBuildResult dispatches to the right WindowBuilder entry
// point given the current mix of semantic search results and
// auto-compaction summary. The SemanticResults and Summary branches
// build without the user message and then append it here so token
// accounting stays in one place; the default branch delegates to
// BuildContextResult which handles the user message itself.
//
// Expected:
//   - in.manifest is a prepared, non-nil manifest with system prompt
//     already populated.
//   - in.userMessage may be empty; an empty string skips the append step.
//   - in.tokenBudget is the full model context limit.
//   - in.searchResults may be empty; non-empty selects the semantic path.
//   - in.compactedSummary may be empty; non-empty selects the summary path.
//   - in.splitterOpt carries the per-Build HotColdSplitter option.
//
// Returns:
//   - The assembled BuildResult with final message slice and token
//     accounting.
//
// Side effects:
//   - None beyond what the selected WindowBuilder entry point performs
//     (logCompressionMetrics, recorder gauge emission).
func (e *Engine) assembleBuildResult(in buildResultInputs) ctxstore.BuildResult {
	switch {
	case len(in.searchResults) > 0:
		result := e.windowBuilder.BuildWithSemanticResults(in.manifest, e.store, in.tokenBudget, in.searchResults, in.splitterOpt)
		return e.appendUserMessageToResult(result, in.userMessage)
	case in.compactedSummary != "":
		result := e.windowBuilder.BuildWithSummary(in.manifest, e.store, in.tokenBudget, in.compactedSummary, in.splitterOpt)
		return e.appendUserMessageToResult(result, in.userMessage)
	default:
		return e.windowBuilder.BuildContextResult(in.manifest, in.userMessage, e.store, in.tokenBudget, in.splitterOpt)
	}
}

// appendUserMessageToResult attaches the user message to a BuildResult
// produced by the SemanticResults or Summary paths, which build
// without the user turn so the search/summary body fills the hot
// portion of the budget first. Token accounting is mirrored so
// BudgetRemaining stays truthful after the append.
//
// Expected:
//   - result is a BuildResult produced by a WindowBuilder entry point
//     that did not include the user message.
//   - userMessage may be empty; empty strings are returned unchanged.
//
// Returns:
//   - The BuildResult with the user message appended (or the
//     unchanged result when userMessage is empty).
//
// Side effects:
//   - None.
func (e *Engine) appendUserMessageToResult(result ctxstore.BuildResult, userMessage string) ctxstore.BuildResult {
	if userMessage == "" {
		return result
	}
	userTokens := e.tokenCounter.Count(userMessage)
	result.Messages = append(result.Messages, provider.Message{
		Role:    "user",
		Content: userMessage,
	})
	result.TokensUsed += userTokens
	result.BudgetRemaining -= userTokens
	return result
}

// snapshotAggregateMicroCount captures the aggregate
// MicroCompactionCount at the start of a Build call so the caller
// can later compute the per-Build delta. Returns zero when no
// CompressionMetrics is wired so the caller does not need to
// nil-check before passing the value into
// attributeMicroCompactionToSession.
//
// Expected:
//   - None.
//
// Returns:
//   - The current aggregate MicroCompactionCount, or zero when no
//     metrics struct is attached.
//
// Side effects:
//   - None.
func (e *Engine) snapshotAggregateMicroCount() int {
	if e.compressionMetrics == nil {
		return 0
	}
	return e.compressionMetrics.MicroCompactionCount
}

// attributeMicroCompactionToSession bridges the aggregate
// MicroCompactionCount bump WindowBuilder applies inside a Build call
// back onto the per-session ledger. The caller captures the aggregate
// counter before the Build and passes it here as microBefore; the
// positive delta over the stored value is the number of cold offloads
// produced under sessionID on this Build. No-op when metrics are not
// wired or when nothing spilled (hot-tail only).
//
// Expected:
//   - sessionID identifies the active session. Empty strings are
//     forwarded to recordSessionMicroCompaction unchanged; the caller
//     owns the policy on empty session IDs.
//   - microBefore is the aggregate MicroCompactionCount captured at
//     the start of the Build call.
//
// Side effects:
//   - May allocate a per-session CompressionMetrics entry via
//     recordSessionMicroCompaction.
func (e *Engine) attributeMicroCompactionToSession(sessionID string, microBefore int) {
	if e.compressionMetrics == nil {
		return
	}
	delta := e.compressionMetrics.MicroCompactionCount - microBefore
	if delta <= 0 {
		return
	}
	e.recordSessionMicroCompaction(sessionID, delta)
}

// logSessionCompressionMetrics emits the per-session companion to the
// WindowBuilder "compression metrics" slog line. The aggregate line
// remains the right signal for flowstate serve dashboards, which
// outlive any one session; this line carries session_id so operators
// running flowstate chat/serve can follow per-session figures without
// parsing the cumulative aggregate. No-op when metrics are not wired.
//
// Expected:
//   - sessionID identifies the session the Build call served.
//
// Side effects:
//   - Writes one slog.Info record at default level when metrics are set.
func (e *Engine) logSessionCompressionMetrics(sessionID string) {
	if e.compressionMetrics == nil {
		return
	}
	sessMetrics := e.SessionCompressionMetrics(sessionID)
	slog.Info("compression metrics session",
		"session_id", sessionID,
		"micro_compaction_count", sessMetrics.MicroCompactionCount,
		"auto_compaction_count", sessMetrics.AutoCompactionCount,
		"tokens_saved", sessMetrics.TokensSaved,
		"compression_overhead_tokens", sessMetrics.OverheadTokens,
	)
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

	assistantMsg := provider.Message{Role: "assistant", Content: content, Thinking: thinking, ModelID: e.LastModel()}
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
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	return e.lastContextResult
}

// ModelContextLimit returns the context window token limit for the configured model.
//
// Returns:
//   - The token limit from the failover manager's first configured
//     preference, or the token counter's resolution of LastModel.
//   - Falls back to the engine's configured systemPromptBudget when no
//     resolver is wired; that field defaults to
//     ctxstore.DefaultModelContextFallback (16K) when cfg.SystemPromptBudget
//     is unset.
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
	return e.resolvedSystemPromptBudget()
}

// ResolveContextLength returns the context window limit for the given provider/model.
// It delegates to the failover manager's resolver if available, or returns the
// engine's configured systemPromptBudget fallback otherwise.
//
// Expected:
//   - providerName and model identify a known provider/model pair.
//
// Returns:
//   - The context length in tokens, or the configured fallback
//     (ctxstore.DefaultModelContextFallback by default) when no failover
//     manager is wired.
//
// Side effects:
//   - None.
func (e *Engine) ResolveContextLength(providerName, model string) int {
	if e.failoverManager != nil {
		return e.failoverManager.ResolveContextLength(providerName, model)
	}
	return e.resolvedSystemPromptBudget()
}

// resolvedSystemPromptBudget returns the engine's configured fallback
// when set, otherwise ctxstore.DefaultModelContextFallback. Centralised
// so ModelContextLimit and ResolveContextLength agree on the same
// default and reading code only has to chase one helper.
//
// Side effects:
//   - None.
func (e *Engine) resolvedSystemPromptBudget() int {
	if e.systemPromptBudget > 0 {
		return e.systemPromptBudget
	}
	return ctxstore.DefaultModelContextFallback
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

// RemoveTool removes the tool with the given name from the engine's tool set.
// Idempotent: returns false without side effects when no tool with that name
// is registered. Callers rely on this to enforce mutual exclusion between
// delegate and suggest_delegate on manifest swap (see
// app.wireDelegateToolIfEnabled and app.wireSuggestDelegateToolIfDisabled).
// Leaving a stale tool in place after a SetManifest swap causes Anthropic to
// reject the request with "400 Bad Request: tools: Tool names must be
// unique" when the stale and newly-added tools share overlapping schemas.
//
// Expected:
//   - name is the tool name to unregister.
//
// Returns:
//   - true if a tool was removed, false if no tool with that name was present.
//
// Side effects:
//   - Mutates the engine's internal tools slice when a match is found.
//   - Invalidates the cached tool schemas when a match is found.
func (e *Engine) RemoveTool(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, t := range e.tools {
		if t.Name() == name {
			e.tools = append(e.tools[:i], e.tools[i+1:]...)
			e.cachedToolSchemas = nil
			return true
		}
	}
	return false
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

// FlushSwarmLifecycle proxies to DelegateTool.FlushSwarmLifecycle on
// the engine's delegate tool. CLI / TUI entry points invoke this after
// the lead's stream completes so swarm-level `when: post` gates fire
// at the spec-correct moment. When no delegate tool is wired (single-
// agent runs, tests with a bare engine) the call is a no-op so callers
// do not have to nil-check the tool surface.
//
// Expected:
//   - ctx is the entry point's outer context (the same one driving the
//     lead's Stream).
//
// Returns:
//   - nil when no delegate tool is wired, no swarm is in flight, or
//     every post-swarm gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each post-swarm gate's runner via the delegate tool.
func (e *Engine) FlushSwarmLifecycle(ctx context.Context) error {
	dt, ok := e.GetDelegateTool()
	if !ok {
		return nil
	}
	return dt.FlushSwarmLifecycle(ctx)
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

// getSuggestDelegateToolLocked returns the SuggestDelegateTool without
// acquiring the lock. Caller must hold e.mu (read or write).
//
// Returns:
//   - The SuggestDelegateTool and true when registered, or nil and false otherwise.
//
// Side effects:
//   - None.
func (e *Engine) getSuggestDelegateToolLocked() (*SuggestDelegateTool, bool) {
	for _, t := range e.tools {
		if st, ok := t.(*SuggestDelegateTool); ok {
			return st, true
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
