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
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/turn"
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

	// planOutputDir is the resolved plan_output_dir (perms.PlanOutputDir).
	// FlushSwarmLifecycle hands it to swarm.PublishPlanToVault so the
	// post-swarm phase writes the planning loop's approved plan to a real
	// file in the vault BEFORE the post-swarm honesty gate verifies it.
	// Empty disables the deterministic publish (the gate's plan-key check
	// still applies). This removes the LLM from the persistence path so
	// the synthesis-hang cannot stop the plan from reaching Obsidian.
	planOutputDir string

	// teeChildContent gates whether child-stream content is mirrored
	// into the parent's user-visible stream via teeToParentStream.
	// Defaults to false (zero value) — the child session plus tool_result
	// is the canonical surface. Set via WithTeeChildContent from
	// AppConfig.Delegation.
	teeChildContent bool

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

	// swarmChainIDs records the most recent lead-allocated, caller-supplied
	// chainID per active swarm id (keyed by swarm.Context.SwarmID). The
	// swarm.Context is immutable and SHARED across concurrent member
	// closures, so the chainID cannot be stored on it; recording it here —
	// captured at member-dispatch time in Execute when a swarm is active
	// and the caller supplied a chainID — lets FlushSwarmLifecycle thread
	// the SAME chain to both the deterministic publisher (so it targets the
	// run's plan, not a stale "*/plan" key) and the post-swarm honesty
	// gate's GateArgs.ChainID. Guarded by swarmLifecycleMu because the lead
	// may fan out to multiple members concurrently. The map persists for
	// the DelegateTool's lifetime; a swarm re-run overwrites its own key.
	swarmChainIDs map[string]string

	// eventBus is the engine's shared `*eventbus.EventBus`, installed via
	// WithEventBus during App-level wiring. When non-nil, the executeSync
	// and executeAsync paths publish `delegation.{started,completed,failed}`
	// events at the six lifecycle sites identified by the bus-bridge plan
	// (May 2026). When nil, the publish calls short-circuit so callers that
	// have not opted into the bus (legacy unit tests, embedded callers
	// without a full Engine) keep the historical behaviour.
	eventBus *eventbus.EventBus

	// turnRegistry is the dispatcher's shared Turn registry — installed
	// via WithTurnRegistry during App-level wiring. When non-nil,
	// executeSync mints a per-child Turn via StartOrReuse on the
	// delegateSessionID after resolveOrCreateSession, injects the child
	// turn_id into delegateCtx via turn.WithTurnID +
	// session.WithAccumulatorTurnID + session.WithTurnRecorder so the
	// child engine's accumulator fans persistence onto the same Turn
	// registry the API server projects to the frontend via
	// FindActiveBySession, and calls Complete on success / Fail on the
	// dispatchErr branch — closing the child-side live-UI parity gap
	// (Plans/Child Session Turn Registry Plumbing (May 2026) §Item 2).
	//
	// Type is the narrow childTurnRegistry interface (only the methods
	// executeSync actually invokes) rather than the concrete
	// *turn.Registry so PR2a's S4.2 spec can spy on the Fail call-count
	// to verify the load-bearing R2 defence: the turnOwnedByWrap guard
	// must short-circuit BEFORE turnRegistry.Fail is invoked on a
	// terminal Turn (B5 resolution). Production wiring at
	// engine.NewDelegateTool / App.configureDelegateTool always passes
	// the concrete *turn.Registry returned by dispatcher.TurnRegistry();
	// the interface widens only the test seam.
	//
	// When nil (legacy test constructors without registry awareness),
	// every Turn lifecycle call short-circuits — back-compat for the
	// large pre-existing NewDelegateTool / NewDelegateToolWithBackground
	// callsite footprint per D7.
	turnRegistry childTurnRegistry
}

