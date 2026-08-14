package engine

import (
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
)

// checkDelegationCandidates returns an error when the target engine has
// preferences configured but all candidates are currently unavailable.
// This prevents a delegation attempt certain to fail and surfaces a
// diagnostic before the stream is opened.
//
// Expected: parameters for checkDelegationCandidates.
// Returns: result of checkDelegationCandidates.
// Side effects: None.
func checkDelegationCandidates(eng *Engine) error {
	_, _, err := delegationCandidatesStatus(eng)
	return err
}

// delegationCandidatesStatus ...
//
// Expected: parameters for delegationCandidatesStatus.
//
// Returns: result of delegationCandidatesStatus.
//
// Side effects: None.
func delegationCandidatesStatus(eng *Engine) ([]provider.ModelPreference, []provider.ModelPreference, error) {
	if eng == nil {
		return nil, nil, nil
	}
	mgr := eng.FailoverManager()
	if mgr == nil {
		return nil, nil, nil
	}
	prefs := mgr.Preferences()
	candidates := mgr.Candidates()
	if len(prefs) > 0 && len(candidates) == 0 {
		return prefs, candidates, errors.New("no available model candidates: all preferred providers are rate-limited or unavailable")
	}
	return prefs, candidates, nil
}

// promoteHealthyDelegationCandidate ...
//
// Expected: parameters for promoteHealthyDelegationCandidate.
//
// Side effects: None.
func promoteHealthyDelegationCandidate(eng *Engine) {
	if eng == nil {
		return
	}
	_, candidates, err := delegationCandidatesStatus(eng)
	if err != nil || len(candidates) == 0 {
		return
	}
	promoteDelegationCandidateIfNeeded(eng, candidates[0])
}

// promoteDelegationCandidateIfNeeded ...
//
// Expected: parameters for promoteDelegationCandidateIfNeeded.
//
// Side effects: None.
func promoteDelegationCandidateIfNeeded(eng *Engine, candidate provider.ModelPreference) {
	if eng == nil {
		return
	}
	currentProvider := eng.LastProvider()
	currentModel := eng.LastModel()
	if currentProvider == candidate.Provider && currentModel == candidate.Model {
		return
	}
	if currentProvider == "" || currentModel == "" || delegationCurrentProviderUnavailable(eng, currentProvider, currentModel) {
		eng.SetModelPreference(candidate.Provider, candidate.Model)
	}
}

// delegationCurrentProviderUnavailable ...
//
// Expected: parameters for delegationCurrentProviderUnavailable.
//
// Returns: result of delegationCurrentProviderUnavailable.
//
// Side effects: None.
func delegationCurrentProviderUnavailable(eng *Engine, providerName, modelName string) bool {
	if eng == nil {
		return false
	}
	mgr := eng.FailoverManager()
	if mgr == nil || mgr.Health() == nil {
		return false
	}
	return mgr.Health().IsRateLimited(providerName, modelName)
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

// resolveChildModelOverride resolves the (provider, model) override pair
// that a fresh delegate / swarm-member session should carry. Implements
// the manifest tier of the cascade UI > manifest > global for child
// sessions — the UI tier is intentionally NOT inherited from the parent
// (the parent's ProviderOverrideKey / ModelOverrideKey are not consulted
// here; the call sites override them with this helper's result so the
// parent's choice cannot leak into the child).
//
// Resolution order:
//  1. target.resolvedProvider / resolvedModel — set by category routing
//     when the parent's delegate call carried a category argument. This
//     is an explicit per-call selection and outranks the manifest.
//  2. The target agent's manifest PreferredModels[0] — the agent's
//     declared default pairing.
//  3. Empty strings — falls through to the engine's global default
//     (LastProvider / LastModel, which the failover manager populates).
//
// Empty returns are explicit "no override" and map to the engine.go
// override gate at engine.go:3005-3009 short-circuiting back to the
// pre-existing request.Provider/Model.
//
// Expected:
//   - target.agentID is non-empty when the caller wants manifest tier
//     to apply; an empty agentID short-circuits to empty returns.
//   - d.registry may be nil (legacy test surfaces wired without an
//     agent registry); the helper tolerates this and returns empty.
//
// Returns:
//   - The (provider, model) pair to stamp on the child ctx, or empty
//     strings to fall through to the engine's global default.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveChildModelOverride(target delegationTarget) (string, string) {
	// Tier 1: category routing already resolved an explicit pair.
	if target.resolvedProvider != "" || target.resolvedModel != "" {
		return target.resolvedProvider, target.resolvedModel
	}
	// Tier 2: target manifest's PreferredModels[0]. Registry-less
	// surfaces and unknown agent IDs both fall through to tier 3.
	if d.registry == nil || target.agentID == "" {
		return "", ""
	}
	manifest, ok := d.registry.Get(target.agentID)
	if !ok || manifest == nil || len(manifest.PreferredModels) == 0 {
		return "", ""
	}
	first := manifest.PreferredModels[0]
	return first.Provider, first.Model
}

