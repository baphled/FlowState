package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

var (
	errDelegationNotAllowed     = errors.New("delegation not allowed for this agent")
	errRoutingFieldRequired     = errors.New("category or subagent_type must be provided")
	errMessageMustBeString      = errors.New("message must be a string")
	errCategoryMustBeString     = errors.New("category must be a string")
	errSubagentTypeMustBeString = errors.New("subagent_type must be a string")
	errSessionIDMustBeString    = errors.New("session_id must be a string")
	errChainIDMustBeString      = errors.New("chainID must be a string")
	errLoadSkillsMustBeArray    = errors.New("load_skills must be an array of strings")
	errHandoffMustBeObject      = errors.New("handoff must be an object")
	errBackgroundModeDisabled   = errors.New("background mode disabled: no background manager configured")
	errCircuitBreakerOpen       = errors.New("circuit breaker open: too many delegation failures")
	errDepthLimitExceeded       = errors.New("depth limit exceeded: maximum delegation depth reached")
	errBudgetLimitExceeded      = errors.New("budget limit exceeded: maximum concurrent delegations reached")
	errAgentNotInAllowlist      = errors.New("agent not in delegation allowlist")
	errMaxRejectionsExhausted   = errors.New("max rejections exhausted: plan reviewer rejected too many times")
	errModelNotToolCapable      = errors.New("delegate refused: target model not in tool-capable allowlist")
)

const maxDelegationFailures = 3

// DelegateStoreFactory creates file-backed context stores for delegation sessions.
type DelegateStoreFactory interface {
	// CreateSessionStore creates a file-backed context store for the given session ID.
	CreateSessionStore(sessionID string) (*recall.FileContextStore, error)
}

// streamOutputKeyType identifies the context key used for streaming output.
type streamOutputKeyType struct{}

var streamOutputKey streamOutputKeyType

// WithStreamOutput returns a child context carrying the given output channel
// so that tools (e.g. DelegateTool) can inject chunks into the parent stream.
//
// Expected:
//   - ctx is a valid context to extend.
//   - ch is the stream output channel to attach.
//
// Returns:
//   - A child context containing the output channel.
//
// Side effects:
//   - Stores the output channel in the returned context for later retrieval.
func WithStreamOutput(ctx context.Context, ch chan<- provider.StreamChunk) context.Context {
	return context.WithValue(ctx, streamOutputKey, ch)
}

// streamOutputFromContext extracts the output channel from the context, if present.
//
// Expected:
//   - ctx may carry a stream output channel stored by WithStreamOutput.
//
// Returns:
//   - The output channel and true when present, or a nil channel and false otherwise.
//
// Side effects:
//   - None.
func streamOutputFromContext(ctx context.Context) (chan<- provider.StreamChunk, bool) {
	ch, ok := ctx.Value(streamOutputKey).(chan<- provider.StreamChunk)
	return ch, ok
}

// DelegateTool enables an engine to delegate tasks to other agents.
type DelegateTool struct {
	engines            map[string]*Engine
	delegation         agent.Delegation
	sourceAgentID      string
	backgroundManager  *BackgroundTaskManager
	coordinationStore  coordination.Store
	embeddingDiscovery *discovery.EmbeddingDiscovery
	circuitBreaker     *delegation.CircuitBreaker
	spawnLimits        delegation.SpawnLimits
	skillResolver      SkillResolver
	categoryResolver   *CategoryResolver
	registry           *agent.Registry
	sessionCreator     ChildSessionCreator
	messageAppender    session.MessageAppender
	sessionManager     *session.Manager
	storeFactory       DelegateStoreFactory
	sessionsDir        string // sessionsDir is the directory for session metadata persistence.
	streamers          map[string]streaming.Streamer
	rejectionTracker   *delegation.RejectionTracker
	// toolCapableModels is the allow-list of model-name patterns the
	// resolved sub-agent's model must match before the sub-engine is
	// streamed. Empty / nil means "skip the gate" — preserves the
	// historical, ungated delegation behaviour for callers that have
	// not opted in (e.g. unit tests with no Config wired through).
	toolCapableModels []string
	// toolIncapableModels is the deny-list. Match here always wins, even
	// when the model also matches toolCapableModels.
	toolIncapableModels []string
	// gateRunner is the swarm.GateRunner consulted at every lifecycle
	// boundary (pre-swarm, pre-member, post-member, post-swarm). When
	// nil, the swarm-gate dispatch hooks are no-ops so callers that
	// have not opted into swarm gates keep the historical delegation
	// behaviour. Production wiring (cmd/flowstate / app.New) installs
	// a swarm.MultiRunner pre-registered with builtin:result-schema.
	gateRunner swarm.GateRunner

	// ownerEngine is the engine this DelegateTool is installed on —
	// the LEAD's engine in a swarm dispatch. activeSwarmContext reads
	// the swarm context from here directly because the lead is by
	// design excluded from d.engines (the targets map only carries
	// agents the lead can delegate TO, not the lead itself), so the
	// targets-map lookup always misses for lead-installed tools and
	// gates would never fire without this reference.
	ownerEngine *Engine

	// swarmRegistry is the lookup the dispatch path consults to find
	// the manifest backing the active swarm.Context. Init-time only:
	// set once via WithSwarmRegistry at app boot, never mutated
	// thereafter, so concurrent reads inside Execute / executeSync do
	// not need synchronisation. nil means no swarm wiring — every
	// swarm-aware code path falls through to the historical (pre-A2)
	// behaviour.
	swarmRegistry *swarm.Registry

	// runnerFactory builds a per-swarm-run *swarm.Runner from the
	// active manifest. Init-time only; the closure is captured once
	// at app boot via WithRunnerFactory and returns a fresh Runner
	// for every previously-unseen swarm id. nil means the dispatcher
	// constructs an all-defaults Runner.
	runnerFactory RunnerFactory

	// runnerCache caches one *swarm.Runner per swarm.Context.SwarmID.
	// Caching is the P0.1 fix: a fresh Runner inside the per-call
	// closure means breaker state never accumulates, defeating the
	// addendum-A2/A3 retry-and-breaker promise. Keyed by SwarmID
	// rather than the *Context pointer because a chat session may
	// reinstall the same context value across delegations and
	// breaker state must persist across that boundary.
	runnerCache sync.Map

	// swarmLifecycleMu guards prefiredSwarmIDs against concurrent
	// dispatches when the lead engine fan-outs to multiple members
	// in parallel (Phase 3 territory but the mutex costs nothing
	// today and keeps the once-fire contract honest under any
	// future concurrency).
	swarmLifecycleMu sync.Mutex

	// prefiredSwarmIDs records the swarm ids whose pre-swarm gates
	// have already fired in this DelegateTool's lifetime so the
	// pre-swarm dispatch fires exactly once per swarm run. Keyed by
	// SwarmID rather than the *Context pointer so a SetSwarmContext
	// re-install of the same swarm id (e.g. after a chat-session
	// reset) still suppresses the duplicate.
	prefiredSwarmIDs map[string]bool
}

// delegationTarget carries the resolved agent, engine, and message for delegation.
type delegationTarget struct {
	agentID string
	engine  *Engine
	message string
	handoff *delegation.Handoff
	chainID string
	// chainIDFromCaller distinguishes a planner-supplied chainID from the
	// auto-generated fallback; only the former drives preamble injection.
	chainIDFromCaller bool
	resolvedModel     string
	resolvedProvider  string
	// requestedSession carries the caller-supplied session_id for resumption.
	requestedSession string
	// denyDelegate is true when the target agent lacks the "delegate" tool permission.
	denyDelegate bool
	// denyTodoWrite is true when the target agent lacks the "todowrite" tool permission.
	denyTodoWrite bool
}

// delegationParams groups the parsed delegation input fields.
type delegationParams struct {
	category     string
	subagentType string
	message      string
	loadSkills   []string
	sessionID    string
	chainID      string // chainID is the caller-supplied top-level chainID; empty when omitted.
	handoff      *delegation.Handoff
	runAsync     bool
}

// delegationResult carries the aggregated response and stream metadata from delegation.
type delegationResult struct {
	response  string
	toolCalls int
	lastTool  string
}

// NewDelegateTool creates a new delegation tool for the given engines, delegation configuration,
// and source agent identifier used for event attribution.
//
// Expected:
//   - engines is a map of agent IDs to their Engine instances.
//   - delegation is the delegation configuration for the current agent.
//   - sourceAgentID identifies the agent that owns this tool.
//
// Returns:
//   - A configured DelegateTool instance.
//
// Side effects:
//   - None.
func NewDelegateTool(engines map[string]*Engine, delegationConfig agent.Delegation, sourceAgentID string) *DelegateTool {
	return &DelegateTool{
		engines:       engines,
		delegation:    delegationConfig,
		sourceAgentID: sourceAgentID,
		circuitBreaker: delegation.NewCircuitBreaker(
			maxDelegationFailures,
			delegation.WithFailureWindow(5*time.Minute),
			delegation.WithHalfOpenTimeout(30*time.Second),
		),
		spawnLimits: delegation.DefaultSpawnLimits(),
	}
}

// WithStreamers registers per-agent streamers that override direct engine streaming.
// Agents with HarnessEnabled in their manifest should have a HarnessStreamer here.
//
// Expected:
//   - streamers maps agent IDs to Streamer implementations.
//
// Returns:
//   - The DelegateTool for chaining.
//
// Side effects:
//   - Sets the streamers map on the DelegateTool.
func (d *DelegateTool) WithStreamers(streamers map[string]streaming.Streamer) *DelegateTool {
	d.streamers = streamers
	return d
}

// WithRejectionTracker configures a RejectionTracker that enforces the maximum
// number of plan-reviewer rejections per delegation chain.
//
// Expected:
//   - tracker is a non-nil RejectionTracker backed by the delegation coordination store.
//
// Returns:
//   - The DelegateTool for chaining.
//
// Side effects:
//   - Sets the rejectionTracker field on the DelegateTool.
func (d *DelegateTool) WithRejectionTracker(tracker *delegation.RejectionTracker) *DelegateTool {
	d.rejectionTracker = tracker
	return d
}

// WithToolCapability configures the model-capability allow/deny lists
// consulted in Execute() before the sub-engine is streamed. When both
// slices are empty/nil, the gate is skipped — the same behaviour as
// before this feature, so callsites that pre-date the feature (or do
// not wire Config through) keep working.
//
// Expected:
//   - allow: model-name patterns that signal "tool-capable" (glob `*`
//     suffix supported, e.g. `claude-*`, `qwen3:*`). See
//     config.AppConfig.ToolCapableModels.
//   - deny: patterns that signal "known to silently emit zero tool
//     calls". Always wins over allow.
//
// Returns:
//   - The DelegateTool for chaining.
//
// Side effects:
//   - Replaces any previously configured allow/deny patterns.
func (d *DelegateTool) WithToolCapability(allow, deny []string) *DelegateTool {
	d.toolCapableModels = allow
	d.toolIncapableModels = deny
	return d
}

// WithGateRunner installs the swarm gate dispatcher consulted after a
// post-member delegation completes. The runner is invoked once per
// matching post-member gate on the active swarm context (see
// dispatchPostMemberGates); production wiring installs a
// swarm.MultiRunner with the builtin:result-schema runner registered.
//
// Expected:
//   - runner may be nil to disable swarm-gate dispatch (the historical
//     no-op behaviour for callers that pre-date T-swarm-3).
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously installed gate runner.
func (d *DelegateTool) WithGateRunner(runner swarm.GateRunner) *DelegateTool {
	d.gateRunner = runner
	return d
}

// WithOwnerEngine pins the engine this DelegateTool is installed on.
// activeSwarmContext consults this reference first when looking up
// the active swarm.Context — necessary because the lead's id is
// excluded from d.engines (the targets-only map) by buildDelegateMaps.
// Production wiring (App.configureDelegateTool) calls this with the
// engine that just received eng.AddTool(delegateTool).
//
// Expected:
//   - eng may be nil (the activeSwarmContext fallback to d.engines
//     applies for older test wiring that pre-dates this method).
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously stored owner engine.
func (d *DelegateTool) WithOwnerEngine(eng *Engine) *DelegateTool {
	d.ownerEngine = eng
	return d
}

// GateRunner returns the currently installed swarm gate dispatcher (or
// nil when none has been wired). Exposed so the App-level wiring tests
// can pin "production wiring installs a non-nil runner" without having
// to spin up a full swarm dispatch end-to-end. Production code never
// reads this field directly — the dispatch path consults d.gateRunner
// internally.
//
// Returns:
//   - The installed runner or nil.
//
// Side effects:
//   - None.
func (d *DelegateTool) GateRunner() swarm.GateRunner {
	return d.gateRunner
}

// RunnerFactory builds the *swarm.Runner the dispatch loop installs
// for a given manifest. The runner is constructed once per swarm id
// and cached; the factory is consulted only on the first dispatch
// against a swarm context. A nil manifest signals "no swarm context"
// and the factory MUST return a Runner with default retry/breaker
// values so the no-swarm path still gets retry semantics.
type RunnerFactory func(*swarm.Manifest) *swarm.Runner