// childTurnRegistry is the narrow seam DelegateTool consumes for child
// Turn lifecycle. Production wiring uses the concrete *turn.Registry
// returned by dispatcher.TurnRegistry(); PR2a spec wiring may pass a
// thin spy that counts Fail calls so §S4.2 can verify the load-bearing
// R2 defence (turnOwnedByWrap short-circuits BEFORE Fail is invoked on
// a terminal Turn — B5 resolution).
//
// Only the four methods executeSync's lifecycle actually invokes are
// exposed; future methods on *turn.Registry are NOT inherited by this
// interface so the spy surface stays minimal.
type childTurnRegistry interface {
	StartOrReuse(sessionID string) (string, error)
	Append(turnID string, msg session.Message) error
	Complete(turnID string, info turn.ModelInfo) error
	Fail(turnID string, cause error) error
	ResetForRetry(turnID string) error
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
	// loadSkills is the raw user-supplied list of skill names from the
	// delegate tool's load_skills argument, preserved verbatim (pre any
	// resolver/allow-list filter). Threaded onto the bus event payload
	// so the Vue DelegationPanel can render delegation-skills-row chips
	// for the skills the user passed.
	loadSkills []string
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

// maxDelegationResultBytes caps the accumulated response text collected from a
// delegated agent's stream. Results exceeding this limit are truncated in-place
// and flagged so callers can log a warning.
const maxDelegationResultBytes = 100 * 1024

// delegationResult carries the aggregated response and stream metadata from delegation.
type delegationResult struct {
	response  string
	toolCalls int
	lastTool  string
	truncated bool
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

// Name returns the tool name.
//
// Returns:
//   - The string "delegate".
//
// Side effects:
//   - None.
//
// Expected: parameters for Name.
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
//
// Expected: parameters for Description.
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
//
// Expected: parameters for Timeout.
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
//
// Expected: parameters for Schema.
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
	// Meta-Swarm Coordinator Architecture (May 2026) — Phase 3.
	//
	// When the active swarm context lists a SWARM id in Members[] (e.g.
	// meta-swarm whose members are [a-team, dev-swarm, planning-loop,
	// board-room]) and the caller invokes delegate("<swarm-id>", brief),
	// route through DispatchSwarmMembers rather than the agent-engine
	// path. The agent registry has no entry for sub-swarm ids by design
	// — they're swarms, not agents — so the default resolveAgentID
	// path would fail with `no agent configured for task type`.
	//
	// This branch fires only when:
	//   1. An active swarm context exists (d.activeSwarmContext() ok).
	//   2. The target id appears in the active swarm's Members[].
	//   3. The target id resolves in the swarm registry.
	//   4. The target id does NOT resolve in the agent registry
	//      (preserves the agent-target precedence from swarm.Resolve).
	//
	// All four conditions must hold; otherwise control falls through to
	// the existing prepareExecution path so single-agent delegation
	// stays unchanged.
	// Commit 3 — Gap C: route both dispatch branches through the same
	// pre-flight gate set (circuit-breaker, can-delegate, spawn-limit).
	// Pre-commit-3 these checks lived ONLY inside prepareExecution,
	// which fires only on the agent-target path; the swarm-target
	// branch below short-circuited before any gate ran. The fix hoists
	// the gates that don't require a resolved target into a shared
	// helper that runs first. Rejection-tracker stays inside
	// prepareExecution because it needs the resolved target's chain id.
	//
	// preFlightSharedGates re-runs inside prepareExecution on the agent-
	// target path; that's intentional and idempotent — circuit.Allow()
	// is a read, can_delegate is a read, and checkSpawnLimits has no
	// state mutation. The duplication keeps the agent-target path's
	// error ordering unchanged and avoids a wider refactor.
	if gateErr := d.preFlightSharedGates(input); gateErr != nil {
		return tool.Result{}, gateErr
	}

	if result, handled, dispErr := d.tryDispatchSwarmTarget(ctx, input); handled {
		return result, dispErr
	}

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
		marker := "chainID=" + chainID
		if strings.Contains(target.message, marker) {
			// idempotent — marker already present, leave message unchanged
		} else if preamble := d.buildMemberSwarmPreamble(target.agentID, chainID); preamble != "" {
			target.message = injectPreamble(preamble, target.message)
		} else {
			target.message = autoInjectChainIDPreamble(target.message, target.agentID, chainID)
		}
		if target.handoff == nil {
			target.handoff = &delegation.Handoff{}
		}
		target.handoff.ChainID = chainID

		// Bug 1 capture: when a swarm is in flight, record the chainID the
		// lead just used to dispatch this member so the post-swarm phase
		// (FlushSwarmLifecycle → publishPlanForSwarm / runSwarmGates) can
		// target the SAME chain instead of suffix-scanning an arbitrary
		// stale "*/plan" key. Captured at dispatch time because the
		// swarm.Context is immutable and shared across member closures.
		if swarmCtx, ok := d.activeSwarmContextForCtx(ctx); ok && swarmCtx != nil {
			d.recordSwarmChainID(swarmCtx.SwarmID, chainID)
		}
	}

	injectVisitedAgents(&target, d.sourceAgentID)

	baseInfo := d.buildDelegationInfo(target, chainID)

	// Planning-Loop Async-Member Pipeline Halt (May 2026).
	//
	// A swarm lead MUST delegate each roster member SYNCHRONOUSLY. The
	// pipeline is lead-LLM-driven: executeSync blocks on the member,
	// fires the post-member result-schema gate (dispatchPostMemberGates),
	// surfaces the member output to the ROOT coordination_store, and
	// returns the result into the lead's turn so it sequences the next
	// member. The async path (executeAsync → executeBackgroundTask)
	// carries NEITHER the gate NOR the lifecycle flush, so a backgrounded
	// member runs detached: its output is never gated, never surfaced to
	// root, and the lead is never re-entered. The lead's turn then
	// "succeeds" after firing N detached goroutines and the pipeline dies
	// (live: planning-loop session 03785a78 — two `{"task_id":...,
	// "status":"running"}` tool_results, empty root coord_store, no plan).
	//
	// So: when an active swarm context is in flight, a member delegation
	// is force-synced regardless of what the model requested. The planner
	// asked for run_in_background; the swarm structure overrides it. We
	// override at the dispatch branch (not by stripping the lead's
	// background_* tools) because the lead legitimately KEEPS
	// background_output/background_cancel in its toolset to poll/cancel
	// its OWN standalone async work — only roster-member delegation must
	// be sync.
	//
	// Discriminator precision — this fires on EXACTLY the right path:
	//   - Sub-swarm dispatch (tryDispatchSwarmTarget for a swarm-id in
	//     Members[]) already returned at the top of Execute with
	//     handled=true, so it never reaches here — its own correctly-
	//     working DispatchSwarmMembers path is untouched.
	//   - A standalone (non-swarm) background delegation has no active
	//     swarm context (activeSwarmContextForCtx returns ok=false), so
	//     params.runAsync is left untouched and it stays async.
	// Therefore control reaching this branch with an active swarm context
	// is necessarily a lead delegating an ordinary roster member.
	if params.runAsync {
		if _, inSwarm := d.activeSwarmContextForCtx(ctx); inSwarm {
			params.runAsync = false
		}
	}

	if params.runAsync {
		return d.executeAsync(ctx, target, baseInfo, outChan, hasOutput)
	}

	return d.executeSync(ctx, target, baseInfo, outChan, hasOutput)
}