// resolveChildModelChain returns the target agent's full ordered
// preferred_models chain as a []provider.ModelPreference, for the
// failover layer to attempt tier-by-tier before falling back to the
// global config default. This is the chain companion to
// resolveChildModelOverride (which yields only tier-0): the override
// answers "what should the child attempt first" while the chain answers
// "what are ALL the agent's fallbacks, in order, before the global
// default".
//
// Resolution mirrors resolveChildModelOverride:
//   - Tier 1 (category routing) takes precedence for the HEAD of the
//     chain, but is no longer a single-element replacement. The
//     category-routed pair is prepended to the agent manifest's
//     PreferredModels list (deduped) so the failover layer retains the
//     manifest tiers as fallback when the category pair fails or is
//     rate-limited. Replacing the chain with only the category pair
//     left the failover layer with no fallback candidates, which
//     combined with promotePinned re-inserting the rate-limited pair
//     produced an infinite retry loop ("agent looping and bailing").
//   - Otherwise the agent manifest's PreferredModels list is returned
//     verbatim (the engine's provider.ModelPreference mirrors the
//     manifest's field shape).
//   - Empty in every other case (registry-less surface, unknown agent,
//     no preferred_models) so session.WithPreferredModels no-ops and
//     the failover layer keeps the prior cascade-to-global-default
//     behaviour.
//
// Side effects: none.
//
// Expected: parameters for resolveChildModelChain.
// Returns: result of resolveChildModelChain.
func (d *DelegateTool) resolveChildModelChain(target delegationTarget) []provider.ModelPreference {
	manifestChain := d.fetchManifestChain(target)
	if target.resolvedProvider == "" && target.resolvedModel == "" {
		return manifestChain
	}
	categoryPair := provider.ModelPreference{Provider: target.resolvedProvider, Model: target.resolvedModel}
	if len(manifestChain) == 0 {
		return []provider.ModelPreference{categoryPair}
	}
	chain := make([]provider.ModelPreference, 0, len(manifestChain)+1)
	chain = append(chain, categoryPair)
	for _, pref := range manifestChain {
		if pref.Provider == categoryPair.Provider && pref.Model == categoryPair.Model {
			continue
		}
		chain = append(chain, pref)
	}
	return chain
}

// fetchManifestChain returns the agent manifest's PreferredModels list
// as a []provider.ModelPreference, or nil when the registry is absent,
// the agent ID is empty, or the manifest declares no preferred_models.
//
// Expected:
//   - d.registry may be nil (legacy test surfaces); the helper tolerates
//     this and returns nil.
//   - target.agentID may be empty (no manifest tier to apply); returns nil.
//
// Returns:
//   - A fresh slice mirroring the manifest's PreferredModels, or nil.
//
// Side effects: none.
func (d *DelegateTool) fetchManifestChain(target delegationTarget) []provider.ModelPreference {
	if d.registry == nil || target.agentID == "" {
		return nil
	}
	manifest, ok := d.registry.Get(target.agentID)
	if !ok || manifest == nil || len(manifest.PreferredModels) == 0 {
		return nil
	}
	chain := make([]provider.ModelPreference, 0, len(manifest.PreferredModels))
	for _, pref := range manifest.PreferredModels {
		chain = append(chain, provider.ModelPreference{Provider: pref.Provider, Model: pref.Model})
	}
	return chain
}