// defaultRunnerFactory returns the all-defaults factory used when the
// app forgets to inject one via WithRunnerFactory. Constructs a Runner
// from Manifest.EffectiveRetryPolicy / EffectiveCircuitBreaker (so
// addendum-A2 defaults apply consistently with the manifest helpers)
// or, when manifest is nil, a Runner from zero-value RetryPolicy /
// CircuitBreakerConfig that the swarm package fills in with its own
// defaults.
func defaultRunnerFactory(m *swarm.Manifest) *swarm.Runner {
	if m == nil {
		return swarm.NewRunner(swarm.RetryPolicy{}, swarm.CircuitBreakerConfig{})
	}
	return swarm.NewRunner(m.EffectiveRetryPolicy(), m.EffectiveCircuitBreaker())
}

// WithSwarmRegistry installs the swarm.Registry the dispatch path uses
// to resolve the active swarm.Context's manifest. Production wiring
// passes the same instance the chat-input @<id> resolver consults so
// the engine sees the same source of truth.
//
// Init-time only: set once at app boot, never mutated thereafter.
// Concurrent reads inside Execute / executeSync rely on the contract
// that no caller mutates this field after the first delegation lands.
//
// Expected:
//   - reg may be nil to disable swarm-aware lookup (the historical
//     pre-Task-1 behaviour).
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously installed registry.
func (d *DelegateTool) WithSwarmRegistry(reg *swarm.Registry) *DelegateTool {
	d.swarmRegistry = reg
	return d
}

// WithRunnerFactory installs the closure the dispatcher consults to
// build a *swarm.Runner the first time a given swarm id appears in a
// delegation. Production wiring closes over Manifest helpers so the
// runner picks up retry / breaker defaults from the manifest.
//
// Init-time only: configured once at app boot, never mutated
// thereafter. The cache lookup (runnerCache) reads the factory under
// the same init-time-only contract.
//
// Expected:
//   - f may be nil to fall back to defaultRunnerFactory.
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously installed factory and clears any
//     cached Runners so a fresh factory takes effect on the next
//     dispatch.
func (d *DelegateTool) WithRunnerFactory(f RunnerFactory) *DelegateTool {
	d.runnerFactory = f
	d.runnerCache = sync.Map{}
	return d
}

// runnerForSwarm returns the cached *swarm.Runner for swarmID,
// constructing it via the configured factory on first use. Subsequent
// calls with the same id return the same pointer so retry/breaker
// state accumulates across delegations within the swarm run.
//
// Expected:
//   - swarmID is the active swarm.Context.SwarmID; non-empty.
//   - manifest is the manifest backing swarmID; may be nil when the
//     registry could not resolve it (the factory falls back to
//     defaults).
//
// Returns:
//   - The cached or freshly-built *swarm.Runner.
//
// Side effects:
//   - Inserts a new Runner into runnerCache on first miss.
func (d *DelegateTool) runnerForSwarm(swarmID string, manifest *swarm.Manifest) *swarm.Runner {
	if cached, ok := d.runnerCache.Load(swarmID); ok {
		if runner, isRunner := cached.(*swarm.Runner); isRunner {
			return runner
		}
	}
	factory := d.runnerFactory
	if factory == nil {
		factory = defaultRunnerFactory
	}
	runner := factory(manifest)
	actual, _ := d.runnerCache.LoadOrStore(swarmID, runner)
	if existing, ok := actual.(*swarm.Runner); ok {
		return existing
	}
	return runner
}

// RunnerForSwarmIDForTest exposes the cached Runner for swarmID so
// wiring tests can pin runner-cache identity (P0.1 verification).
// Production code never calls this; it is intentionally a thin
// pointer accessor.
func (d *DelegateTool) RunnerForSwarmIDForTest(swarmID string) *swarm.Runner {
	if cached, ok := d.runnerCache.Load(swarmID); ok {
		if runner, isRunner := cached.(*swarm.Runner); isRunner {
			return runner
		}
	}
	return nil
}

// resolveStreamer returns the Streamer for agentID, falling back to eng when none is registered.
//
// Expected:
//   - agentID is the target agent identifier.
//   - eng is the fallback engine.
//
// Returns:
//   - The Streamer to use for the delegation call.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveStreamer(agentID string, eng *Engine) streaming.Streamer {
	if d.streamers != nil {
		if str, ok := d.streamers[agentID]; ok {
			return str
		}
	}
	return eng
}

// NewDelegateToolWithBackground creates a new delegation tool with background task support.
//
// Expected:
//   - engines is a map of agent IDs to their Engine instances.
//   - delegation is the delegation configuration for the current agent.
//   - sourceAgentID identifies the agent that owns this tool.
//   - backgroundManager is the manager for tracking background tasks.
//   - coordinationStore is the shared store for cross-agent coordination.
//
// Returns:
//   - A configured DelegateTool instance with background support.
//
// Side effects:
//   - None.
func NewDelegateToolWithBackground(
	engines map[string]*Engine,
	delegationConfig agent.Delegation,
	sourceAgentID string,
	backgroundManager *BackgroundTaskManager,
	coordinationStore coordination.Store,
) *DelegateTool {
	return &DelegateTool{
		engines:           engines,
		delegation:        delegationConfig,
		sourceAgentID:     sourceAgentID,
		backgroundManager: backgroundManager,
		coordinationStore: coordinationStore,
		circuitBreaker: delegation.NewCircuitBreaker(
			maxDelegationFailures,
			delegation.WithFailureWindow(5*time.Minute),
			delegation.WithHalfOpenTimeout(30*time.Second),
		),
		spawnLimits:      delegation.DefaultSpawnLimits(),
		rejectionTracker: newRejectionTrackerIfPresent(coordinationStore),
	}
}

// newRejectionTrackerIfPresent returns a RejectionTracker when store is non-nil, otherwise nil.
//
// Expected:
//   - store may be nil.
//
// Returns:
//   - A RejectionTracker backed by store, or nil.
//
// Side effects:
//   - None.
func newRejectionTrackerIfPresent(store coordination.Store) *delegation.RejectionTracker {
	if store == nil {
		return nil
	}
	return delegation.NewRejectionTracker(store, 0)
}

// SetEmbeddingDiscovery sets the embedding-based discovery for agent matching.
//
// Expected:
//   - ed is a non-nil EmbeddingDiscovery instance.
//
// Side effects:
//   - Sets the embeddingDiscovery field for use in target resolution.
func (d *DelegateTool) SetEmbeddingDiscovery(ed *discovery.EmbeddingDiscovery) {
	d.embeddingDiscovery = ed
}

// WithSpawnLimits configures spawn limits for delegation depth and budget enforcement.
//
// Expected:
//   - limits is a valid SpawnLimits configuration.
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Sets the spawnLimits field to enforce during Execute().
func (d *DelegateTool) WithSpawnLimits(limits delegation.SpawnLimits) *DelegateTool {
	d.spawnLimits = limits
	return d
}

// WithSkillResolver sets the skill resolver for injecting skills into child engine system prompts.
//
// Expected:
//   - r is a non-nil SkillResolver instance.
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Sets the skillResolver field for skill injection during delegation.
func (d *DelegateTool) WithSkillResolver(r SkillResolver) *DelegateTool {
	d.skillResolver = r
	return d
}

// WithCategoryResolver sets the CategoryResolver used to map category names to model config.
//
// Expected:
//   - r is a non-nil CategoryResolver.
//
// Returns:
//   - The DelegateTool for method chaining.
//
// Side effects:
//   - Replaces any previously configured category resolver.
func (d *DelegateTool) WithCategoryResolver(r *CategoryResolver) *DelegateTool {
	d.categoryResolver = r
	return d
}

// WithRegistry sets the agent registry used for name and alias resolution.
//
// Expected:
//   - reg is a non-nil agent Registry.
//
// Returns:
//   - The DelegateTool for method chaining.
//
// Side effects:
//   - Replaces any previously configured registry.
func (d *DelegateTool) WithRegistry(reg *agent.Registry) *DelegateTool {
	d.registry = reg
	return d
}

// WithSessionCreator sets the session creator used to register child sessions
// when delegation fires. When set, executeSync calls CreateWithParent using the
// parent session ID extracted from context.
//
// Expected:
//   - c is a valid ChildSessionCreator, or nil to disable child session registration.
//
// Returns:
//   - The DelegateTool for method chaining.
//
// Side effects:
//   - Replaces any previously configured session creator.
func (d *DelegateTool) WithSessionCreator(c ChildSessionCreator) *DelegateTool {
	d.sessionCreator = c
	return d
}

// WithMessageAppender sets the message appender used to accumulate delegation
// stream chunks into the child session's message history.
//
// Expected:
//   - a is a valid session.MessageAppender, or nil to disable message accumulation.
//
// Returns:
//   - The DelegateTool for method chaining.
//
// Side effects:
//   - Replaces any previously configured message appender.
func (d *DelegateTool) WithMessageAppender(a session.MessageAppender) *DelegateTool {
	d.messageAppender = a
	return d
}

// WithSessionManager sets the session manager for registering synthetic sessions.
//
// Expected:
//   - mgr is a valid session.Manager or nil.
//
// Returns:
//   - The DelegateTool instance for chaining.
//
// Side effects:
//   - Sets the sessionManager field.
func (d *DelegateTool) WithSessionManager(mgr *session.Manager) *DelegateTool {
	d.sessionManager = mgr
	return d
}

// WithStoreFactory sets an optional factory for creating file-backed stores
// for delegation sessions. When nil (default), delegation sessions use
// in-memory accumulation only.
//
// Expected:
//   - f is a valid DelegateStoreFactory or nil to disable file persistence.
//
// Returns:
//   - The DelegateTool instance for chaining.
//
// Side effects:
//   - Sets the storeFactory field used during executeSync and executeBackgroundTask.
func (d *DelegateTool) WithStoreFactory(f DelegateStoreFactory) *DelegateTool {
	d.storeFactory = f
	return d
}

// WithSessionsDir sets the directory where session metadata files are persisted.
// When non-empty, createChildSession will write a .meta.json file after creating
// each child session so that sessions survive application restarts.
//
// Expected:
//   - dir is an absolute path to the sessions directory, or empty to disable persistence.
//
// Returns:
//   - The DelegateTool instance for chaining.
//
// Side effects:
//   - Sets the sessionsDir field used by persistSessionMetadata.
func (d *DelegateTool) WithSessionsDir(dir string) *DelegateTool {
	d.sessionsDir = dir
	return d
}

// persistSessionMetadata writes session metadata to disk on a best-effort basis.
// Errors are silently discarded so that persistence failures never block delegation.
//
// Expected:
//   - sess is the child session to persist.
//
// Returns:
//   - None.
//
// Side effects:
//   - Calls session.PersistSession when sessionsDir is non-empty.
func (d *DelegateTool) persistSessionMetadata(sess *session.Session) {
	if d.sessionsDir == "" {
		return
	}
	if err := session.PersistSession(d.sessionsDir, sess); err != nil {
		return
	}
}

// attachSessionStore creates a FileContextStore via the factory (if set) and
// attaches it to the target engine. Returns a cleanup function that flushes
// and closes the store. When no factory is configured, returns a no-op closer.
//
// Expected:
//   - eng is the delegation target engine.
//   - sessionID is the child session identifier.
//
// Returns:
//   - A cleanup function that must be called after the delegation stream is fully consumed.
//
// Side effects:
//   - When factory is set, calls SetContextStore on the engine, closing any previous store first.
func (d *DelegateTool) attachSessionStore(eng *Engine, sessionID string) func() {
	if d.storeFactory == nil {
		return func() {}
	}
	if existing := eng.ContextStore(); existing != nil {
		existing.Close()
	}
	store, err := d.storeFactory.CreateSessionStore(sessionID)
	if err != nil {
		return func() {}
	}
	eng.SetContextStore(store, sessionID)
	return func() {
		store.Close()
	}
}

// ResolveByNameOrAlias returns the agent ID for a given name or alias.
//
// Expected:
//   - name is a non-empty string identifying an agent.
//
// Returns:
//   - The resolved agent ID and nil on success.
//   - Empty string and error if not found.
//
// Side effects:
//   - None.
func (d *DelegateTool) ResolveByNameOrAlias(name string) (string, error) {
	if d.registry == nil {
		return "", fmt.Errorf("no registry configured for agent %q lookup", name)
	}
	manifest, ok := d.registry.GetByNameOrAlias(name)
	if !ok {
		ids := make([]string, 0, len(d.registry.List()))
		for _, m := range d.registry.List() {
			ids = append(ids, m.ID)
		}
		return "", fmt.Errorf("unknown agent %q; available agents: %s", name, strings.Join(ids, ", "))
	}
	return manifest.ID, nil
}