// preFlightSharedGates runs the pre-resolve gate set that applies to
// BOTH the agent-target and swarm-target dispatch paths: circuit
// breaker, delegation policy, and spawn limit. Commit 3 (May 2026,
// Gap C) introduces this helper so the swarm-target branch
// (tryDispatchSwarmTarget) cannot bypass the gates the agent-target
// branch enforces — pre-commit-3 the swarm branch fired before
// prepareExecution, so a coordinator could route a swarm-id delegate
// while the breaker was open or the budget was exhausted.
//
// Rejection-tracker is NOT in the shared set: it keys on the resolved
// target's chain id, which is only available after resolveTargetWithOptions
// (agent-target path) or after sub-swarm registry lookup (swarm-target
// path uses its own chain prefix derived from the manifest). Keep that
// gate inside prepareExecution where it has the chain id to assert
// against.
//
// Expected:
//   - input may carry a handoff with depth metadata used by
//     checkSpawnLimits.
//
// Returns:
//   - errCircuitBreakerOpen / errDelegationNotAllowed / a spawn-limit
//     error when any gate refuses the call.
//   - A parse error if the handoff is malformed (so the swarm-target
//     branch surfaces the same parse failure shape as the agent-target
//     branch would).
//   - nil when all gates allow the call to proceed.
//
// Side effects:
//   - circuitBreaker.Allow() is a read; no breaker-state mutation here.
//   - checkSpawnLimits is a read against d.backgroundManager.ActiveCount().
func (d *DelegateTool) preFlightSharedGates(input tool.Input) error {
	if !d.circuitBreaker.Allow() {
		return errCircuitBreakerOpen
	}
	if !d.delegation.CanDelegate {
		return errDelegationNotAllowed
	}
	// The handoff parse is needed only for depth extraction. Mirror
	// parseDelegationParams's narrow handoff-only path: skip the full
	// params parse so swarm-target callers (which deliberately don't
	// parse handoff fields, per tryDispatchSwarmTarget's documented
	// contract) don't surface a routing-required error from this helper.
	var handoff *delegation.Handoff
	if raw, ok := input.Arguments["handoff"]; ok && raw != nil {
		h, parseErr := d.parseHandoff(raw)
		if parseErr != nil {
			return fmt.Errorf("parsing handoff: %w", parseErr)
		}
		handoff = h
	}
	return d.checkSpawnLimits(handoff)
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

// PostMemberGateMaxAttempts bounds how many times a member is dispatched
// when its post-member gate keeps failing for missing output. The first
// attempt plus up to (PostMemberGateMaxAttempts-1) re-delegations. Kept
// small relative to the wave-fan-in harness's retry floor of 8 because a
// single member with a clear re-write directive should self-correct fast;
// a larger budget only delays an honest fail when the member genuinely
// cannot write. Deterministic + bounded — there is no unbounded loop.
const PostMemberGateMaxAttempts = 3
