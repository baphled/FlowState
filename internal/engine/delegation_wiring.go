package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/turn"
)

// WithEventBus installs the shared `*eventbus.EventBus` the engine uses for
// delegation lifecycle publication. Production wiring (App.configureDelegateTool)
// passes `eng.EventBus()` so the bus is the same instance the API SSE handler
// and TUI chat intent subscribe to.
//
// Expected:
//   - bus may be nil to disable bus publication (the historical no-op
//     behaviour for callers that pre-date the bus-bridge plan).
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously installed bus.
func (d *DelegateTool) WithEventBus(bus *eventbus.EventBus) *DelegateTool {
	d.eventBus = bus
	return d
}

// WithTurnRegistry installs the dispatcher's shared `*turn.Registry`
// the engine uses to mint per-child Turn lifecycles around
// delegate-spawned child sessions. Production wiring
// (App.configureDelegateTool) passes `dispatcher.TurnRegistry()` so the
// API server's `handleListV1Sessions` projection sees the same
// `byActiveSession[childID] = childTurnID` entry the child's
// long-poll endpoint reads from — the missing link in
// Plans/Child Session Turn Registry Plumbing (May 2026).
//
// Expected:
//   - reg may be nil to disable child Turn registration (the historical
//     no-op behaviour for callers that pre-date the plumbing plan,
//     including the dozens of NewDelegateTool / NewDelegateToolWithBackground
//     test callsites that do not need the live channel). Default-nil
//     with nil-checks at each lifecycle site keeps backward
//     compatibility per D7.
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously installed registry.
func (d *DelegateTool) WithTurnRegistry(reg *turn.Registry) *DelegateTool {
	if reg == nil {
		d.turnRegistry = nil
		return d
	}
	d.turnRegistry = reg
	return d
}