// checkSpawnLimits validates that delegation respects configured depth and budget limits.
//
// Expected:
//   - handoff may be nil or contain depth metadata.
//
// Returns:
//   - An error if depth or budget limits are exceeded, nil otherwise.
//
// Side effects:
//   - None.
func (d *DelegateTool) checkSpawnLimits(handoff *delegation.Handoff) error {
	depth := 0
	if handoff != nil && handoff.Metadata != nil {
		if depthStr, ok := handoff.Metadata["depth"]; ok {
			var depthVal int
			if _, err := fmt.Sscanf(depthStr, "%d", &depthVal); err == nil {
				depth = depthVal
			}
		}
	}

	effectiveLimits := d.spawnLimits
	if maxDepth := d.swarmAwareMaxDepth(); maxDepth > 0 {
		effectiveLimits.MaxDepth = maxDepth
	}

	if effectiveLimits.ExceedsDepth(depth) {
		return errDepthLimitExceeded
	}

	if d.backgroundManager != nil {
		if d.spawnLimits.ExceedsBudget(d.backgroundManager.ActiveCount()) {
			return errBudgetLimitExceeded
		}
	}

	return nil
}

// swarmAwareMaxDepth returns the manifest-resolved depth ceiling for
// the active swarm context, or 0 when no context / registry / manifest
// is in flight (caller falls back to d.spawnLimits.MaxDepth).
//
// Resolution honours addendum A4: an explicit Manifest.MaxDepth wins
// over the per-type default; SwarmType=analysis stays at 8, codegen
// at 16, orchestration at 32.
//
// Expected:
//   - The receiver may have no swarm context wired; returns 0 in that
//     case.
//
// Returns:
//   - The resolved depth ceiling when a swarm context + registry +
//     manifest are in flight.
//   - 0 when any link in that chain is missing.
//
// Side effects:
//   - None.
func (d *DelegateTool) swarmAwareMaxDepth() int {
	swarmCtx, ok := d.activeSwarmContext()
	if !ok {
		return 0
	}
	manifest := d.manifestForSwarm(swarmCtx.SwarmID)
	if manifest == nil {
		return 0
	}
	return manifest.ResolveMaxDepth()
}

// Name returns the tool name.
//
// Returns:
//   - The string "delegate".
//
// Side effects:
//   - None.
func (d *DelegateTool) Name() string {
	return "delegate"
}

// Description returns a human-readable description of the delegation tool.
//
// Returns:
//   - A string describing what the tool does.
//
// Side effects:
//   - None.
func (d *DelegateTool) Description() string {
	return "Delegate a task to another agent based on task type"
}

// Timeout signals that DelegateTool implements tool.TimeoutOverrider
// and returns 0 to opt out of the engine's default per-tool execution
// budget, inheriting the parent context unchanged.
//
// Delegation runs a full multi-turn sub-agent conversation: the child
// engine runs its own streaming LLM loop and dispatches its own tool
// calls, so the shell-tool latency profile the default ~2-minute cap
// assumes does not apply. Parent cancellation still cascades via the
// ctx DelegateTool.Execute forwards to the child engine.
//
// Returns:
//   - 0 — inherit parent context, no engine-injected deadline.
//
// Side effects:
//   - None.
func (d *DelegateTool) Timeout() time.Duration {
	return 0
}

// Schema returns the JSON schema for the delegation tool input.
//
// Returns:
//   - A tool.Schema describing the required subagent_type and message properties,
//   - plus optional run_in_background and handoff properties.
//
// Side effects:
//   - None.
func (d *DelegateTool) Schema() tool.Schema {
	schema := buildDelegateSchema(delegateCategoryOptions())
	applyRegistryEnum(&schema, d.registry)
	return schema
}

// delegateCategoryOptions returns the set of category keys derived from
// DefaultCategoryRouting, used as the schema enum for the "category"
// property.
//
// Expected:
//   - DefaultCategoryRouting returns a non-nil map; the helper is safe
//     against an empty map and returns an empty slice.
//
// Returns:
//   - A slice of category keys in map-iteration order.
//
// Side effects:
//   - None.
func delegateCategoryOptions() []string {
	categories := make([]string, 0, len(DefaultCategoryRouting()))
	for category := range DefaultCategoryRouting() {
		categories = append(categories, category)
	}
	return categories
}

// buildDelegateSchema returns the static delegate-tool schema,
// parameterised only on the category enum. Registry-derived enums are
// applied separately by applyRegistryEnum.
//
// Expected:
//   - categoryOptions is the enum slice for the "category" property; an
//     empty slice produces a schema with an empty enum, which the caller
//     accepts.
//
// Returns:
//   - A tool.Schema describing the delegate tool's input contract.
//
// Side effects:
//   - None.
func buildDelegateSchema(categoryOptions []string) tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"category": {
				Type:        "string",
				Description: "The routing category to use for model selection",
				Enum:        categoryOptions,
			},
			"subagent_type": {
				Type:        "string",
				Description: "The specialised sub-agent type to delegate to",
			},
			"load_skills": {
				Type:        "array",
				Description: "Optional skills to load for the delegated task",
			},
			"session_id": {
				Type:        "string",
				Description: "Optional session identifier for continuation",
			},
			"message": {
				Type:        "string",
				Description: "The message or instruction to send to the target agent",
			},
			"run_in_background": {
				Type:        "boolean",
				Description: "If true, run the delegation asynchronously and return a task ID",
			},
			"handoff": {
				Type:        "object",
				Description: "Optional handoff metadata including ChainID for coordination",
			},
			"chainID": {
				Type: "string",
				Description: "Optional coordination chainID. When set, the delegate tool " +
					"auto-injects a structured preamble into the specialist's user message " +
					"stating `chainID=<value>` plus, for specialists with a well-known " +
					"coordination_store key convention (explorer, librarian, analyst, " +
					"plan-writer, plan-reviewer), the canonical target key " +
					"`<chainID>/<role-convention>`. The caller no longer needs to write the " +
					"chainID into the free-form `message` itself.",
			},
		},
		Required: []string{"subagent_type", "message"},
	}
}

// applyRegistryEnum overrides subagent_type.Enum with the live agent IDs
// when a registry is present and non-empty. Mutates schema in place so
// the caller can keep the literal definition declarative.
//
// Expected:
//   - schema is non-nil and has the "subagent_type" property defined.
//   - registry may be nil, in which case the function is a no-op.
//
// Returns:
//   - Nothing; mutation occurs in place on schema.
//
// Side effects:
//   - Replaces schema.Properties["subagent_type"].Enum with the live
//     agent IDs from the registry when non-empty.
func applyRegistryEnum(schema *tool.Schema, registry *agent.Registry) {
	if registry == nil {
		return
	}
	manifests := registry.List()
	if len(manifests) == 0 {
		return
	}
	agentIDs := make([]string, 0, len(manifests))
	for _, m := range manifests {
		agentIDs = append(agentIDs, m.ID)
	}
	prop := schema.Properties["subagent_type"]
	prop.Enum = agentIDs
	schema.Properties["subagent_type"] = prop
}

// Execute runs the delegation tool by routing the task to the appropriate sub-agent.
// When run_in_background is true and a background manager is configured, the task
// is executed asynchronously and returns a task ID immediately.
//
// Expected:
//   - ctx is a valid context for the delegation operation.
//   - input contains "subagent_type" and "message" string arguments.
//   - Optional "run_in_background" boolean to run asynchronously.
//   - Optional "handoff" object for ChainID and coordination.
//
// Returns:
//   - A tool.Result containing the sub-agent's aggregated response or task ID.
//   - An error if delegation is not allowed, arguments are invalid, or streaming fails.
//
// Side effects:
//   - Streams a request to the target agent's engine.
//   - Emits DelegationInfo stream chunks when an output channel is available in ctx.
func (d *DelegateTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	params, target, err := d.prepareExecution(ctx, input)
	if err != nil {
		return tool.Result{}, err
	}

	outChan, hasOutput := streamOutputFromContext(ctx)
	chainID := target.chainID
	if chainID == "" {
		chainID = newDelegationChainID()
	}

	// Auto-inject the chainID preamble (and role-specific coord_store
	// key) only when the caller actually supplied a chainID — the
	// auto-generated fallback stays internal so backwards-compatible
	// call sites see no preamble. Propagate the authoritative chainID
	// onto the handoff so downstream DelegationInfo events and the
	// RejectionTracker observe the planner-allocated namespace, not
	// the fallback.
	if target.chainIDFromCaller {
		target.message = autoInjectChainIDPreamble(target.message, target.agentID, chainID)
		if target.handoff == nil {
			target.handoff = &delegation.Handoff{}
		}
		target.handoff.ChainID = chainID
	}

	injectVisitedAgents(&target, d.sourceAgentID)

	baseInfo := d.buildDelegationInfo(target, chainID)

	if params.runAsync {
		return d.executeAsync(ctx, target, baseInfo, outChan, hasOutput)
	}

	return d.executeSync(ctx, target, baseInfo, outChan, hasOutput)
}

// prepareExecution runs the gating checks (circuit, permission, parse,
// spawn limit, target resolution, rejection limit) and returns the
// parsed params plus resolved target. Splits Execute's pre-flight from
// its dispatch logic.
//
// Expected:
//   - ctx is a valid context for target resolution.
//   - input contains the delegate-tool arguments to parse.
//   - The DelegateTool has a configured circuit breaker, registry, and
//     rejection tracker.
//
// Returns:
//   - The parsed delegationParams and resolved delegationTarget on success.
//   - errCircuitBreakerOpen, errDelegationNotAllowed, a parse error, a
//     spawn-limit error, a target-resolution error, or a rejection-limit
//     error on failure.
//
// Side effects:
//   - Consults the circuit breaker (no state mutation on Allow check).
//   - Consults the rejection tracker.
//   - Resolves the target agent and engine via the registry/engine factory.
func (d *DelegateTool) prepareExecution(
	ctx context.Context, input tool.Input,
) (delegationParams, delegationTarget, error) {
	if !d.circuitBreaker.Allow() {
		return delegationParams{}, delegationTarget{}, errCircuitBreakerOpen
	}
	if !d.delegation.CanDelegate {
		return delegationParams{}, delegationTarget{}, errDelegationNotAllowed
	}

	params, err := d.parseDelegationParams(input)
	if err != nil {
		return delegationParams{}, delegationTarget{}, err
	}
	if err := d.checkSpawnLimits(params.handoff); err != nil {
		return delegationParams{}, delegationTarget{}, err
	}

	target, err := d.resolveTargetWithOptions(ctx, params)
	if err != nil {
		return delegationParams{}, delegationTarget{}, err
	}
	if capErr := d.checkTargetToolCapability(target); capErr != nil {
		return delegationParams{}, delegationTarget{}, capErr
	}
	if rejErr := d.checkRejectionLimit(ctx, target.chainID); rejErr != nil {
		return delegationParams{}, delegationTarget{}, rejErr
	}
	return params, target, nil
}

// buildDelegationInfo assembles the provider.DelegationInfo emitted on
// stream chunks, applying any caller-supplied overrides for model and
// provider name.
//
// Expected:
//   - target carries the resolved engine plus optional model/provider
//     overrides supplied by the caller.
//   - chainID is the coordination chain identifier (may be empty).
//
// Returns:
//   - A provider.DelegationInfo populated with agent, model, provider,
//     and chain identifiers for the active delegation.
//
// Side effects:
//   - None.
func (d *DelegateTool) buildDelegationInfo(target delegationTarget, chainID string) provider.DelegationInfo {
	modelName := target.engine.LastModel()
	providerName := target.engine.LastProvider()
	if target.resolvedModel != "" {
		modelName = target.resolvedModel
	}
	if target.resolvedProvider != "" {
		providerName = target.resolvedProvider
	}
	return provider.DelegationInfo{
		SourceAgent:  d.sourceAgentID,
		TargetAgent:  target.agentID,
		ChainID:      chainID,
		ModelName:    modelName,
		ProviderName: providerName,
		Description:  target.message,
		StartedAt:    ptrTime(time.Now().UTC()),
	}
}

// parseDelegationParams extracts delegation arguments into a typed parameter set.
//
// Expected:
//   - input contains delegation arguments accepted by the schema.
//
// Returns:
//   - Parsed delegation parameters.
//   - An error if the arguments are invalid.
//
// Side effects:
//   - None.
func (d *DelegateTool) parseDelegationParams(input tool.Input) (delegationParams, error) {
	params := delegationParams{}
	if err := populateDelegationRouting(&params, input.Arguments); err != nil {
		return delegationParams{}, err
	}
	if err := populateDelegationMetadata(&params, input.Arguments, d); err != nil {
		return delegationParams{}, err
	}

	return params, nil
}

// populateDelegationRouting copies routing fields from raw arguments into params.
//
// Expected:
//   - params is a non-nil destination.
//   - arguments contains delegation routing fields.
//
// Returns:
//   - An error if a routing value has the wrong type.
//
// Side effects:
//   - Writes parsed values into params.
func populateDelegationRouting(params *delegationParams, arguments map[string]interface{}) error {
	if raw, ok := arguments["category"]; ok && raw != nil {
		category, ok := raw.(string)
		if !ok {
			return errCategoryMustBeString
		}
		params.category = category
	}
	if raw, ok := arguments["subagent_type"]; ok && raw != nil {
		subagentType, ok := raw.(string)
		if !ok {
			return errSubagentTypeMustBeString
		}
		params.subagentType = subagentType
	}
	if params.category == "" && params.subagentType == "" {
		return errRoutingFieldRequired
	}
	return nil
}