// correctiveRetryModel returns the (provider, model) the post-member gate
// corrective retry should route a struggling swarm member onto, alongside
// the forced tool_choice.
//
// Primary source — the MEMBER's own preferred_models chain HEAD
// (resolveChildModelChain(target)[0]): the most-capable tier the member
// declares. The corrective retry ESCALATES the struggling member onto its
// own capable tier, not the lead's current model.
//
// Why NOT the lead's pair (the previous behaviour): the lead's resolved
// (provider, model) is only "reliable" when the lead itself is on a capable
// model. When anthropic + openai are unavailable, the LEAD fails over onto
// the weak global default (zai/glm) too — so copying the lead's pair routed
// the member glm → glm, a NO-OP. A member that STALLS on glm (narrates the
// write, emits no tool call) was then re-rolled on glm and stalled every
// attempt. Targeting the member's own capable HEAD instead means the retry
// attempts the member's best tier first; the engine's failover manager then
// cascades down the member's chain to a reachable tier if the head is down —
// so "only the weak model reachable" degrades to the prior behaviour with no
// regression, while a reachable capable tier is genuinely re-attempted.
//
// Why the chain HEAD and not "the highest tier strictly above the just-failed
// model": the just-failed pair is not cleanly determinable at the retry point
// — the member's engine LastProvider/LastModel report the post-failover
// LANDING (e.g. glm) rather than a clean chain entry, and the capable head was
// never "unreliable", merely unreachable (a transient condition worth
// re-attempting). Stamping the head and letting failover skip an unreachable
// head is the simpler, evidence-backed choice with identical end behaviour.
//
// Fallback — when the member declares NO preferred_models (empty/nil chain,
// or a registry-less surface), there is no capable tier to escalate onto, so
// the retry keeps the prior behaviour and routes onto the swarm LEAD's
// already-resolved pair (d.ownerEngine.LastProvider / LastModel). This
// preserves the override rather than losing it entirely (which would leave
// the member on its stalled tier).
//
// Returns empty strings only when BOTH the member chain is empty AND no lead
// engine is wired (non-swarm delegate, or a legacy test surface without
// WithOwnerEngine) — the caller treats empty as "no override", leaving the
// member's manifest-tier override in place so the retry still forces the
// tool, just without re-routing the model.
//
// Side effects: none.
//
// Expected: parameters for correctiveRetryModel.
// Returns: result of correctiveRetryModel.
func (d *DelegateTool) correctiveRetryModel(target delegationTarget) (string, string) {
	// Primary: escalate onto the member's capable preferred-tier head.
	if chain := d.resolveChildModelChain(target); len(chain) > 0 {
		head := chain[0]
		if head.Provider != "" || head.Model != "" {
			return head.Provider, head.Model
		}
	}
	// Fallback: the lead's already-resolved pair (prior behaviour).
	if d.ownerEngine == nil {
		return "", ""
	}
	return d.ownerEngine.LastProvider(), d.ownerEngine.LastModel()
}

// isKnownDelegationTarget reports whether targetID resolves to a known
// agent OR a known swarm via the wired-in registries. This is the
// "exists anywhere" lookup the permissive-orchestrator gate uses to
// distinguish a typo (genuine miss) from a member-of-some-swarm hit.
//
// Lookup order:
//
//  1. Agent registry — match by id, case-insensitive id, or alias via
//     Registry.GetByNameOrAlias. Mirrors the resolution path used by
//     resolveAgentID and tryDispatchSwarmTarget so permissive admit
//     is consistent with what the dispatch path actually accepts.
//  2. Swarm registry — match by exact id via Registry.Get. Swarm ids
//     are case-sensitive in the registry, so this lookup mirrors the
//     dispatch-side check at tryDispatchSwarmTarget.
//
// Returns false when both registries are nil or no match is found.
//
// Expected:
//   - targetID is the candidate agent or swarm id; non-empty.
//
// Returns:
//   - true when the id is registered in either registry.
//   - false otherwise.
//
// Side effects:
//   - None (read-only access via Registry.GetByNameOrAlias / Get).
func (d *DelegateTool) isKnownDelegationTarget(targetID string) bool {
	if targetID == "" {
		return false
	}
	if d.registry != nil {
		if _, found := d.registry.GetByNameOrAlias(targetID); found {
			return true
		}
	}
	if d.swarmRegistry != nil {
		if _, found := d.swarmRegistry.Get(targetID); found {
			return true
		}
	}
	return false
}
