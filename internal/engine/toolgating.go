// Package engine — tool-set gating and tool schema assembly.
//
// This file implements the allowed-tool-set gating that decides which tools a
// manifest (including MCP server tools and swarm lead caps) exposes for a
// turn, plus assembly of the provider-facing tool schemas.
package engine

import (
	"context"
	"sort"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/permissionmode"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// BuildSystemPrompt constructs the system prompt from the engine's
// active agent manifest and skills. It is a convenience wrapper for
// BuildSystemPromptCtx that uses a background context (no per-stream
// binding) — call sites that route through Stream() should use
// BuildSystemPromptCtx and pass the stream's ctx so the manifest
// snapshot taken at Stream() entry is honoured.
//
// The composition order is: base prompt → agent files → delegation sections → prompt_append (last).
// Returns a cached result when the prompt inputs have not changed since the last build.
// The cache is invalidated by SetManifest and SetAgentOverrides.
//
// Returns:
//   - The concatenated system prompt string including permanently-active and agent-level skill content.
//
// Side effects:
//   - Caches the built prompt and loaded agent files for subsequent calls
//     when no per-context manifest binding is active.

// buildAllowedToolSetFor returns the set of tool names allowed by
// the supplied manifest. The ctx-bound build path calls this so
// each concurrent stream's tool schemas are derived from its own
// manifest's Capabilities, not from whatever happens to live on
// the engine's shared manifest field at the moment buildToolSchemas
// fires.
//
// Expected:
//   - manifest is the manifest whose Capabilities drive tool
//     filtering.
//   - e.mcpServerTools maps server names to their available tool
//     names.
//
// Returns:
//   - A non-nil map of allowed tool names. D1 (Agent Runtime Quality,
//     May 2026): every manifest implicitly inherits the
//     agent.DefaultBaseTools() floor (todowrite, todo_update,
//     skill_load), unioned with the manifest's declared tools, then
//     subtracted by manifest.Capabilities.ToolsDeny. Empty/nil
//     Capabilities.Tools no longer fails closed — agents that did
//     not declare these three tools previously stuck on the universal
//     surfaces; the base set restores the floor. The bundle aliases
//     "file" / "delegate" / "autoresearch_run" expand into individual
//     tool names — see below. Commit 3 (May 2026, Gap B): the
//     `delegate` bundle was narrowed to lifecycle tools only
//     (delegate + background_output + background_cancel). Pre-commit-3
//     it silently included autoresearch_run + autoresearch_prune,
//     which let a coordinator declaring `[delegate]` do its own
//     background research instead of delegating a researcher member.
//     Agents that need autoresearch_* must now declare it explicitly.
//   - MCP tools are gated by Capabilities.MCPServers: each declared
//     server name has its tools merged into the allowed set. Unknown
//     server names are silently ignored. See ADR - MCP Tool Gating
//     by Agent Manifest for the full contract.
//
// buildAllowedToolSetFor computes the allowed tool set for the given manifest.
//
// Side effects:
//   - None.
func (e *Engine) buildAllowedToolSetFor(manifest agent.Manifest) map[string]bool {
	return BuildAllowedToolSet(manifest, e.mcpServerTools)
}

// BuildAllowedToolSet computes the set of tool names an agent manifest is
// permitted to invoke. It is the single source of truth for tool gating
// and is shared by two call sites:
//
//   - The engine's runtime gate at executeToolCall and the schema
//     advertisement path (via effectiveAllowedToolsForCtx) — gates
//     dispatch and what reaches the provider request.
//   - The app's delegate-engine registry construction (see
//     buildToolsForManifestWithStore) — filters which concrete tool
//     implementations the delegate engine's e.tools slice carries, so
//     out-of-manifest tools are not just hidden from the model but
//     absent from the registry entirely. Without this shared seam the
//     two layers would drift: a permissive provider that emits an
//     out-of-schema tool call could still reach a registered Execute
//     body if the registry held it; with the seam shared, that path is
//     closed at construction time and the runtime gate becomes a
//     defence-in-depth for the primary engine's shared-slice case.
//
// Computation:
//
//   - effective := manifest.EffectiveTools()
//     (union(Capabilities.Tools, DefaultBaseTools) − Capabilities.ToolsDeny)
//   - bundle expansion: "file" → {read, write}; "delegate" → {delegate,
//     background_output, background_cancel}; "autoresearch_run" →
//     {autoresearch_run, background_output, background_cancel};
//     "autoresearch_prune" → {autoresearch_prune}.
//   - MCP server gating: each declared Capabilities.MCPServers entry
//     contributes its tool names from mcpServerTools. Unknown server
//     names are silently ignored.
//   - ToolsDeny is re-applied over the expanded set so deny entries
//     take effect on bundle-expanded names (e.g. tools=[delegate],
//     tools_deny=[background_cancel] yields the delegate fan-out minus
//     background_cancel).
//   - suggest_delegate is force-added as the read-only escape hatch
//     (P12). The concrete tool is only registered on CanDelegate=false
//     engines, so the flag is a no-op when the tool is absent.
//
// Commit 3 (May 2026, Gap B) — the `delegate` bundle was narrowed to
// lifecycle tools only (delegate + background_output +
// background_cancel). Pre-commit-3 it also silently expanded into
// autoresearch_run + autoresearch_prune, letting a coordinator
// declaring `tools: [delegate]` do its own background research instead
// of delegating a researcher member. Agents that need autoresearch_*
// now declare it explicitly.
//
// Expected:
//   - manifest is the manifest whose Capabilities drive the
//     computation. A zero-value manifest yields DefaultBaseTools plus
//     suggest_delegate.
//   - mcpServerTools maps MCP server names to their available tool
//     names. nil is treated as "no MCP gating contributions".
//
// Returns:
//   - A non-nil map of allowed tool names. Invariably contains
//     suggest_delegate. Never contains entries denied by ToolsDeny.
//
// Side effects:
//   - None; pure computation.
func BuildAllowedToolSet(manifest agent.Manifest, mcpServerTools map[string][]string) map[string]bool {
	effective := manifest.EffectiveTools()
	allowed := make(map[string]bool, len(effective)+1)
	for _, mt := range effective {
		switch mt {
		case "file":
			// Tool-Scoped Permissions plan (Slice C): the `file` bundle
			// expands to every filesystem-mutating tool that pathguard
			// now gates. Pre-Slice C the bundle was {read, write}, which
			// meant a manifest declaring `tools: [file]` silently lost
			// access to edit/multiedit/apply_patch even though the
			// pathguard *ForTool routes (wired in Slice B) treat all
			// five symmetrically. Keeping the bundle narrow would force
			// every operator to enumerate the extras by hand and would
			// leave the per-tool allow/deny rules in permissions.yaml
			// unreachable from the most common manifest shape.
			allowed["read"] = true
			allowed["write"] = true
			allowed["edit"] = true
			allowed["multiedit"] = true
			allowed["apply_patch"] = true
		case "delegate":
			allowed["delegate"] = true
			allowed["background_output"] = true
			allowed["background_cancel"] = true
		case "autoresearch_run":
			allowed["autoresearch_run"] = true
			allowed["background_output"] = true
			allowed["background_cancel"] = true
		case "autoresearch_prune":
			allowed["autoresearch_prune"] = true
		default:
			allowed[mt] = true
		}
	}

	for _, serverName := range manifest.Capabilities.MCPServers {
		for _, toolName := range mcpServerTools[serverName] {
			allowed[toolName] = true
		}
	}

	// D1: re-apply ToolsDeny over the expanded set so deny entries
	// take effect even on bundle-alias-expanded tool names (e.g. a
	// manifest that lists `tools: [delegate]` and `tools_deny: [bash]`
	// still gets the delegate fan-out but never sees bash).
	for _, denied := range manifest.Capabilities.ToolsDeny {
		delete(allowed, denied)
	}

	// Harness guarantee: skill_load is permanently available to every agent,
	// even when explicitly listed in ToolsDeny. Skills are foundational
	// to agent operation (permanently-active skills discipline, memory,
	// pre-action, etc.) and must never be excludable.
	allowed["skill_load"] = true

	// Blocking question tool (Aug 2026): the question tool is a
	// harness-level interaction primitive like skill_load — every
	// agent can ask the operator a clarifying question without
	// declaring it per-manifest. The concrete tool is only
	// registered on app-wired engines (AppendQuestionTool), so on
	// test/NewForTest engines without a question registry the flag is
	// a harmless no-op. Not subject to ToolsDeny: agents cannot be
	// silenced from asking clarifying questions, mirroring the
	// permission-request ask flow.
	allowed["question"] = true

	// P12: suggest_delegate is a read-only escape hatch. The
	// corresponding tool is only attached to the engine for
	// CanDelegate=false agents, so this flag is a no-op when the tool
	// is absent. Not subject to ToolsDeny on purpose — escape hatches
	// do not honour denials.
	allowed["suggest_delegate"] = true

	return allowed
}

// mcpServerForTool returns the MCP server name that exposes toolName,
// or "" when no server in mcpServerTools owns it (the tool is a
// regular non-MCP tool, or the server isn't wired). The lookup is the
// inverse of BuildAllowedToolSet's expansion at engine.go:2558-2562 —
// where BuildAllowedToolSet projects (serverName → tools) into the
// flat allowed map, this projects (toolName → serverName) for the
// Slice 5 prompter routing. O(N+M) over (servers, tools) is acceptable
// because mcpServerTools is small (single-digit servers, low-double
// digits tools each) and the call site fires only on a denied tool
// dispatch under ModeAskUser.
//
// Permission Mode ModeAskUser Extension plan (May 2026) Slice 5.
//
// Expected: parameters for mcpServerForTool.
// Returns: result of mcpServerForTool.
// Side effects: None.
func mcpServerForTool(mcpServerTools map[string][]string, toolName string) string {
	for serverName, names := range mcpServerTools {
		for _, name := range names {
			if name == toolName {
				return serverName
			}
		}
	}
	return ""
}

// effectiveAllowedToolsForCtx returns the allowed-tool set for the
// manifest bound to ctx via WithBoundManifest, falling back to the
// engine's active manifest when no binding is present. It is the
// shared seam used by both the schema-advertisement gate
// (assembleToolSchemasLocked, buildToolSchemasCtx) and the PR7
// runtime tool gate at executeToolCall — sharing the routine
// (rather than duplicating the per-manifest expansion logic at the
// dispatch site) prevents the two surfaces from drifting and keeps
// the contract "what the LLM sees" == "what the dispatch path will
// run" as a single source of truth.
//
// Permission Modes Slice 4 follow-up: the Plan-mode mutating-tool
// filter applies HERE rather than at the schema-build call site so
// both surfaces (schema advertisement and runtime gate) consult an
// identically filtered allowed-set. Pre-fix the filter lived inside
// assembleToolSchemasLocked, which left the runtime gate mode-blind:
// glm-4.6 hallucinated a `write` tool_use outside the advertised
// schema and the dispatch path executed it because
// buildAllowedToolSetFor was the only consultation. Routing the Plan-
// mode subtraction through this seam restores the documented "what
// the LLM sees" == "what the dispatch path will run" invariant.
//
// Expected:
//   - ctx is a valid context that may carry a boundManifestKey value
//     and/or a permission-mode value (via WithPermissionMode).
//
// Returns:
//   - A non-nil map of allowed tool names. See buildAllowedToolSetFor
//     for the base set-construction contract (D1 inherit-by-default
//     base toolset, bundle alias expansion, MCP server gating,
//     ToolsDeny subtraction, suggest_delegate escape hatch). When
//     permissionmode.FromContext(ctx) == ModePlan the returned map is
//     a copy with every tool name in permissionmode.PlanModeStrippedTools
//     removed. The strict subset (currently {bash}) is the only set of
//     tools removed at the schema layer; the remaining four mutating
//     tools (write/edit/multiedit/apply_patch) are kept in the schema
//     so pathguard can path-scope them to the operator's
//     plan_output_dir at runtime (Plan-Mode Output Directory plan).
//
// Side effects:
//   - None.
func (e *Engine) effectiveAllowedToolsForCtx(ctx context.Context) map[string]bool {
	// Read-locks without exception. Pre-Orchestrator-Self-Execution (May 2026) the
	// bound path skipped the lock because the seam read only ctx state
	// and buildAllowedToolSetFor was pure over its inputs. The swarm-
	// lead tool cap in effectiveAllowedToolsForCtxLocked now reads
	// e.swarmContext, so even the bound path must hold the read lock to
	// snapshot it without racing SetSwarmContext. The schema-build
	// surface (assembleToolSchemasLocked) already holds the write lock
	// when it calls the Locked body directly, so this RLock applies only
	// to the runtime-gate surface, which never holds e.mu on entry.
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.effectiveAllowedToolsForCtxLocked(ctx)
}

// effectiveAllowedToolsForCtxLocked is the lock-free body of
// effectiveAllowedToolsForCtx. Callers that already hold e.mu (read
// or write) invoke this directly to avoid the RWMutex non-reentrancy
// trap — sync.RWMutex does not permit recursive RLock from the same
// goroutine when a writer is waiting (it can deadlock against itself),
// and the unbound path in buildToolSchemasCtx holds the write lock
// when it calls into the schema-build seam. Routing both surfaces
// through this Locked variant keeps the Plan-mode subtraction at the
// shared seam without re-entering the engine mutex.
//
// Expected:
//   - e.mu is held by the caller (read or write) when ctx has no
//     bound manifest. Bound-manifest ctx reads only ctx state and
//     does not need the engine lock; callers may invoke this with
//     no lock in that case.
//   - ctx carries the manifest binding and/or permission mode.
//
// Returns:
//   - Same contract as effectiveAllowedToolsForCtx.
//
// Side effects:
//   - None.
func (e *Engine) effectiveAllowedToolsForCtxLocked(ctx context.Context) map[string]bool {
	var allowed map[string]bool
	var turnManifestID string
	if bound, ok := manifestFromContext(ctx); ok {
		allowed = e.buildAllowedToolSetFor(bound)
		turnManifestID = bound.ID
	} else {
		allowed = e.buildAllowedToolSetFor(e.manifest)
		turnManifestID = e.manifest.ID
	}

	// Orchestrator Self-Execution (May 2026) — defence-in-depth tool
	// cap. When this turn is a swarm LEAD turn (the engine carries a
	// swarm context whose LeadAgent matches the turn's manifest — the
	// same discriminator used at appendSwarmLeadSectionFor), the
	// effective toolset is intersected with the lead manifest's OWN
	// declared tools. This is the structural guarantee that a swarm-
	// lead turn can never surface execution tools it did not declare,
	// even if a future caller fails to bind the lead manifest into ctx
	// (Part 1 binds it on the auto-dispatch path; this cap holds the
	// invariant regardless). Applied here so BOTH the schema-
	// advertisement surface (assembleToolSchemasLocked) and the runtime
	// gate (executeToolCall) inherit it from the single seam. The cap
	// reads e.swarmContext / e.agentRegistry under the caller's lock —
	// see effectiveAllowedToolsForCtx, which now RLocks even the bound
	// path for this read.
	allowed = e.capToolsetAtSwarmLeadLocked(ctx, allowed, turnManifestID)

	if override := session.ToolsAllowlistOverrideFromContext(ctx); len(override) > 0 {
		overrideSet := make(map[string]bool, len(override))
		for _, name := range override {
			overrideSet[name] = true
		}
		for name := range allowed {
			if !overrideSet[name] {
				delete(allowed, name)
			}
		}
	}

	if permissionmode.FromContext(ctx) != permissionmode.ModePlan {
		return allowed
	}

	// Plan-mode: copy-on-write subtract PlanModeStrippedTools. The map
	// returned by buildAllowedToolSetFor is a freshly composed value
	// (see BuildAllowedToolSet) but treating it as caller-owned would
	// invite future drift if that contract changes; copy defensively
	// so the Plan path can never poison a sibling Default caller
	// against the same manifest.
	//
	// Plan-Mode Output Directory plan (May 2026) §3 Slice 1: the
	// strip set is now PlanModeStrippedTools (= {bash}) rather than
	// the full MutatingTools set. The four file-writers
	// (write/edit/multiedit/apply_patch) remain in the schema and are
	// path-scoped at the pathguard layer to the operator's
	// plan_output_dir. bash has no path-scoping option (a shell can
	// invoke anything) so it stays fully filtered.
	filtered := make(map[string]bool, len(allowed))
	for name, ok := range allowed {
		if ok && !permissionmode.IsStrippedUnderPlan(name) {
			filtered[name] = true
		}
	}
	return filtered
}

// capToolsetAtSwarmLeadLocked intersects the supplied allowed-tool set
// with the swarm lead manifest's own declared tools when the current
// turn is a swarm-LEAD turn — the Orchestrator Self-Execution (May 2026)
// defence-in-depth invariant. The returned set can never exceed
// BuildAllowedToolSet of the lead's manifest for a lead turn, so an
// orchestrator turn physically cannot surface execution tools the lead
// did not declare — even when a leaky session-default manifest is bound
// into ctx (the root of the auto-dispatch self-execution bug, where the
// bound manifest is default-assistant, NOT the lead). Member turns and
// standalone (no-swarm) turns are returned unchanged.
//
// Discriminator. The swarm context is read from the per-turn ctx scope
// FIRST (swarm.ScopeFromContext) and only falls back to the engine field
// e.swarmContext when no scope was attached. This matters because the
// dispatcher attaches the scope to streamCtx on EVERY dispatch path
// (dispatcher.go:765 swarm.WithScope(streamCtx, swarmCtx)) but only calls
// SetSwarmContext inside its `if swarmActive` block. The swarm-id entry
// path (agent_id == a SWARM ID) attaches a non-nil scope to ctx but the
// pre-fix code never reached SetSwarmContext, so e.swarmContext stayed
// nil and a cap keyed solely on the field returned `allowed` unchanged —
// bash leaked. Keying on the ctx scope makes the cap fire on EVERY swarm
// entry point. When the ctx scope is present, it is authoritative
// (scoped=true): a nil scope means "this turn is standalone" and the cap
// must NOT fire; the engine field is consulted only when no scope was
// attached (the legacy / direct-SetSwarmContext test path).
//
// The lead's own engine is the ONLY engine that ever carries a swarm
// context: dispatch.SetSwarmContext is called exclusively on the shared
// dispatchEngine (the lead engine), while members run on their own
// per-agent delegate engines (d.engines[memberID]) whose swarmContext is
// nil. We nonetheless ALSO exclude turns whose manifest is a declared
// swarm member as belt-and-braces: if a future wiring change ever runs a
// member turn on a swarm-context-bearing engine, that member must keep
// its legitimate execution tools. The net rule — swarm active AND turn
// manifest is not a member ⇒ cap at lead — holds the invariant whether or
// not the lead manifest was correctly bound into ctx, which is precisely
// the case the literal LeadAgent==boundID discriminator misses (the leak
// binds default-assistant, so LeadAgent != boundID and the cap would
// never fire).
//
// Manifest intersection (not a static bash/read/write denylist) is used
// deliberately: a denylist would silently miss any future execution tool
// added to the registry, whereas intersecting against the lead's
// declared set is closed by construction — only what the lead explicitly
// declares survives.
//
// Expected:
//   - ctx may carry a per-turn swarm scope (swarm.WithScope). When it
//     does (scoped=true) that scope is authoritative; otherwise the cap
//     falls back to the engine field e.swarmContext.
//   - allowed is the freshly composed allowed-tool set for the turn.
//   - turnManifestID is the ID of the manifest driving the turn (bound
//     via ctx, else e.manifest.ID).
//   - The caller holds e.mu (read or write) — the fallback path reads
//     e.swarmContext. e.agentRegistry is set once at construction and
//     never mutated.
//
// Returns:
//   - allowed unchanged for member / no-swarm turns; otherwise the
//     intersection of allowed with the lead manifest's BuildAllowedToolSet.
//     When the lead manifest cannot be resolved from the registry the
//     set is returned unchanged (Part 1's ctx binding remains the
//     primary enforcement; this cap is best-effort defence-in-depth and
//     must not fail closed on an unresolvable lead, which would break
//     legitimate lead turns whose manifest simply isn't registered).
//
// Side effects:
//   - None.
func (e *Engine) capToolsetAtSwarmLeadLocked(ctx context.Context, allowed map[string]bool, turnManifestID string) map[string]bool {
	// Prefer the per-turn ctx scope (attached by the dispatcher on EVERY
	// path) over the engine field. When a scope is attached it is
	// authoritative even when nil — nil means "standalone turn, do not
	// cap". Only when NO scope was attached (legacy / direct
	// SetSwarmContext callers) do we read e.swarmContext.
	swarmCtx := e.swarmContext
	if scoped, present := swarm.ScopeFromContext(ctx); present {
		swarmCtx = scoped
	}
	if swarmCtx == nil || swarmCtx.LeadAgent == "" {
		return allowed
	}
	// A turn whose manifest is a declared member keeps its own tools —
	// members legitimately execute. Only the lead (non-member turn on
	// the swarm-context-bearing engine) is capped.
	for _, member := range swarmCtx.Members {
		if member == turnManifestID {
			return allowed
		}
	}
	if e.agentRegistry == nil {
		return allowed
	}
	leadManifest, ok := e.agentRegistry.Get(swarmCtx.LeadAgent)
	if !ok || leadManifest == nil {
		return allowed
	}
	leadAllowed := e.buildAllowedToolSetFor(*leadManifest)
	capped := make(map[string]bool, len(allowed))
	for name, on := range allowed {
		if on && leadAllowed[name] {
			capped[name] = true
		}
	}
	return capped
}

// sortedKeys returns the keys of a string-keyed set in
// alphabetical order. Used by the PR7 runtime tool gate to render
// the agent's effective toolset in a stable form so the rejection
// body is deterministic across runs (matters for test assertions
// and for the model's pattern-matching on retry — a random ordering
// per call invites confusion).
//
// Expected:
//   - set is a non-nil map (nil-safe — empty slice returned).
//
// Returns:
//   - The keys of set, sorted with sort.Strings.
//
// Side effects:
//   - None.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
// Convenience wrapper for buildToolSchemasCtx using a background
// context (no per-stream binding). Stream() and the retry path
// pass the stream's ctx so concurrent callers each get their own
// manifest's tool envelope.
//
// Returns:
//   - A slice of provider.Tool values with schema information for each tool.
//   - Returns a cached result when tools have not changed since the last call
//     and no per-context manifest binding is active.
//
// Side effects:
//   - Caches the built schemas for subsequent calls when no
//     per-context binding is active.
//
// Expected: parameters for buildToolSchemas.
func (e *Engine) buildToolSchemas() []provider.Tool {
	return e.buildToolSchemasCtx(context.Background())
}

// buildToolSchemasCtx is the manifest-binding-aware variant of
// buildToolSchemas. When ctx carries a bound manifest, schemas
// are filtered against that manifest's Capabilities and the engine
// cache is skipped — concurrent streams pinned to different
// manifests cannot share or invalidate each other's cached
// schemas.
//
// Expected:
//   - ctx is a valid context; nil is treated as unbound.
//
// Returns:
//   - The provider tool schemas for the ctx-bound manifest when
//     present, otherwise for the engine's active manifest.
//
// Side effects:
//   - Updates the engine's tool-schema cache only on the unbound
//     path.
func (e *Engine) buildToolSchemasCtx(ctx context.Context) []provider.Tool {
	mode := permissionmode.FromContext(ctx)

	if _, ok := manifestFromContext(ctx); ok {
		e.mu.RLock()
		defer e.mu.RUnlock()
		return e.assembleToolSchemasLocked(ctx)
	}

	// Plan-mode skips the cache. The cache stores the unfiltered
	// schema keyed on the engine's active manifest; serving a Plan
	// session from cache would either (a) poison the cache for a
	// concurrent Default session if we cached the filtered result,
	// or (b) leak mutating tools to the Plan session if we served
	// the unfiltered cached entry. Plan turns are rare enough that
	// rebuilding on every call is the safe trade.
	if mode == permissionmode.ModePlan {
		e.mu.RLock()
		defer e.mu.RUnlock()
		return e.assembleToolSchemasLocked(ctx)
	}

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

	tools := e.assembleToolSchemasLocked(ctx)
	e.cachedToolSchemas = tools
	return tools
}

// assembleToolSchemasLocked is the pure tool-schema composition
// routine shared by the engine-state and ctx-bound build paths.
// Caller must hold e.mu (read lock for the ctx-bound path is
// sufficient because no engine state is mutated; the unbound path
// already holds the write lock for cache update).
//
// Permission Modes Slice 4 follow-up: this routine no longer carries
// a Plan-mode parameter. The Plan-mode mutating-tool subtraction was
// hoisted into effectiveAllowedToolsForCtx so the schema-build path
// and the runtime tool gate at executeToolCall consult the same
// filtered allowed-set — restoring the "what the LLM sees" == "what
// the dispatch path will run" invariant. The mode is still read from
// ctx by the caller (buildToolSchemasCtx) to drive the cache skip.
//
// Expected:
//   - e.mu is held (read or write).
//   - ctx carries the manifest binding (manifestFromContext) and the
//     permission mode (permissionmode.FromContext). The seam dispatches
//     on both.
//
// Returns:
//   - The composed provider tool schemas slice.
//
// Side effects:
//   - None.
func (e *Engine) assembleToolSchemasLocked(ctx context.Context) []provider.Tool {
	allowedSet := e.effectiveAllowedToolsForCtxLocked(ctx)

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
	return tools
}

// ToolSchemas returns the current tool schemas filtered by the active manifest.
//
// Returns:
//   - A slice of provider.Tool representing the tools available under the current manifest.
//
// Side effects:
//   - May cache the schemas internally for subsequent calls.
//
// Expected: parameters for ToolSchemas.
func (e *Engine) ToolSchemas() []provider.Tool {
	return e.buildToolSchemas()
}

// ToolSchemasCtx returns the tool schemas filtered against the
// manifest bound to ctx (via WithBoundManifest), or against the
// engine's active manifest when no binding is present. Callers
// inside Stream/retry paths use this to honour the per-stream
// manifest snapshot.
//
// Expected:
//   - ctx is a valid context; nil is treated as unbound.
//
// Returns:
//   - The provider tool schemas for the resolved manifest.
//
// Side effects:
//   - Same as buildToolSchemasCtx.
func (e *Engine) ToolSchemasCtx(ctx context.Context) []provider.Tool {
	return e.buildToolSchemasCtx(ctx)
}