// populateDelegationMetadata copies metadata fields from raw arguments into params.
//
// Expected:
//   - params is a non-nil destination.
//   - arguments contains delegation metadata fields.
//   - d is the delegate tool used to parse nested handoff data.
//
// Returns:
//   - An error if a metadata value has the wrong type or nested parsing fails.
//
// Side effects:
//   - Writes parsed values into params.
func populateDelegationMetadata(params *delegationParams, arguments map[string]interface{}, d *DelegateTool) error {
	message, ok := arguments["message"].(string)
	if !ok {
		return errMessageMustBeString
	}
	params.message = sanitiseDelegationMessage(message)

	if value, ok := arguments["run_in_background"].(bool); ok {
		params.runAsync = value
	}

	if raw, ok := arguments["handoff"]; ok && raw != nil {
		h, err := d.parseHandoff(raw)
		if err != nil {
			return fmt.Errorf("parsing handoff: %w", err)
		}
		params.handoff = h
	}

	if raw, ok := arguments["load_skills"]; ok && raw != nil {
		loadSkills, err := parseLoadSkills(raw)
		if err != nil {
			return err
		}
		params.loadSkills = loadSkills
	}

	if raw, ok := arguments["session_id"]; ok && raw != nil {
		sessionID, ok := raw.(string)
		if !ok {
			return errSessionIDMustBeString
		}
		params.sessionID = sessionID
	}

	if raw, ok := arguments["chainID"]; ok && raw != nil {
		chainID, ok := raw.(string)
		if !ok {
			return errChainIDMustBeString
		}
		params.chainID = chainID
	}

	return nil
}

// sanitiseDelegationMessage cleans a delegation message to prevent
// prompt injection and context flooding.
//
// Expected:
//   - msg is the raw message string from the LLM's tool call.
//
// Returns:
//   - The sanitised message string.
//
// Side effects:
//   - None.
func sanitiseDelegationMessage(msg string) string {
	const maxMessageLen = 10000
	if len(msg) > maxMessageLen {
		msg = msg[:maxMessageLen]
	}
	// Strip control characters except newline (\n), tab (\t), carriage return (\r)
	var b strings.Builder
	b.Grow(len(msg))
	for _, r := range msg {
		if r == '\n' || r == '\t' || r == '\r' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applySkillsAndSessionMode injects requested skills into the target engine's
// system prompt and configures agent-file loading based on session presence.
//
// Expected:
//   - targetEngine is a non-nil Engine.
//   - params contains the delegation parameters.
//
// Side effects:
//   - Mutates targetEngine's manifest and agent-file loading flag.
func (d *DelegateTool) applySkillsAndSessionMode(targetEngine *Engine, params delegationParams) {
	if len(params.loadSkills) > 0 {
		manifest := targetEngine.Manifest()
		basePrompt := manifest.Instructions.SystemPrompt
		injectedPrompt := d.InjectSkillsIfProvided(params.loadSkills, basePrompt)
		manifest.Instructions.SystemPrompt = injectedPrompt
		targetEngine.SetManifest(manifest)
	}
	targetEngine.SetSkipAgentFiles(params.sessionID == "")
}

// checkDelegationCycle returns an error when the target agent has already
// been visited in this delegation chain or when the source and target are
// the same agent.
//
// Expected:
//   - sourceAgentID is the current agent's ID.
//   - targetAgentID is the intended delegation target.
//   - handoff may be nil.
//
// Returns:
//   - An error if a cycle or self-delegation is detected, nil otherwise.
//
// Side effects:
//   - None.
func checkDelegationCycle(sourceAgentID, targetAgentID string, handoff *delegation.Handoff) error {
	if targetAgentID == sourceAgentID {
		return fmt.Errorf("self-delegation not allowed: agent %q cannot delegate to itself", targetAgentID)
	}
	if handoff != nil && handoff.Metadata != nil {
		if visited, ok := handoff.Metadata["visited_agents"]; ok {
			for _, id := range strings.Split(visited, ",") {
				if strings.TrimSpace(id) == targetAgentID {
					return fmt.Errorf("delegation cycle detected: agent %q already visited in chain [%s]", targetAgentID, visited)
				}
			}
		}
	}
	return nil
}

// injectVisitedAgents records the source agent in the handoff metadata so
// downstream delegations can detect cycles via the visited_agents key.
//
// Expected:
//   - target is a non-nil delegationTarget pointer.
//   - sourceAgentID is the current agent's ID.
//
// Side effects:
//   - Mutates target.handoff.Metadata["visited_agents"].
func injectVisitedAgents(target *delegationTarget, sourceAgentID string) {
	if target.handoff == nil {
		target.handoff = &delegation.Handoff{Metadata: make(map[string]string)}
	}
	if target.handoff.Metadata == nil {
		target.handoff.Metadata = make(map[string]string)
	}
	visited := target.handoff.Metadata["visited_agents"]
	if visited == "" {
		visited = sourceAgentID
	} else {
		visited = visited + "," + sourceAgentID
	}
	target.handoff.Metadata["visited_agents"] = visited
}

// parseLoadSkills converts a raw load_skills argument into a slice of skill names.
//
// Expected:
//   - value is either a JSON array decoded into []interface{}, or a string
//     containing a JSON-encoded array. The string form is accepted because
//     some OpenAI-compat models (e.g. GLM-4.5/4.6) serialise array arguments
//     as JSON strings instead of native JSON arrays.
//
// Returns:
//   - A slice of skill names.
//   - An error if the value cannot be interpreted as an array of strings.
//
// Side effects:
//   - None.
func parseLoadSkills(value interface{}) ([]string, error) {
	// Fast path: provider decoded the array correctly.
	if items, ok := value.([]interface{}); ok {
		loadSkills := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, errLoadSkillsMustBeArray
			}
			loadSkills = append(loadSkills, s)
		}
		return loadSkills, nil
	}

	// Lenient path: model passed the array as a JSON string (e.g. "[]" or
	// "[\"skill-a\",\"skill-b\"]"). Try to decode it.
	if s, ok := value.(string); ok {
		var items []string
		if err := json.Unmarshal([]byte(s), &items); err == nil {
			return items, nil
		}
		// String present but not valid JSON array — fall through to error.
	}

	return nil, errLoadSkillsMustBeArray
}

// teeToParentStream buffers the full content of each member's stream and
// forwards it to the parent outChan as a single coherent chunk once the
// member's stream closes. Forwarding chunk-by-chunk produces interleaved,
// unreadable output when multiple members run in parallel.
//
// The emitted chunk is prefixed with a bold agent label so the reader can
// tell which member produced each block. Done and DelegationInfo-only chunks
// are never forwarded: Done would prematurely close the parent stream and
// DelegationInfo events are emitted separately by executeSync.
//
// The send to parentOut is non-blocking so a full or absent parent channel
// never stalls the member's own stream pipeline.
func teeToParentStream(ctx context.Context, agentID string, src <-chan provider.StreamChunk) <-chan provider.StreamChunk {
	parentOut, ok := streamOutputFromContext(ctx)
	if !ok {
		return src
	}
	out := make(chan provider.StreamChunk, cap(src)+1)
	go func() {
		defer close(out)
		var buf strings.Builder
		for chunk := range src {
			if chunk.Content != "" && !chunk.Done && chunk.DelegationInfo == nil {
				buf.WriteString(chunk.Content)
			}
			out <- chunk
		}
		if buf.Len() > 0 {
			text := "\n\n**[" + agentID + "]**\n\n" + buf.String() + "\n"
			select {
			case parentOut <- provider.StreamChunk{Content: text}:
			default:
			}
		}
	}()
	return out
}

// wrapWithAccumulator wraps the raw chunk stream through session.AccumulateStream
// when a messageAppender is configured, storing accumulated messages into the
// child session identified by sessionID.
//
// Expected:
//   - ctx bounds the accumulator goroutine so a cancelled delegation turn
//     drops promptly instead of parking on rawCh. Passed through from the
//     delegation call site's own context.
//   - rawCh is the stream from target.engine.Stream.
//   - sessionID is the child session identifier returned by createChildSession.
//   - agentID identifies the delegated agent.
//
// Returns:
//   - The (possibly wrapped) chunk channel.
//
// Side effects:
//   - When messageAppender is set, spawns a goroutine that calls AppendMessage.
func (d *DelegateTool) wrapWithAccumulator(
	ctx context.Context,
	rawCh <-chan provider.StreamChunk,
	sessionID, agentID string,
) <-chan provider.StreamChunk {
	if d.messageAppender == nil {
		return rawCh
	}
	return session.AccumulateStream(ctx, d.messageAppender, sessionID, agentID, rawCh)
}

// withHarnessEvents wires harness lifecycle events into outChan for harness-enabled targets.
// When an explicit Streamer is registered for the target, it tees EventType chunks from src
// to outChan (the registered Streamer emits its own harness events already).
// When no explicit Streamer is registered and the target has HarnessEnabled, it injects a
// synthetic harness_attempt_start event before forwarding chunks.
// When HarnessEnabled is false or hasOutput is false it returns src unchanged.
//
// Expected:
//   - src is the stream from resolveStreamer.
//   - outChan and hasOutput control event delivery to the parent stream.
//   - target provides the manifest HarnessEnabled flag and agentID.
//
// Returns:
//   - A channel carrying all chunks from src for use by wrapWithAccumulator.
//
// Side effects:
//   - Spawns a goroutine that closes the returned channel on completion.
func (d *DelegateTool) withHarnessEvents(
	ctx context.Context,
	target delegationTarget,
	src <-chan provider.StreamChunk,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) <-chan provider.StreamChunk {
	if !hasOutput || !target.engine.Manifest().HarnessEnabled {
		return src
	}
	out := make(chan provider.StreamChunk, 64)
	go d.runHarnessEventLoop(ctx, target.agentID, src, outChan, out)
	return out
}

// runHarnessEventLoop is the goroutine body for withHarnessEvents.
// It emits a harness_attempt_start chunk when no explicit Streamer is registered,
// then forwards all chunks from src to out whilst tee-ing EventType chunks to outChan.
//
// Expected:
//   - agentID is used to look up any registered explicit Streamer.
//   - src, outChan, and out are non-nil, live channels.
//
// Returns:
//   - Nothing; closes out on completion.
//
// Side effects:
//   - Closes out.
func (d *DelegateTool) runHarnessEventLoop(
	ctx context.Context,
	agentID string,
	src <-chan provider.StreamChunk,
	outChan chan<- provider.StreamChunk,
	out chan<- provider.StreamChunk,
) {
	defer close(out)
	explicit := d.streamers != nil && d.streamers[agentID] != nil
	if !explicit {
		select {
		case outChan <- provider.StreamChunk{EventType: "harness_attempt_start"}:
		case <-ctx.Done():
			return
		}
	}
	for chunk := range src {
		if explicit && chunk.EventType != "" {
			select {
			case outChan <- chunk:
			case <-ctx.Done():
				return
			}
		}
		select {
		case out <- chunk:
		case <-ctx.Done():
			return
		}
	}
}

// createChildSession registers a child session for the delegation and returns its ID.
// When a sessionCreator is configured and a parent session ID is present in ctx,
// it calls CreateWithParent to produce a traceable child session.
// On any error (including parent not found) it falls back to a synthetic ID.
//
// Expected:
//   - ctx may carry a parent session ID via session.IDKey{}.
//   - agentID identifies the delegated agent.
//
// Returns:
//   - The child session ID to use as the delegation context value.
//
// Side effects:
//   - May call sessionCreator.CreateWithParent, storing a new session in memory.
func (d *DelegateTool) createChildSession(ctx context.Context, agentID string) string {
	parentID := sessionIDFromContext(ctx)
	if d.sessionCreator != nil && parentID != "" {
		if child, err := d.sessionCreator.CreateWithParent(parentID, agentID); err == nil {
			d.persistSessionMetadata(child)
			return child.ID
		}
	}
	if d.sessionManager != nil && parentID != "" {
		if child, err := d.sessionManager.CreateWithParent(parentID, agentID); err == nil {
			d.persistSessionMetadata(child)
			return child.ID
		}
	}
	syntheticID := fmt.Sprintf("delegate-%s-%d", agentID, time.Now().UTC().UnixNano())
	if d.sessionManager != nil {
		d.sessionManager.RegisterSession(syntheticID, agentID)
	}
	return syntheticID
}

// resolveOrCreateSession returns an existing session when sessionID is found in the manager,
// or creates a new child session when not found or sessionID is empty.
//
// Expected:
//   - ctx may carry a parent session ID for new child session creation.
//   - agentID identifies the agent for the delegation.
//   - sessionID is the optional caller-supplied session to resume; empty means create new.
//
// Returns:
//   - The session ID to use for the delegation context.
//
// Side effects:
//   - May call sessionManager.GetSession or createChildSession.
func (d *DelegateTool) resolveOrCreateSession(ctx context.Context, agentID, sessionID string) string {
	if sessionID != "" && d.sessionManager != nil {
		if sess, err := d.sessionManager.GetSession(sessionID); err == nil {
			return sess.ID
		}
	}
	return d.createChildSession(ctx, agentID)
}