// withChildTurnRegistry installs a custom childTurnRegistry
// implementation for spec-side spying — used by PR2a's §S4.2 spec to
// verify the Fail-call-count invariant. Test seam only; production
// callers MUST use WithTurnRegistry which preserves the typed
// *turn.Registry signature.
func (d *DelegateTool) withChildTurnRegistry(reg childTurnRegistry) *DelegateTool {
	d.turnRegistry = reg
	return d
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

// WithPlanOutputDir installs the resolved plan_output_dir the post-swarm
// deterministic publisher writes the planning loop's plan to. Production
// wiring (App.configureDelegateTool) passes the same directory the
// artifact-published honesty gate bounds its containment check by, so the
// published file always lands inside the dir the gate verifies.
//
// Expected:
//   - dir may be empty to disable the deterministic publish (the gate's
//     plan-key check still applies; the loop then keeps the historical
//     no-vault-write behaviour).
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously stored plan output dir.
func (d *DelegateTool) WithPlanOutputDir(dir string) *DelegateTool {
	d.planOutputDir = dir
	return d
}

// WithTeeChildContent controls whether delegate chain-of-thought text is
// mirrored into the parent's user-visible content stream. When false (the
// default), child output stays in the child session and surfaces to the
// parent only via the delegation tool_result — matching the consensus
// pattern across Claude Code, OpenCode, and other harnesses.
//
// Expected:
//   - enabled is the value from AppConfig.Delegation.TeeChildContent.
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Replaces the previously stored value.
func (d *DelegateTool) WithTeeChildContent(enabled bool) *DelegateTool {
	d.teeChildContent = enabled
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

// TurnRegistry returns the production *turn.Registry installed via
// WithTurnRegistry (or nil when no registry / a spec-side spy has been
// installed). Exposed so the App-level wiring test (Plans/Child Session
// Turn Registry Plumbing (May 2026) §S8.1) can verify that the
// App-constructed DelegateTool and the api.Server's Dispatcher share
// the SAME *turn.Registry instance pointer — production wiring at
// configureDelegateTool calls dt.WithTurnRegistry(a.API.TurnRegistry())
// so a divergent registry on either side surfaces as a nil or distinct
// pointer here.
//
// Mirrors GateRunner's pattern: a thin accessor for cross-package
// wiring tests; production code never reads this field directly — the
// dispatch path consults d.turnRegistry internally.
//
// The type assertion `d.turnRegistry.(*turn.Registry)` returns nil
// when the field was installed via the spec seam (withChildTurnRegistry)
// with a non-*turn.Registry spy — that branch is deliberately
// out-of-scope for this accessor (S8.1 verifies production wiring,
// which always installs the concrete pointer).
//
// Returns:
//   - The installed *turn.Registry, or nil when no registry is wired
//     or the field carries a spec-side spy.
//
// Side effects:
//   - None.
func (d *DelegateTool) TurnRegistry() *turn.Registry {
	if d.turnRegistry == nil {
		return nil
	}
	reg, _ := d.turnRegistry.(*turn.Registry)
	return reg
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
//
// injectPreamble prepends preamble to message, separated by a blank line.
// When message is empty the preamble is returned as-is.
func injectPreamble(preamble, message string) string {
	if message == "" {
		return preamble
	}
	return preamble + "\n\n" + message
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
		preamble += " Write your findings to coordination_store key=" + chainID + "/" + suffix + "." +
			" Perform the coordination_store write — do not narrate your reasoning, analysis, or process; just write the output under that key." +
			" If the key is empty or predecessor data is unavailable, proceed with your available context — do not wait, retry, or loop."
	}
	if message == "" {
		return preamble
	}
	return preamble + "\n\n" + message
}

// buildMemberSwarmPreamble derives a rich membership contract from the active
// swarm manifest's gate definitions. When a swarm is running, every member
// has exactly one post-member builtin:result-schema gate that names the
// output_key and schema it must write. This function builds a preamble that
// tells the member its chainID, the exact coord-store key it must write, and
// the schema it must conform to — so the member's system prompt need carry
// none of that swarm-specific boilerplate.
//
// Expected:
//   - agentID is the target member's manifest ID.
//   - chainID is the authoritative chain identifier for this swarm run.
//
// Returns:
//   - A formatted preamble string ready to prepend to the delegation message.
//   - "" when no swarm is active, the member has no post-member gate, or the
//     gate lacks both output_key and schema_ref (nothing to inject).
//
// Side effects:
//   - None.
func (d *DelegateTool) buildMemberSwarmPreamble(agentID, chainID string) string {
	if chainID == "" {
		return ""
	}
	swarmCtx, ok := d.activeSwarmContext()
	if !ok {
		return ""
	}
	manifest := d.manifestForSwarm(swarmCtx.SwarmID)
	if manifest == nil {
		return ""
	}

	chainPrefix := swarmCtx.ChainPrefix
	if chainPrefix == "" {
		chainPrefix = swarmCtx.SwarmID
	}

	// Find the primary post-member result-schema gate for this member.
	// That gate names output_key and schema_ref — the two fields that
	// constitute the member's output contract.
	var outputKey, schemaRef string
	for _, g := range manifest.Harness.Gates {
		if g.When == swarm.LifecyclePostMember && g.Target == agentID && g.Kind == "builtin:result-schema" {
			outputKey = g.OutputKey
			schemaRef = g.SchemaRef
			break
		}
	}
	if outputKey == "" && schemaRef == "" {
		// Member has no schema gate — fall back to basic chain injection.
		return ""
	}

	var b strings.Builder
	b.WriteString("chainID=" + chainID + ".")
	b.WriteString(" You are a member of the **" + swarmCtx.SwarmID + "** swarm.")

	if outputKey != "" {
		// When the manifest's output_key carries the "{chainID}"
		// template, the member writes to the resolved "<chainID>/<suffix>"
		// key directly (no <chainPrefix>/<agentID> segment). The gate
		// resolves the identical key, so the preamble MUST advertise the
		// substituted form — otherwise the member would write
		// "<chainID>/<suffix>" while the preamble told it to write
		// "<chainPrefix>/<agentID>/{chainID}/<suffix>", reopening the
		// divergence this fix closes. Non-templated keys keep the legacy
		// "<chainPrefix>/<agentID>/<output_key>" shape.
		var fullKey string
		if strings.Contains(outputKey, "{chainID}") {
			fullKey = strings.ReplaceAll(outputKey, "{chainID}", chainID)
		} else {
			fullKey = chainPrefix + "/" + agentID + "/" + outputKey
		}
		b.WriteString(" Write your result to coordination_store key=**" + fullKey + "**.")
		b.WriteString(" Perform the write — do not narrate your process, analysis, or reasoning; just write the output. If predecessor data is unavailable under the expected keys, proceed with what you have — do not loop waiting for keys.")
	}
	if schemaRef != "" {
		b.WriteString(memberOutputContract(schemaRef))
	}

	return b.String()
}

// memberOutputContract returns the output-format clause for a member's swarm
// preamble, scoped to what the member's post-member gate ACTUALLY validates.
//
// The planning-loop gates no longer JSON-schema-validate members whose output
// is consumed as raw text by the next LLM member (see
// internal/swarm/gate_result_schema.go). Asking those members for "valid JSON
// only" while the gate accepts prose was the lockstep half of the
// false-failure: the model spent effort emitting brittle JSON the gate didn't
// need and the next member couldn't read as naturally as prose. The wording is
// therefore scoped on the SAME schema signal the gate keys on:
//
//   - evidence-bundle-v1 / external-refs-v1 / analysis-bundle-v1 (prose
//     bundles): clear, complete written findings/analysis (prose or Markdown
//     fine) — the gate checks presence + non-emptiness, not a JSON struct.
//   - plan-document-v1: a Markdown plan document — the gate routes through the
//     publisher's render predicate, which accepts raw Markdown.
//   - review-verdict-v1: an explicit VERDICT line (one of the recognised
//     verdict tokens) plus rationale — the gate (and the approve/reject loop)
//     keys on the verdict TOKEN, not a JSON enum.
//   - any OTHER schema (section-v1, code-review-verdict-v1, …): unchanged —
//     these ARE typed-parsed by their swarm's publisher, so "valid JSON only"
//     still holds. This is what keeps the wording change from forcing prose on
//     swarms whose gates still need JSON.
//
// Expected:
//   - schemaRef is the gate's non-empty SchemaRef.
//
// Returns:
//   - A leading-space-prefixed clause to append to the preamble builder.
//
// Side effects:
//   - None.
func memberOutputContract(schemaRef string) string {
	switch schemaRef {
	case swarm.EvidenceBundleV1Name, swarm.ExternalRefsV1Name, swarm.AnalysisBundleV1Name:
		return " Write your findings as clear, complete prose or Markdown under that key —" +
			" a downstream agent reads them as text, so do NOT wrap them in JSON;" +
			" just make sure the key is non-empty and substantive." +
			" Do not narrate your process — write only the findings."
	case swarm.PlanDocumentV1Name:
		return " Write a complete Markdown plan document under that key" +
			" (e.g. starting with a `# ` heading) — prose/Markdown, not a JSON blob." +
			" Do not narrate your planning process — write only the plan document."
	case swarm.ReviewVerdictV1Name:
		return " Write your review under that key with an explicit verdict line —" +
			" `VERDICT: " + strings.Join(coordination.RecognisedVerdictTokens, " | ") + "`" +
			" — followed by your rationale. The loop keys on the verdict token, so it MUST be present."
	default:
		return " Your output MUST conform to the **" + schemaRef + "** JSON schema." +
			" Produce only valid JSON matching that schema — no markdown fences, no extra keys."
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

// SetCoordinationStore updates the coordination store the tool reads
// through when its post-member gates check for member output.
//
// The store is otherwise set only in the constructor
// (NewDelegateToolWithBackground). On a manifest-switch rebind, the App
// re-wires the members with a freshly-built store; without this setter the
// tool would keep reading its original store while the members write to the
// new one, so the gate reports "no member output found" even though the
// member wrote correctly. App.wireDelegateToolIfEnabled calls this on the
// rebind path; the singleton in App.sharedCoordinationStore makes the
// instance identical in practice, and this setter is the belt-and-braces
// guard that keeps the gate-read store in lockstep regardless.
//
// Expected:
//   - store is the coordination store the members are wired through. May be
//     nil; the rejection tracker is left untouched in that case.
//
// Side effects:
//   - Replaces the internal coordinationStore used during gate evaluation.
func (d *DelegateTool) SetCoordinationStore(store coordination.Store) {
	d.coordinationStore = store
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
// The comparison is case-insensitive: ResolveByNameOrAlias returns the
// canonical lowercase manifest.ID, but swarm rosters in YAML
// (dev-swarm, board-room, planning-loop, engineer-swarm) frequently
// list members in PascalCase. The case-insensitive contract is
// documented explicitly at internal/app/swarms/a-team.yml:14-16
// ("Members resolve via the agent registry's case-insensitive
// name+alias lookup") — pre-PR7 the resolve leg was case-insensitive
// (via Registry.GetByNameOrAlias) but the membership-check leg was
// byte-exact, breaking the dev-swarm path captured in session
// 148ad4a2-9b52-4eed-a652-ed3f402538f9.
//
// Expected:
//   - allowlist is a slice of agent ID strings (may be empty).
//   - agentID is the resolved agent identifier to search for.
//
// Returns:
//   - true if agentID matches any element in allowlist under
//     strings.EqualFold (Unicode-aware case folding, the same
//     comparator Registry.GetByNameOrAlias uses).
//
// Side effects:
//   - None.
func containsAgent(allowlist []string, agentID string) bool {
	for _, id := range allowlist {
		if strings.EqualFold(id, agentID) {
			return true
		}
	}
	return false
}
