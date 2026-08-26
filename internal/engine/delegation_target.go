package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

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

// applySkillsAndSessionMode injects requested skills into the target engine's
// system prompt and configures agent-file loading based on session presence.
//
// Expected:
//   - targetEngine is a non-nil Engine.
//   - params contains the delegation parameters.
//
// Side effects:
//   - Mutates targetEngine's manifest and agent-file loading flag.
//
// Returns: result of applySkillsAndSessionMode.
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

	// Swarm-context shadowing (May 2026): when a swarm is in flight, the
	// roster on swarm.Context shadows the lead's static
	// delegation.allowlist outright — it does not union with it. This
	// finally enforces the contract documented at engine.go:461-463 and
	// swarm/context.go:32-34 that the prompt block (engine.go:2086-2123)
	// already relied on.
	//
	// Rationale for narrowing (not unioning): an agent can be a member of
	// multiple swarms; unioning the static allowlist with each swarm's
	// roster would grow the effective permissions monotonically across
	// memberships. Keeping the two concepts separate (static gates
	// standalone delegation, swarm Members[] gates in-swarm delegation)
	// preserves containment.
	//
	// Empty Members[] inside a swarm is a real zero — we do NOT fall
	// through to the static allowlist, because that would re-introduce
	// the bug for swarm configs that legitimately have an empty roster
	// (broken config) or list only the lead (no delegation targets at
	// all). The standalone path (no active swarm context) is unchanged.
	// Permissive-orchestrator bypass (May 2026).
	//
	// When the lead agent's manifest declares `delegation.scope:
	// permissive`, the gate accepts any target that resolves to a
	// known agent OR a known swarm in the registries — regardless of
	// the active swarm.Context.Members[] or DelegationAllowlist. This
	// is the "permissive first, restrictive last" principle: top-level
	// orchestrators (coordinator, Team-Lead) need to reach across the
	// full agent graph, while leaf agents (Senior-Engineer,
	// KB-Curator, etc.) stay scoped by the default restrictive
	// behaviour above.
	//
	// Genuine unknowns are still rejected, but with a clarified
	// message that names the permissive scope so the user sees
	// "target not found in registries" rather than "scope-restricted".
	if d.delegation.IsPermissive() {
		if d.isKnownDelegationTarget(targetAgentID) {
			// Known target — admit. Skip Members[] and allowlist
			// checks entirely.
		} else {
			swarmCtx, _ := d.activeSwarmContextForCtx(ctx)
			return delegationTarget{}, fmt.Errorf("%w: %s",
				errAgentNotInAllowlist,
				d.formatPermissiveRejection(swarmCtx, targetAgentID))
		}
	} else if swarmCtx, ok := d.activeSwarmContextForCtx(ctx); ok {
		if !containsAgent(swarmCtx.Members, targetAgentID) {
			return delegationTarget{}, fmt.Errorf("%w: %s",
				errAgentNotInAllowlist,
				d.formatRejection(swarmCtx, swarmCtx.Members, targetAgentID))
		}
	} else if len(d.delegation.DelegationAllowlist) > 0 && !containsAgent(d.delegation.DelegationAllowlist, targetAgentID) {
		return delegationTarget{}, fmt.Errorf("%w: %s",
			errAgentNotInAllowlist,
			d.formatRejection(nil, d.delegation.DelegationAllowlist, targetAgentID))
	}

	if err := checkDelegationCycle(d.sourceAgentID, targetAgentID, params.handoff); err != nil {
		return delegationTarget{}, err
	}

	callerChainID := params.chainID
	if callerChainID == "" && params.handoff != nil {
		callerChainID = params.handoff.ChainID
	}
	chainID, chainIDFromCaller := d.resolveMemberChainID(ctx, callerChainID)

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
		loadSkills:        params.loadSkills,
	}, nil
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

// lookupEngineByID returns the engine registered under agentID, falling
// back to a case-insensitive scan when the byte-exact key misses. The
// engines map is keyed by `agentManifest.ID` (canonical case, see
// internal/app/app.go:buildDelegateMaps), while swarm rosters in YAML
// frequently list members with case variants (e.g. `Tech-Lead` in the
// manifest, `tech-Lead` from a delegate-target invocation). The first
// lookup hop (in-swarm gate at resolveTargetWithOptions) is already
// case-folded via containsAgent; the second hop (this lookup, used by
// buildMemberRunner + resolveSubSwarm) needs to mirror that contract
// or a delegate-target dispatch surfaces `no engine for swarm member
// "tech-Lead"` despite the member being a known agent.
//
// Forensic anchor: session 7dfdb197-ce21-45a2-b5da-f2fa62dd293b at
// 17:57:05.214 — the orchestrator delegated to dev-swarm with
// member `tech-Lead`; the in-swarm gate accepted under
// containsAgent's EqualFold; this map lookup missed and the
// dispatcher surfaced the "no engine" error.
//
// Expected:
//   - agentID is the (possibly case-variant) target id.
//
// Returns:
//   - The canonical (engines-map-keyed) id, the matching engine, and
//     true when a match exists under any case.
//   - The original agentID, nil, and false otherwise — preserves the
//     existing sentinel-error shape so callers' error strings keep
//     using the id the caller actually typed.
//
// Side effects:
//   - None (read-only access to d.engines).
func (d *DelegateTool) lookupEngineByID(agentID string) (string, *Engine, bool) {
	if eng, ok := d.engines[agentID]; ok && eng != nil {
		return agentID, eng, true
	}
	for id, eng := range d.engines {
		if eng == nil {
			continue
		}
		if strings.EqualFold(id, agentID) {
			return id, eng, true
		}
	}
	return agentID, nil, false
}