// agentHasToolPermission reports whether the named agent is permitted to use toolName.
// When no registry is configured, all tools are permitted (the runtime has
// no manifest to consult, so the only safe default is permissive). When the
// agent ID is not found in the registry, all tools are also permitted —
// the lookup miss means we have no manifest to gate against, which is a
// configuration issue rather than a permission decision.
// When the agent's tool list is empty, NO tools are permitted (fail-closed).
// Legacy manifests without an explicit tools list previously inherited the
// full toolbelt; that quietly defeated the orchestrator-strictness guarantee
// and is now treated as the operator forgetting to declare capabilities.
//
// Expected:
//   - agentID identifies the agent to inspect.
//   - toolName is the name of the tool to check permission for.
//
// Returns:
//   - true when the agent may use the tool or when permissive defaults apply.
//
// Side effects:
//   - None.
func (d *DelegateTool) agentHasToolPermission(agentID, toolName string) bool {
	if d.registry == nil {
		return true
	}
	manifest, ok := d.registry.Get(agentID)
	if !ok {
		return true
	}
	if len(manifest.Capabilities.Tools) == 0 {
		return false
	}
	for _, t := range manifest.Capabilities.Tools {
		if t == toolName {
			return true
		}
	}
	return false
}

// AgentHasToolPermission is the exported form of agentHasToolPermission for testing.
//
// Expected:
//   - agentID identifies the agent to inspect.
//   - toolName is the name of the tool to check permission for.
//
// Returns:
//   - true when the agent may use the tool or when permissive defaults apply.
//
// Side effects:
//   - None.
func (d *DelegateTool) AgentHasToolPermission(agentID, toolName string) bool {
	return d.agentHasToolPermission(agentID, toolName)
}

// formatDelegationOutput wraps the delegated agent's aggregated
// response in a `<task_result>` block. The block is the LLM-visible
// signal that this content is the sub-agent's response (not a
// continuation of the lead's own thinking) so callers downstream of
// the model — log filters, transcript renderers — can scan for the
// boundary tags without false positives.
//
// Notably absent: the historical "task_id: <sessionID> (for resuming
// to continue this task if needed)" header. That header was
// LLM-misleading: synchronous delegations returned a *child session*
// id under the same `task_id:` label that asynchronous delegations
// used for *background-task* ids. The lead would see the header,
// reflexively call `background_output` against the value, and get
// "task not found" because the sync session id was never registered
// with the BackgroundTaskManager. The session id is still surfaced
// to engine consumers via the tool result's Metadata["sessionId"]
// field — the model just never sees it.
//
// Expected:
//   - text is the aggregated response from the delegated agent.
//
// Returns:
//   - A formatted string with the response wrapped in a task_result
//     block.
//
// Side effects:
//   - None.
func formatDelegationOutput(text string) string {
	return fmt.Sprintf("<task_result>\n%s\n</task_result>", text)
}

// FormatDelegationOutput is the exported form of formatDelegationOutput for testing.
//
// Expected:
//   - text is the aggregated response from the delegated agent.
//
// Returns:
//   - A formatted string with the response wrapped in a task_result block.
//
// Side effects:
//   - None.
func FormatDelegationOutput(text string) string {
	return formatDelegationOutput(text)
}

// executeSync runs delegation synchronously, blocking until complete.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result with the delegation result.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events.
func (d *DelegateTool) executeSync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")
	if hasOutput {
		label := "\n*Delegating to **" + target.agentID + "**…*\n"
		select {
		case outChan <- provider.StreamChunk{Content: label}:
		default:
		}
	}

	if gateErr := d.dispatchPreSwarmGatesOnce(ctx); gateErr != nil {
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, gateErr
	}
	if gateErr := d.dispatchPreMemberGates(ctx, target.agentID); gateErr != nil {
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, gateErr
	}

	delegateSessionID := d.resolveOrCreateSession(ctx, target.agentID, target.requestedSession)
	closeStore := d.attachSessionStore(target.engine, delegateSessionID)
	defer closeStore()
	delegateCtx := context.WithValue(ctx, session.IDKey{}, delegateSessionID)

	var result delegationResult
	dispatchErr := d.runStreamThroughRunner(delegateCtx, target, &result)
	if dispatchErr != nil {
		completedAt := time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, dispatchErr
	}

	completedAt := time.Now().UTC()
	modelName := target.engine.LastModel()
	providerName := target.engine.LastProvider()
	baseInfo.ModelName = modelName
	baseInfo.ProviderName = providerName
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")
	d.closeSessionIfManaged(delegateSessionID)

	if gateErr := d.dispatchPostMemberGates(ctx, target.agentID); gateErr != nil {
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, gateErr
	}

	return tool.Result{
		Output: formatDelegationOutput(result.response),
		Title:  target.message,
		Metadata: map[string]interface{}{
			"sessionId": delegateSessionID,
			"model":     modelName,
			"provider":  providerName,
		},
	}, nil
}

// runStreamThroughRunner dispatches the target's Stream + collect
// pipeline through the per-swarm-context Runner when one is in flight,
// or through the historical CircuitBreaker on the no-swarm path.
//
// On the swarm path the closure is the work the runner re-invokes per
// retry attempt: a fresh Stream call followed by collectWithProgress.
// CategorisedErrors flow through unchanged so the runner sees the
// retry/terminal verdict the source layer attached. Plain errors get
// CategoryUnknown which the runner treats as terminal — this is the
// addendum-A3 contract that uncategorised errors NEVER silently
// retry.
//
// On the no-swarm path the historical delegation.CircuitBreaker still
// records failures and successes; the swarm.Runner is OUT of the
// dispatch chain (per the multi-expert review's OQ.3 resolution).
//
// Expected:
//   - delegateCtx is the per-call context with the delegate session id.
//   - target is the resolved delegation target.
//   - result is an out-parameter the caller reads after success or
//     failure — toolCalls / lastTool flow through it for emit metadata.
//
// Returns:
//   - nil on success; a wrapped error otherwise.
//
// Side effects:
//   - Calls Stream on the resolved streamer and drains its chunks.
//   - Mutates the historical breaker on the no-swarm path only.
//   - Caches a Runner per active swarm id on first use.
func (d *DelegateTool) runStreamThroughRunner(delegateCtx context.Context, target delegationTarget, result *delegationResult) error {
	swarmCtx, hasSwarm := d.activeSwarmContext()
	if !hasSwarm {
		return d.runStreamWithLegacyBreaker(delegateCtx, target, result)
	}

	runner := d.runnerForSwarm(swarmCtx.SwarmID, d.manifestForSwarm(swarmCtx.SwarmID))
	dispatchErr := runner.Dispatch(delegateCtx, target.agentID, func(ctx context.Context, _ string) error {
		return d.streamAndCollect(ctx, target, result)
	})
	return dispatchErr
}

// runStreamWithLegacyBreaker preserves the historical no-swarm-context
// dispatch semantics: the legacy delegation.CircuitBreaker tracks
// process-wide failure counts and the error wrapper matches the
// pre-Task-1 surface so existing tests (and any caller that does not
// install a swarm context) behave identically.
//
// Expected:
//   - delegateCtx is the per-call context.
//   - target is the resolved delegation target.
//   - result is the out-parameter populated by collectWithProgress.
//
// Returns:
//   - nil on success.
//   - "delegation failed: %w" on Stream error; the underlying error
//     from collectWithProgress otherwise.
//
// Side effects:
//   - Calls RecordSuccess / RecordFailure on the historical breaker.
func (d *DelegateTool) runStreamWithLegacyBreaker(delegateCtx context.Context, target delegationTarget, result *delegationResult) error {
	chunks, err := d.resolveStreamer(target.agentID, target.engine).Stream(delegateCtx, target.agentID, target.message)
	if err != nil {
		d.circuitBreaker.RecordFailure()
		return fmt.Errorf("delegation failed: %w", err)
	}
	chunks = teeToParentStream(delegateCtx, target.agentID, chunks)
	chunks = d.withHarnessEvents(delegateCtx, target, chunks, nil, false)
	chunks = d.wrapWithAccumulator(delegateCtx, chunks, sessionIDFromContext(delegateCtx), target.agentID)
	res, collectErr := d.collectWithProgress(delegateCtx, chunks, time.Now())
	*result = res
	if collectErr != nil {
		d.circuitBreaker.RecordFailure()
		return collectErr
	}
	d.circuitBreaker.RecordSuccess()
	return nil
}

// streamAndCollect is the per-attempt closure body the swarm Runner
// re-invokes under retry. A streamer panic surfaces as a
// CategoryTerminal CategorisedError so the runner halts at attempt 1
// (P1.3); a Stream-error and a collectWithProgress-error pass through
// unchanged so any caller-categorisation reaches the runner intact.
//
// Expected:
//   - ctx is the runner's per-attempt context.
//   - target is the resolved delegation target.
//   - result is the out-parameter populated on success or partial
//     completion (so tool-call counts surface even on failure).
//
// Returns:
//   - nil when the stream drained cleanly.
//   - A categorised error wrapping the underlying cause otherwise.
//
// Side effects:
//   - Calls Stream on the resolved streamer and drains its chunks.
func (d *DelegateTool) streamAndCollect(ctx context.Context, target delegationTarget, result *delegationResult) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = &swarm.CategorisedError{
				Category: swarm.CategoryTerminal,
				MemberID: target.agentID,
				Cause:    fmt.Errorf("streamer panic: %v", r),
			}
		}
	}()

	chunks, err := d.resolveStreamer(target.agentID, target.engine).Stream(ctx, target.agentID, target.message)
	if err != nil {
		return err
	}
	chunks = teeToParentStream(ctx, target.agentID, chunks)
	chunks = d.withHarnessEvents(ctx, target, chunks, nil, false)
	chunks = d.wrapWithAccumulator(ctx, chunks, sessionIDFromContext(ctx), target.agentID)
	res, collectErr := d.collectWithProgress(ctx, chunks, time.Now())
	*result = res
	return collectErr
}

// manifestForSwarm returns the swarm.Manifest backing swarmID via the
// installed registry, or nil when no registry is wired or no manifest
// is registered. Pulled into a helper so the runner-cache lookup can
// stay focused on the cache contract.
func (d *DelegateTool) manifestForSwarm(swarmID string) *swarm.Manifest {
	if d.swarmRegistry == nil {
		return nil
	}
	m, ok := d.swarmRegistry.Get(swarmID)
	if !ok {
		return nil
	}
	return m
}

// DispatchSwarmMembers fans out the swarm-context members through
// swarm.DispatchMembers honouring the manifest's Harness.Parallel +
// Harness.MaxParallel. Each member's per-iteration work is the same
// per-attempt closure executeSync drives for a single delegation:
// resolveStreamer + Stream + collectWithProgress wrapped in the
// per-swarm Runner cached by SwarmID.
//
// The PostMember hook fires post-member gates on the worker goroutine
// before the semaphore slot releases, so peers still in flight do not
// proceed past a failed validation. This is the §T37 contract baked
// into DispatchOptions.PostMember.
//
// MaxParallel is clamped against d.spawnLimits.MaxTotalBudget so a
// manifest cannot fan out beyond the engine-level concurrency
// ceiling. Recursion into sub-swarms (Task 5) shares the same ceiling
// to bound active fan-out across all depths (P0.6).
//
// Expected:
//   - ctx is the lead-side delegation context.
//   - swarmCtx is the active swarm context; non-nil.
//   - members is the roster to fan out across; empty yields nil.
//   - message is the prompt forwarded to every member.
//
// Returns:
//   - nil when every member completes and every post-member gate
//     passes.
//   - The first error otherwise.
//
// Side effects:
//   - Streams per-member work through the resolved streamers.
//   - Fires the PostMember hook for each member as it lands.
func (d *DelegateTool) DispatchSwarmMembers(ctx context.Context, swarmCtx *swarm.Context, members []string, message string) error {
	if swarmCtx == nil || len(members) == 0 {
		return nil
	}
	manifest := d.manifestForSwarm(swarmCtx.SwarmID)
	parallel := false
	maxParallel := 0
	if manifest != nil {
		parallel = manifest.Harness.Parallel
		maxParallel = manifest.Harness.MaxParallel
	}
	maxParallel = d.clampMaxParallelToBudget(maxParallel, len(members))

	memberRunner := d.buildMemberRunner(swarmCtx, message)
	postMember := d.buildPostMemberHook()

	return swarm.DispatchMembers(ctx, members, memberRunner, swarm.DispatchOptions{
		Parallel:    parallel,
		MaxParallel: maxParallel,
		PostMember:  postMember,
	})
}

// clampMaxParallelToBudget bounds the manifest's MaxParallel against
// the engine's spawn-limit MaxTotalBudget so a single swarm cannot
// monopolise the worker pool. A zero / negative input is treated as
// "no swarm-level cap" and clamped down to the budget.
//
// Expected:
//   - manifestMax is opts.MaxParallel from the manifest (may be 0).
//   - rosterSize is len(members); used as the floor for unset caps.
//
// Returns:
//   - The effective ceiling: min(rosterSize, MaxTotalBudget) when
//     manifestMax <= 0; min(manifestMax, MaxTotalBudget) otherwise.
//
// Side effects:
//   - None.
func (d *DelegateTool) clampMaxParallelToBudget(manifestMax, rosterSize int) int {
	budget := d.spawnLimits.MaxTotalBudget
	cap := manifestMax
	if cap <= 0 {
		cap = rosterSize
	}
	if budget > 0 && cap > budget {
		cap = budget
	}
	return cap
}

// buildMemberRunner returns the swarm.MemberRunner closure that drives
// one member's stream through the per-swarm Runner. The closure is
// shared across every member of the fan-out so retry/breaker state
// accumulates correctly on the cached Runner.
//
// When the member id resolves to another swarm in the registry, the
// closure recurses into DispatchSwarmMembers with a child context
// constructed via Context.NestSubSwarm so the chain prefix and depth
// carry the parent/child trace (Task 5). The recursion shares the
// engine's spawn-limit MaxTotalBudget so active fan-out across all
// depths cannot exceed the configured ceiling (P0.6).
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - message is the prompt forwarded to every member.
//
// Returns:
//   - A swarm.MemberRunner the dispatcher invokes per member.
//
// Side effects:
//   - On invocation: resolves the target engine OR recurses into a
//     nested DispatchSwarmMembers call.
func (d *DelegateTool) buildMemberRunner(swarmCtx *swarm.Context, message string) swarm.MemberRunner {
	return func(ctx context.Context, memberID string) error {
		if subSwarm := d.resolveSubSwarm(memberID); subSwarm != nil {
			child := swarmCtx.NestSubSwarm(memberID)
			child.LeadAgent = subSwarm.Lead
			child.Members = append([]string(nil), subSwarm.Members...)
			child.Gates = append([]swarm.GateSpec(nil), subSwarm.Harness.Gates...)
			return d.DispatchSwarmMembers(ctx, &child, subSwarm.Members, message)
		}
		eng, ok := d.engines[memberID]
		if !ok || eng == nil {
			return fmt.Errorf("no engine for swarm member %q", memberID)
		}
		runner := d.runnerForSwarm(swarmCtx.SwarmID, d.manifestForSwarm(swarmCtx.SwarmID))
		target := delegationTarget{
			agentID: memberID,
			engine:  eng,
			message: message,
		}
		var result delegationResult
		return runner.Dispatch(ctx, memberID, func(dispatchCtx context.Context, _ string) error {
			return d.streamAndCollect(dispatchCtx, target, &result)
		})
	}
}

// resolveSubSwarm returns the manifest for memberID when the swarm
// registry has a swarm with that id AND no agent engine matches the
// id. The agent-vs-swarm precedence mirrors swarm.Resolve: agents win
// at the registry boundary so a swarm whose id collides with an
// agent stays callable as the agent (the validator already prevents
// this collision globally).
//
// Expected:
//   - memberID is one entry from the swarm-context roster.
//
// Returns:
//   - The child manifest when memberID resolves to a swarm and not an
//     agent.
//   - nil when memberID is an agent or unknown.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveSubSwarm(memberID string) *swarm.Manifest {
	if d.swarmRegistry == nil {
		return nil
	}
	if eng, ok := d.engines[memberID]; ok && eng != nil {
		return nil
	}
	m, ok := d.swarmRegistry.Get(memberID)
	if !ok {
		return nil
	}
	return m
}

// buildPostMemberHook wires DispatchOptions.PostMember to the
// engine's existing post-member gate dispatcher. The hook fires on
// the worker goroutine so peers still in flight see a gate failure
// before they release the semaphore slot — matches the §T37
// contract.
//
// Returns:
//   - The MemberPostHook closure; nil when no gate runner is wired
//     so the dispatcher skips the hook entirely.
//
// Side effects:
//   - On invocation: calls dispatchPostMemberGates on the engine.
func (d *DelegateTool) buildPostMemberHook() swarm.MemberPostHook {
	if d.gateRunner == nil {
		return nil
	}
	return func(ctx context.Context, memberID string, runErr error) error {
		if runErr != nil {
			return nil
		}
		return d.dispatchPostMemberGates(ctx, memberID)
	}
}

// dispatchPostMemberGates fires every post-member gate on the active
// swarm context whose Target matches memberID. Thin wrapper over
// dispatchMemberGates kept for call-site readability — executeSync
// still reads "post-member dispatch happens here" at a glance.
//
// Expected:
//   - ctx is the delegation context.
//   - memberID is the agent id whose stream just completed.
//
// Returns:
//   - nil when no gate runner is wired, no swarm context is in flight,
//     no gates target memberID, or every matching gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each matching gate's runner, which may read from the
//     coordination store.
func (d *DelegateTool) dispatchPostMemberGates(ctx context.Context, memberID string) error {
	return d.dispatchMemberGates(ctx, swarm.LifecyclePostMember, memberID)
}

// dispatchPreMemberGates fires every pre-member gate on the active
// swarm context whose Target matches memberID. Called by executeSync
// just before the targeted member's Stream is invoked so the gate
// runner can validate prerequisite coord-store keys exist. Phase 2
// of T-swarm-3.
//
// Expected:
//   - ctx is the delegation context.
//   - memberID is the agent id whose stream is about to start.
//
// Returns:
//   - nil under the same conditions as dispatchPostMemberGates.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - See dispatchMemberGates.
func (d *DelegateTool) dispatchPreMemberGates(ctx context.Context, memberID string) error {
	return d.dispatchMemberGates(ctx, swarm.LifecyclePreMember, memberID)
}

// dispatchMemberGates fires every gate on the active swarm context
// whose When matches when ("pre-member" or "post-member") and whose
// Target matches memberID. Single helper underlying both
// dispatchPreMemberGates and dispatchPostMemberGates so the lifecycle
// fan-out logic (gate-runner / swarm-context / coord-store
// resolution) lives in exactly one place.
//
// Expected:
//   - ctx is the delegation context.
//   - when is a member-level lifecycle point.
//   - memberID is the agent id the runner is wrapping.
//
// Returns:
//   - nil when no gate runner is wired, no swarm context is in flight,
//     no gates match the (when, memberID) pair, or every matching gate
//     passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each matching gate's runner.
func (d *DelegateTool) dispatchMemberGates(ctx context.Context, when, memberID string) error {
	if d.gateRunner == nil {
		return nil
	}
	swarmCtx, ok := d.activeSwarmContext()
	if !ok {
		return nil
	}
	matches := swarm.MemberGatesFor(swarmCtx.Gates, when, memberID)
	if len(matches) == 0 {
		return nil
	}
	args := swarm.GateArgs{
		SwarmID:     swarmCtx.SwarmID,
		ChainPrefix: swarmCtx.ChainPrefix,
		MemberID:    memberID,
		CoordStore:  d.coordinationStore,
	}
	report := swarm.Dispatch(ctx, d.gateRunner, matches, args)
	if report.Halted {
		return report.Err
	}
	return nil
}

// dispatchPreSwarmGatesOnce fires every pre-swarm gate on the active
// swarm context exactly once per swarm id. The "once" guarantee is
// the key contract for pre-swarm: even though the dispatcher is
// called from executeSync (which fires per delegation), the swarm
// runner spec promises pre-swarm fires ONCE at swarm start.
// prefiredSwarmIDs records firings; subsequent calls are no-ops for
// the same swarm.
//
// Expected:
//   - ctx is the delegation context (the lead's tool-call context).
//
// Returns:
//   - nil when no swarm is in flight, no pre-swarm gates exist on
//     the manifest, the gates have already fired this swarm run, or
//     every gate passes.
//   - The first *swarm.GateError otherwise. On failure, the swarm
//     id is NOT marked as fired so a retry from a fresh delegation
//     would attempt the gates again (Phase 2 still halts fail-fast,
//     so the retry only matters when an upstream caller catches the
//     error and re-invokes; future Phase 3 retry/rollback work will
//     rely on this invariant).
//
// Side effects:
//   - Calls each matching gate's runner under the swarm lifecycle
//     mutex.
func (d *DelegateTool) dispatchPreSwarmGatesOnce(ctx context.Context) error {
	if d.gateRunner == nil {
		return nil
	}
	swarmCtx, ok := d.activeSwarmContext()
	if !ok {
		return nil
	}
	if !d.markPreSwarmFiring(swarmCtx.SwarmID) {
		return nil
	}
	if err := d.runSwarmGates(ctx, swarmCtx, swarm.LifecyclePreSwarm); err != nil {
		d.unmarkPreSwarmFiring(swarmCtx.SwarmID)
		return err
	}
	return nil
}

// FlushSwarmLifecycle fires the post-swarm gates on the active swarm
// context (if any) and forgets the pre-swarm "fired" flag for that
// swarm id. The swarm-runner caller (cli.runPrompt / chat intent
// after the lead's stream returns) invokes this so a swarm-level
// `when: post` gate can validate the final aggregated state.
//
// Phase 2 still uses a single DelegateTool per app instance, so a
// long-lived process running multiple back-to-back swarm sessions
// MUST call FlushSwarmLifecycle between them. Skipping the flush
// would leak pre-swarm fired-state from the previous run and
// suppress the next session's pre-swarm dispatch.
//
// Expected:
//   - ctx is the swarm-runner's outer context (the same one driving
//     the lead's Stream).
//
// Returns:
//   - nil when no gate runner is wired, no swarm is in flight, or
//     every post-swarm gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each post-swarm gate's runner.
//   - Clears the pre-swarm fired marker for the swarm id.
func (d *DelegateTool) FlushSwarmLifecycle(ctx context.Context) error {
	if d.gateRunner == nil {
		return nil
	}
	swarmCtx, ok := d.activeSwarmContext()
	if !ok {
		return nil
	}
	defer d.unmarkPreSwarmFiring(swarmCtx.SwarmID)
	return d.runSwarmGates(ctx, swarmCtx, swarm.LifecyclePostSwarm)
}

// runSwarmGates dispatches every swarm-level gate on swarmCtx whose
// When matches when. Used by both dispatchPreSwarmGatesOnce and
// FlushSwarmLifecycle; centralising the args / loop here keeps the
// callers focused on their own lifecycle invariants.
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - when is "pre" or "post".
//
// Returns:
//   - nil when no gates match or every gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each matching gate's runner. MemberID is empty on the
//     args because swarm-level gates have no per-member fan-out.
func (d *DelegateTool) runSwarmGates(ctx context.Context, swarmCtx *swarm.Context, when string) error {
	matches := swarm.SwarmGatesFor(swarmCtx.Gates, when)
	if len(matches) == 0 {
		return nil
	}
	args := swarm.GateArgs{
		SwarmID:     swarmCtx.SwarmID,
		ChainPrefix: swarmCtx.ChainPrefix,
		CoordStore:  d.coordinationStore,
	}
	report := swarm.Dispatch(ctx, d.gateRunner, matches, args)
	if report.Halted {
		return report.Err
	}
	return nil
}

// markPreSwarmFiring records that pre-swarm gates are firing for
// swarmID. The returned bool reports whether the caller should
// proceed with the dispatch (true) or skip because another caller has
// already fired (false). The map is lazily initialised so the zero-
// value DelegateTool stays usable.
//
// Expected:
//   - swarmID is the active swarm's id.
//
// Returns:
//   - true when the caller should proceed with the dispatch.
//   - false when pre-swarm has already fired for this swarm id.
//
// Side effects:
//   - Mutates prefiredSwarmIDs under swarmLifecycleMu.
func (d *DelegateTool) markPreSwarmFiring(swarmID string) bool {
	d.swarmLifecycleMu.Lock()
	defer d.swarmLifecycleMu.Unlock()
	if d.prefiredSwarmIDs == nil {
		d.prefiredSwarmIDs = make(map[string]bool)
	}
	if d.prefiredSwarmIDs[swarmID] {
		return false
	}
	d.prefiredSwarmIDs[swarmID] = true
	return true
}

// unmarkPreSwarmFiring clears the pre-swarm fired marker for
// swarmID. Called from FlushSwarmLifecycle on swarm end and from the
// pre-swarm dispatcher's failure path so a subsequent retry attempts
// the gates again.
//
// Expected:
//   - swarmID is the active swarm's id.
//
// Side effects:
//   - Mutates prefiredSwarmIDs under swarmLifecycleMu.
func (d *DelegateTool) unmarkPreSwarmFiring(swarmID string) {
	d.swarmLifecycleMu.Lock()
	defer d.swarmLifecycleMu.Unlock()
	delete(d.prefiredSwarmIDs, swarmID)
}

// activeSwarmContext returns the swarm.Context installed on the lead
// engine, if any. T-swarm-2 wires the context onto the lead engine
// (the engine whose id matches the DelegateTool's sourceAgentID); the
// dispatcher reads it from there so post-member gates see the same
// gate slice the runner authored.
//
// Expected:
//   - d.sourceAgentID names the lead engine in d.engines.
//
// Returns:
//   - The lead engine's swarm context and true when set.
//   - (nil, false) when no lead engine is registered, or when the
//     lead engine has no swarm context (single-agent mode).
//
// Side effects:
//   - None.
func (d *DelegateTool) activeSwarmContext() (*swarm.Context, bool) {
	// Owner-engine path is the canonical lookup: the DelegateTool is
	// installed on the lead's engine via eng.AddTool, and the lead's
	// engine is exactly where DispatchSwarm sets the swarm context.
	// Reading from d.engines[d.sourceAgentID] doesn't work here because
	// buildDelegateMaps explicitly EXCLUDES the lead from the targets
	// map (see app.go's `if agentManifest.ID == excludeID { continue }`)
	// — the lead can never delegate to itself, so its engine is absent
	// from that map by design. Without WithOwnerEngine wiring we'd
	// silently fall through and gates would never fire.
	if d.ownerEngine != nil {
		if swarmCtx := d.ownerEngine.SwarmContext(); swarmCtx != nil {
			return swarmCtx, true
		}
	}
	// Backwards-compatible fallback for tests that wired d.engines
	// without WithOwnerEngine: try the targets map. This path stays
	// inert for lead-style DelegateTools (their id is the excluded
	// one) but covers the historical "sub-DelegateTool inside a target
	// engine" case where the target's id IS in the map.
	if d.engines == nil {
		return nil, false
	}
	leadEngine, ok := d.engines[d.sourceAgentID]
	if !ok || leadEngine == nil {
		return nil, false
	}
	swarmCtx := leadEngine.SwarmContext()
	if swarmCtx == nil {
		return nil, false
	}
	return swarmCtx, true
}

// executeAsync runs delegation asynchronously, returning immediately with a task ID.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result containing the task ID.
//   - An error if background mode is disabled or task launch fails.
//
// Side effects:
//   - Spawns a goroutine for the delegation.
//   - Emits delegation events for started status.
func (d *DelegateTool) executeAsync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
	if d.backgroundManager == nil {
		return tool.Result{}, errBackgroundModeDisabled
	}

	taskID := d.createChildSession(ctx, target.agentID)
	if taskID == "" {
		taskID = fmt.Sprintf("task-%s-%d", target.agentID, time.Now().UTC().UnixNano())
	}

	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")

	d.backgroundManager.Launch(context.WithoutCancel(ctx), taskID, target.agentID, target.message, func(ctx context.Context) (string, error) {
		delegateCtx := context.WithValue(ctx, session.IDKey{}, taskID)
		result, err := d.executeBackgroundTask(delegateCtx, target, baseInfo, outChan, hasOutput)
		if err != nil {
			return "", err
		}
		return result, nil
	})

	return tool.Result{
		Output: fmt.Sprintf(`{"task_id": %q, "status": "running"}`, taskID),
		Title:  target.message,
		Metadata: map[string]interface{}{
			"sessionId": taskID,
		},
	}, nil
}

// executeBackgroundTask performs the actual delegation within a background goroutine.
//
// Expected:
//   - ctx is the task context with cancellation support.
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - The delegation result string on success.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events for completed or failed status.
func (d *DelegateTool) executeBackgroundTask(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (string, error) {
	closeStore := d.attachSessionStore(target.engine, sessionIDFromContext(ctx))

	chunks, err := d.resolveStreamer(target.agentID, target.engine).Stream(ctx, target.agentID, target.message)
	if err != nil {
		closeStore()
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return "", fmt.Errorf("delegation failed: %w", err)
	}

	chunks = d.wrapWithAccumulator(ctx, chunks, sessionIDFromContext(ctx), target.agentID)

	result, err := d.collectDelegationResult(chunks)
	closeStore()
	if err != nil {
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return "", err
	}

	d.circuitBreaker.RecordSuccess()
	completedAt := time.Now().UTC()
	baseInfo.ModelName = target.engine.LastModel()
	baseInfo.ProviderName = target.engine.LastProvider()
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")

	taskSessionID := sessionIDFromContext(ctx)
	d.closeSessionIfManaged(taskSessionID)

	return result.response, nil
}

// resolveTargetWithOptions validates input and resolves the target with async options.
//
// Expected:
//   - ctx is a valid context for the discovery operation.
//   - input contains subagent_type, message, run_in_background, and optional handoff arguments.
//
// Returns:
//   - The resolved target with chain ID.
//   - Whether to run asynchronously.
//   - An error if delegation is disabled, inputs are invalid, or no target exists.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveTargetWithOptions(ctx context.Context, params delegationParams) (delegationTarget, error) {
	if !d.delegation.CanDelegate {
		return delegationTarget{}, errDelegationNotAllowed
	}

	targetAgentID, err := d.resolveAgentID(ctx, params)
	if err != nil {
		return delegationTarget{}, err
	}

	if len(d.delegation.DelegationAllowlist) > 0 && !containsAgent(d.delegation.DelegationAllowlist, targetAgentID) {
		return delegationTarget{}, fmt.Errorf("%w: %q not in allowlist: %v",
			errAgentNotInAllowlist, targetAgentID, d.delegation.DelegationAllowlist)
	}

	if err := checkDelegationCycle(d.sourceAgentID, targetAgentID, params.handoff); err != nil {
		return delegationTarget{}, err
	}

	// Precedence: top-level chainID > handoff.chain_id > fresh fallback.
	// chainIDFromCaller distinguishes a planner-supplied value (which
	// drives preamble injection) from the auto-generated fallback (which
	// stays internal for backwards compatibility).
	var chainID string
	chainIDFromCaller := false
	switch {
	case params.chainID != "":
		chainID = params.chainID
		chainIDFromCaller = true
	case params.handoff != nil && params.handoff.ChainID != "":
		chainID = params.handoff.ChainID
		chainIDFromCaller = true
	default:
		chainID = newDelegationChainID()
	}

	targetEngine, ok := d.engines[targetAgentID]
	if !ok {
		return delegationTarget{}, fmt.Errorf("target agent engine not available: %s", targetAgentID)
	}

	var resolvedModel, resolvedProvider string
	if params.category != "" && d.categoryResolver != nil {
		if cfg, resolveErr := d.categoryResolver.Resolve(params.category); resolveErr == nil {
			resolvedModel = cfg.Model
			resolvedProvider = cfg.Provider
		}
	}

	d.applySkillsAndSessionMode(targetEngine, params)

	return delegationTarget{
		agentID:           targetAgentID,
		engine:            targetEngine,
		message:           params.message,
		handoff:           params.handoff,
		chainID:           chainID,
		chainIDFromCaller: chainIDFromCaller,
		resolvedModel:     resolvedModel,
		resolvedProvider:  resolvedProvider,
		requestedSession:  params.sessionID,
		denyDelegate:      !d.agentHasToolPermission(targetAgentID, "delegate"),
		denyTodoWrite:     !d.agentHasToolPermission(targetAgentID, "todowrite"),
	}, nil
}

// checkTargetToolCapability rejects delegation when the resolved
// (provider, model) for the sub-agent is not in the tool-capable allow
// list (or matches the deny list). The check is skipped when no allow
// list is configured — see WithToolCapability for rationale.
//
// Expected:
//   - target carries either an explicit resolvedProvider/resolvedModel
//     (set by category routing) or a sub-engine whose LastModel/LastProvider
//     reports the manifest's first failover preference.
//
// Returns:
//   - nil when the gate is disabled or the model is approved.
//   - errModelNotToolCapable wrapped with the offending agent + (provider,
//     model) so the parent agent can recover by re-delegating.
//
// Side effects:
//   - None.
func (d *DelegateTool) checkTargetToolCapability(target delegationTarget) error {
	if len(d.toolCapableModels) == 0 && len(d.toolIncapableModels) == 0 {
		return nil
	}
	providerName, modelName := resolveTargetProviderModel(target)
	if modelName == "" {
		return nil
	}
	if IsToolCapableModel(providerName, modelName, d.toolCapableModels, d.toolIncapableModels) {
		return nil
	}
	return fmt.Errorf("%w: target agent %q would resolve to (%s, %s); configure tool_capable_models in config.yaml or pick a different agent",
		errModelNotToolCapable, target.agentID, providerName, modelName)
}

// resolveTargetProviderModel returns the (provider, model) pair the
// sub-agent will run on, preferring the explicitly resolved values from
// category routing and falling back to the engine's first failover
// preference. Both values may be empty when neither path produced a
// resolution; callers treat that as "no opinion, skip the check".
//
// Expected:
//   - target.engine is non-nil whenever the caller intends to stream.
//
// Returns:
//   - The provider name and model name to consult against the
//     capability allow/deny lists.
//
// Side effects:
//   - None.
func resolveTargetProviderModel(target delegationTarget) (string, string) {
	providerName := target.resolvedProvider
	modelName := target.resolvedModel
	if target.engine != nil {
		if providerName == "" {
			providerName = target.engine.LastProvider()
		}
		if modelName == "" {
			modelName = target.engine.LastModel()
		}
	}
	return providerName, modelName
}

// closeSessionIfManaged closes the named session via the session manager when one is configured.
//
// Expected:
//   - sessionID identifies the session to close.
//
// Side effects:
//   - Closes the session in the session manager if one is set.
//   - Suppresses ErrSessionNotFound; other errors are silently discarded.
func (d *DelegateTool) closeSessionIfManaged(sessionID string) {
	if d.sessionManager == nil {
		return
	}
	if err := d.sessionManager.CloseSession(sessionID); err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		_ = err
	}
}

// resolveAgentID attempts registry lookup via subagent_type, then falls back to discovery.
//
// Expected:
//   - ctx is a valid context for discovery operations.
//   - params contains routing fields from the delegation input.
//
// Returns:
//   - The resolved agent ID on success.
//   - An error if no agent can be resolved.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveAgentID(ctx context.Context, params delegationParams) (string, error) {
	var registryErr error
	if params.subagentType != "" {
		resolvedID, err := d.ResolveByNameOrAlias(params.subagentType)
		if err == nil {
			return resolvedID, nil
		}
		registryErr = err
		if _, ok := d.engines[params.subagentType]; ok {
			return params.subagentType, nil
		}
	}

	if params.subagentType == "" {
		return "", errRoutingFieldRequired
	}

	id, discErr := d.resolveWithDiscovery(ctx, params.subagentType, params.message)
	if discErr != nil && registryErr != nil && d.registry != nil {
		return "", registryErr
	}
	return id, discErr
}

// resolveWithDiscovery attempts to resolve the target agent using embedding-based discovery.
//
// Expected:
//   - ctx is a valid context for the embedding operation.
//   - taskType is the delegation task type key.
//   - message is the delegation message for embedding.
//
// Returns:
//   - The resolved target agent ID.
//   - An error if resolution fails.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveWithDiscovery(ctx context.Context, taskType, message string) (string, error) {
	if d.embeddingDiscovery != nil {
		matches, err := d.embeddingDiscovery.Match(ctx, taskType+" "+message)
		if err == nil && len(matches) > 0 && matches[0].Confidence >= 0.7 {
			if _, ok := d.engines[matches[0].AgentID]; ok {
				return matches[0].AgentID, nil
			}
		}
	}

	return "", fmt.Errorf("no agent configured for task type: %s", taskType)
}

// parseHandoff parses a handoff argument into a delegation.Handoff struct.
//
// Expected:
//   - handoffArg is an interface{} that can be unmarshalled to Handoff.
//
// Returns:
//   - A parsed Handoff on success.
//   - An error if parsing fails.
//
// Side effects:
//   - None.
func (d *DelegateTool) parseHandoff(handoffArg interface{}) (*delegation.Handoff, error) {
	var h delegation.Handoff

	switch v := handoffArg.(type) {
	case map[string]interface{}:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, errHandoffMustBeObject
		}
		if err := json.Unmarshal(data, &h); err != nil {
			return nil, errHandoffMustBeObject
		}
	case string:
		if err := json.Unmarshal([]byte(v), &h); err != nil {
			return nil, errHandoffMustBeObject
		}
	default:
		return nil, errHandoffMustBeObject
	}

	return &h, nil
}

// collectDelegationResult aggregates streamed chunks from the delegated agent.
//
// Expected:
//   - chunks is the stream returned by the target engine.
//
// Returns:
//   - The concatenated response text.
//   - The number of chunks observed.
//   - The most recent tool name seen in the stream.
//   - An error if the stream yields a chunk error.
//
// Side effects:
//   - Reads from the streamed chunk channel until it closes or returns an error.
func (d *DelegateTool) collectDelegationResult(chunks <-chan provider.StreamChunk) (delegationResult, error) {
	var response strings.Builder
	toolCalls := 0
	lastTool := ""
	for chunk := range chunks {
		toolCalls++
		if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
			lastTool = chunk.ToolCall.Name
		}
		if chunk.Error != nil {
			return delegationResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
		}
		response.WriteString(chunk.Content)
	}

	return delegationResult{response: response.String(), toolCalls: toolCalls, lastTool: lastTool}, nil
}

// checkRejectionLimit returns errMaxRejectionsExhausted when the rejection
// count for chainID has reached or exceeded the maximum configured on the tracker.
// Returns nil when no tracker is configured or the limit has not been reached.
//
// Expected:
//   - chainID identifies the current delegation chain.
//
// Returns:
//   - errMaxRejectionsExhausted when the limit is exhausted.
//   - nil otherwise.
//
// Side effects:
//   - Reads the coordination store via the rejection tracker.
func (d *DelegateTool) checkRejectionLimit(ctx context.Context, chainID string) error {
	if d.rejectionTracker == nil || chainID == "" {
		return nil
	}
	exhausted, err := d.rejectionTracker.ExhaustedFor(ctx, chainID)
	if err != nil {
		return fmt.Errorf("checking rejection limit: %w", err)
	}
	if exhausted {
		return errMaxRejectionsExhausted
	}
	return nil
}

// newDelegationChainID returns a unique identifier for a delegation chain.
//
// Returns:
//   - A chain identifier string derived from the current UTC time.
//
// Side effects:
//   - Reads the current clock to ensure uniqueness.
func newDelegationChainID() string {
	return fmt.Sprintf("chain-%d", time.Now().UTC().UnixNano())
}

// coordStoreKeyConvention returns the canonical coord-store key suffix
// for the given specialist agent ID, or the empty string when the
// agent is outside the known set. The map encodes the contract that
// each role-specific prompt repeats in English; the engine owns this
// suffix-per-role mapping as data so the auto-injection helper can
// construct the canonical key without re-parsing prompts.
//
// Expected:
//   - agentID is the specialist's manifest ID.
//
// Returns:
//   - The coord-store suffix when the agent is in the known set.
//   - "" when the agent is custom or ad-hoc.
//
// Side effects:
//   - None.
func coordStoreKeyConvention(agentID string) string {
	switch agentID {
	case "explorer":
		return "codebase-findings"
	case "librarian":
		return "external-refs"
	case "analyst":
		return "analysis"
	case "plan-writer":
		return "plan"
	case "plan-reviewer":
		return "review"
	default:
		return ""
	}
}

// autoInjectChainIDPreamble prepends a structured chainID preamble to
// the message when chainID is non-empty AND the message does not
// already contain `chainID=<value>`. For specialists in the
// coordStoreKeyConvention map, the preamble also names the canonical
// `coordination_store key=<chainID>/<role-suffix>` line. Agents outside
// the map receive the chainID line only.
//
// Idempotency is the contract that lets the planner prompt continue
// to embed the chainID in free-form text (the post-e899dcc
// behaviour) without ever producing duplicates: the injector detects
// the existing substring and returns the message unchanged.
//
// Expected:
//   - message is the caller's free-form delegation message.
//   - agentID is the target specialist's manifest ID.
//   - chainID is the authoritative chain identifier; when empty the
//     message is returned unchanged.
//
// Returns:
//   - The composed user message with preamble (or the original message
//     when no injection is required).
//
// Side effects:
//   - None.
func autoInjectChainIDPreamble(message, agentID, chainID string) string {
	if chainID == "" {
		return message
	}
	marker := "chainID=" + chainID
	if strings.Contains(message, marker) {
		return message
	}
	preamble := marker + "."
	if suffix := coordStoreKeyConvention(agentID); suffix != "" {
		preamble += " Write your findings to coordination_store key=" + chainID + "/" + suffix + "."
	}
	if message == "" {
		return preamble
	}
	return preamble + "\n\n" + message
}

// emitDelegationEvent sends a DelegationInfo chunk to the output channel when available.
//
// Expected:
//   - hasOutput indicates whether delegation events should be published.
//   - base contains the delegation metadata to reuse for the emitted chunk.
//
// Side effects:
//   - Attempts a non-blocking send to the output channel if it's still open.
//   - Silently drops events if the channel is full or closed (common when parent context is cancelled).
//   - Recovers from panic if the channel was closed by the parent context.
func (d *DelegateTool) emitDelegationEvent(
	outChan chan<- provider.StreamChunk, hasOutput bool,
	base provider.DelegationInfo, status string,
) {
	if !hasOutput {
		return
	}

	defer func() {
		if recover() == nil {
			return
		}
	}()

	info := base
	info.Status = status
	select {
	case outChan <- provider.StreamChunk{DelegationInfo: &info}:
	default:
	}
}

// deliverProgressEvent sends a ProgressEvent to the parent output channel when available.
//
// Expected:
//   - ctx carries the parent output channel via WithStreamOutput.
//   - toolCalls is the current count of tool invocations.
//   - lastTool is the name of the most recently invoked tool.
//   - startedAt is the time delegation began.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Attempts a non-blocking send to the parent output channel.
//   - Silently drops the event if the channel is full or absent.
func (d *DelegateTool) deliverProgressEvent(ctx context.Context, toolCalls int, lastTool string, startedAt time.Time) {
	outChan, ok := streamOutputFromContext(ctx)
	if !ok {
		return
	}

	activeDelegations := 0
	if d.backgroundManager != nil {
		activeDelegations = d.backgroundManager.ActiveCount()
	}

	ev := streaming.ProgressEvent{
		ToolCallCount:     toolCalls,
		LastTool:          lastTool,
		ActiveDelegations: activeDelegations,
		ElapsedTime:       time.Since(startedAt),
	}

	defer func() {
		if recover() == nil {
			return
		}
	}()
	select {
	case outChan <- provider.StreamChunk{Event: ev}:
	default:
	}
}

// collectWithProgress aggregates delegation chunks and periodically emits ProgressEvents.
//
// Expected:
//   - ctx carries the parent output channel for progress delivery.
//   - chunks is the stream channel from the child engine.
//   - startedAt is the delegation start time.
//
// Returns:
//   - A delegationResult with accumulated response, tool call count, and last tool name.
//   - An error if any chunk carries a stream error.
//
// Side effects:
//   - Emits ProgressEvents every 5 tool calls or every 5 seconds via deliverProgressEvent.
func (d *DelegateTool) collectWithProgress(
	ctx context.Context,
	chunks <-chan provider.StreamChunk,
	startedAt time.Time,
) (delegationResult, error) {
	var response strings.Builder
	toolCalls := 0
	lastTool := ""
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	const progressInterval = 5

	for {
		select {
		case <-ctx.Done():
			return delegationResult{}, ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return delegationResult{response: response.String(), toolCalls: toolCalls, lastTool: lastTool}, nil
			}
			toolCalls++
			if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
				lastTool = chunk.ToolCall.Name
			}
			if chunk.Error != nil {
				return delegationResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
			}
			response.WriteString(chunk.Content)
			if toolCalls%progressInterval == 0 {
				d.deliverProgressEvent(ctx, toolCalls, lastTool, startedAt)
			}
		case <-ticker.C:
			d.deliverProgressEvent(ctx, toolCalls, lastTool, startedAt)
		}
	}
}

// ptrTime returns a pointer to the supplied time.
//
// Expected:
//   - t is a valid time value to reference.
//
// Returns:
//   - A pointer to t.
//
// Side effects:
//   - None.
func ptrTime(t time.Time) *time.Time {
	return &t
}

// DelegateToAgent sends a message to a sub-agent and streams the response.
//
// Expected:
//   - ctx is a valid context for the delegation operation.
//   - engines is a map of agent IDs to their Engine instances.
//   - agentID identifies the delegation target directly.
//   - message is the instruction to send to the target agent.
//
// Returns:
//   - A channel of StreamChunk values from the target agent.
//   - An error if delegation is not allowed or the target agent is unavailable.
//
// Side effects:
//   - Initiates a streaming request on the target agent's engine.
func (e *Engine) DelegateToAgent(
	ctx context.Context,
	engines map[string]*Engine,
	agentID string,
	message string,
) (<-chan provider.StreamChunk, error) {
	if !e.manifest.Delegation.CanDelegate {
		return nil, errDelegationNotAllowed
	}

	targetEngine, ok := engines[agentID]
	if !ok {
		return nil, fmt.Errorf("target agent engine not available: %s", agentID)
	}

	return targetEngine.Stream(ctx, agentID, message)
}

// BackgroundManager returns the background task manager for this delegate tool.
//
// Returns:
//   - The BackgroundTaskManager if configured, or nil.
//
// Side effects:
//   - None.
func (d *DelegateTool) BackgroundManager() *BackgroundTaskManager {
	return d.backgroundManager
}

// CoordinationStore returns the coordination store for this delegate tool.
//
// Returns:
//   - The coordination.Store if configured, or nil.
//
// Side effects:
//   - None.
func (d *DelegateTool) CoordinationStore() coordination.Store {
	return d.coordinationStore
}

// HasEmbeddingDiscovery reports whether an embedding discovery has been wired.
//
// Returns:
//   - true when SetEmbeddingDiscovery has been called with a non-nil value.
//
// Side effects:
//   - None.
func (d *DelegateTool) HasEmbeddingDiscovery() bool {
	return d.embeddingDiscovery != nil
}

// SetDelegation updates the delegation configuration for this tool.
//
// Expected:
//   - config is the new delegation configuration to apply.
//
// Side effects:
//   - Replaces the internal delegation config used during Execute().
func (d *DelegateTool) SetDelegation(config agent.Delegation) {
	d.delegation = config
}

// SetSourceAgentID updates the source agent identifier for delegation event attribution.
//
// Expected:
//   - id is the identifier of the agent that owns this tool.
//
// Side effects:
//   - Replaces the internal sourceAgentID used during Execute().
func (d *DelegateTool) SetSourceAgentID(id string) {
	d.sourceAgentID = id
}

// Delegation returns the current delegation configuration.
//
// Returns:
//   - The agent.Delegation currently in use by this tool.
//
// Side effects:
//   - None.
func (d *DelegateTool) Delegation() agent.Delegation {
	return d.delegation
}

// CircuitBreaker returns the circuit breaker protecting the delegation flow.
//
// Returns:
//   - The CircuitBreaker instance used by this tool.
//
// Side effects:
//   - None.
func (d *DelegateTool) CircuitBreaker() *delegation.CircuitBreaker {
	return d.circuitBreaker
}

// InjectSkillsIfProvided prepends skill content to the base system prompt if loadSkills is non-empty.
//
// Expected:
//   - loadSkills is a slice of skill names to resolve.
//   - basePrompt is the initial system prompt to prepend skills to.
//
// Returns:
//   - The base prompt with skill content prepended (if resolver is available and loadSkills is non-empty).
//   - The base prompt unchanged if no resolver is configured or loadSkills is empty.
//
// Side effects:
//   - None.
func (d *DelegateTool) InjectSkillsIfProvided(loadSkills []string, basePrompt string) string {
	if d.skillResolver == nil || len(loadSkills) == 0 {
		return basePrompt
	}

	var skillContents []string
	for _, skillName := range loadSkills {
		content, err := d.skillResolver.Resolve(skillName)
		if err != nil {
			continue
		}
		marker := extractSkillMarker(content)
		if marker != "" && containsSkillMarker(basePrompt, marker) {
			continue
		}
		skillContents = append(skillContents, content)
	}

	if len(skillContents) == 0 {
		return basePrompt
	}

	return strings.Join(skillContents, "\n\n") + "\n\n" + basePrompt
}

// extractSkillMarker returns the first line of content if it starts with a
// Markdown heading (# or ##). This is used as a deduplication marker to avoid
// injecting the same skill twice into a prompt.
//
// Expected:
//   - content is a non-empty skill content string (may be empty, returns "").
//
// Returns:
//   - The first line when it begins with "# " or "## ".
//   - An empty string if content is empty or the first line is not a heading.
//
// Side effects:
//   - None.
func extractSkillMarker(content string) string {
	firstLine, _, _ := strings.Cut(content, "\n")
	if strings.HasPrefix(firstLine, "# ") || strings.HasPrefix(firstLine, "## ") {
		return firstLine
	}
	return ""
}

// containsSkillMarker reports whether marker appears as a complete line in prompt.
// This prevents false-positive prefix matches such as "# Skill: golang" matching
// against "# Skill: golang-testing".
//
// Expected:
//   - marker is a non-empty heading line extracted by extractSkillMarker.
//   - prompt is the base system prompt to search.
//
// Returns:
//   - true if marker appears as a standalone line (followed by "\n" or at end of string).
//
// Side effects:
//   - None.
func containsSkillMarker(prompt, marker string) bool {
	return strings.Contains(prompt, marker+"\n") || strings.HasSuffix(prompt, marker)
}

// Engines returns the delegate engine map keyed by agent ID.
//
// Returns:
//   - A map of agent ID to Engine for each delegation target.
//
// Side effects:
//   - None.
func (d *DelegateTool) Engines() map[string]*Engine {
	return d.engines
}

// containsAgent reports whether agentID appears in the allowlist slice.
//
// Expected:
//   - allowlist is a slice of agent ID strings (may be empty).
//   - agentID is the resolved agent identifier to search for.
//
// Returns:
//   - true if agentID matches any element in allowlist.
//
// Side effects:
//   - None.
func containsAgent(allowlist []string, agentID string) bool {
	for _, id := range allowlist {
		if id == agentID {
			return true
		}
	}
	return false
}
