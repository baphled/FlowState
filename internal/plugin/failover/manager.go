package failover

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

var errNoModelsAvailable = errors.New("no models available from any provider")

// defaultManagerFallback mirrors context.DefaultModelContextFallback
// without importing the context package (failover sits below context in
// the dependency graph). Keep the literal in sync; the unit test in
// manager_test.go pins both packages to the same value.
const defaultManagerFallback = 16384

// Manager is the central component for provider selection, preference management,
// and state tracking during failover. It replaces the provider.FailbackChain for
// determining which provider/model pairs to attempt, preserving base preferences
// as fallback even when a user override is active.
//
// All methods are safe for concurrent use.
type Manager struct {
	mu              sync.RWMutex
	registry        *provider.Registry
	health          *HealthManager
	basePreferences []provider.ModelPreference
	override        *provider.ModelPreference
	timeout         time.Duration
	lastProvider    string
	lastModel       string
	modelTiers      map[string]string
	attempts        map[ProviderModel]attemptRecord
	// capabilityFilter, when non-nil, reports whether a (provider, model)
	// pair is tool-capable enough to be an auto-failover target. It is
	// injected from the layer above (app/engine wires the engine's
	// IsToolCapableModel bound to cfg.ToolCapableModels /
	// cfg.ToolIncapableModels) so the failover package keeps no dependency
	// on the engine's capability tables — failover sits below engine in
	// the dependency graph and importing it would form a cycle. nil means
	// "no capability filtering" (the legacy behaviour), so existing
	// callers and tests that never wire a filter are unaffected.
	capabilityFilter func(providerName, model string) bool
	// contextFallback is the token cap returned when the registered
	// provider/model lookup fails. Defaults to defaultManagerFallback
	// (16K). App.New overrides this from cfg.SystemPromptBudget so
	// operators with hardware that warrants a different cap can pin
	// the fallback per-deployment.
	contextFallback int
}

// SetContextFallback overrides the token cap ResolveContextLength
// returns when the provider/model lookup fails. Zero or negative inputs
// are ignored so callers may pass an unset config field without
// guarding the call site.
//
// Expected:
//   - limit is the new fallback token cap; values <= 0 leave the
//     existing fallback untouched.
//
// Side effects:
//   - Mutates the receiver under its write lock.
//
// Returns: result of SetContextFallback.
func (m *Manager) SetContextFallback(limit int) {
	if limit <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextFallback = limit
}

// SetCapabilityFilter installs a predicate that reports whether a
// (provider, model) pair is tool-capable enough to be an AUTO-FAILOVER
// target. When set, base preferences that fail the predicate are dropped
// from the candidate list so a transient failure on a capable model does
// not cascade onto a model too weak to drive a swarm (e.g. a local
// llama3.2 that malforms the delegate tool call).
//
// Scope and guards (see healthyCandidates):
//   - Only base preferences are filtered. An explicit user/manifest
//     override is the caller's deliberate head choice and is never
//     dropped — running a solo agent on ollama must still work.
//   - If filtering would leave ZERO base candidates, the unfiltered base
//     list is returned instead. A last-resort weak model beats a hard
//     nil ("no healthy providers available").
//   - A nil filter (the default) disables filtering entirely, preserving
//     the legacy behaviour for callers that never wire one.
//
// Expected:
//   - filter reports true for pairs that should remain failover targets;
//     may be nil to disable filtering.
//
// Side effects:
//   - Mutates the receiver under its write lock.
//
// Returns: result of SetCapabilityFilter.
func (m *Manager) SetCapabilityFilter(filter func(providerName, model string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.capabilityFilter = filter
}

// ResolveContextLength returns the context length for a given provider/model.
//
// Expected:
//   - providerName is the name of the provider to query.
//   - model is the model identifier to look up.
//
// Returns:
//   - The context length for the given model when the provider knows it.
//   - The configured fallback (defaultManagerFallback by default;
//     override via SetContextFallback) when the provider is missing,
//     errors, or does not advertise a positive ContextLength for the
//     model.
//
// Side effects:
//   - None.
func (m *Manager) ResolveContextLength(providerName, model string) int {
	fallback := m.resolvedFallback()
	p, err := m.registry.Get(providerName)
	if err != nil {
		return fallback
	}
	models, err := p.Models()
	if err != nil {
		return fallback
	}
	for _, candidate := range models {
		if candidate.ID == model && candidate.ContextLength > 0 {
			return candidate.ContextLength
		}
	}
	return fallback
}

// ResolveOutputLimit returns the per-model output token budget for the
// given provider/model. Mirrors ResolveContextLength in shape so the
// engine's overflow gate and context_usage emitter can consult both via
// the same registry round-trip. Used to tighten the Phase-2 reserve
// formula from `max(req.MaxTokens or 4096, 1024)` to
// `max(req.MaxTokens or model.OutputLimit, 1024)`.
//
// Expected:
//   - providerName is the name of the provider to query.
//   - model is the model identifier to look up.
//
// Returns:
//   - The model's OutputLimit when the provider knows the pair AND
//     advertises a positive OutputLimit for it.
//   - Zero when the provider is missing, errors, or returns a model
//     entry with OutputLimit unset. The engine treats zero as "no
//     registry data" and falls back to its defaultOutputReserve.
//
// Side effects:
//   - None.
func (m *Manager) ResolveOutputLimit(providerName, model string) int {
	p, err := m.registry.Get(providerName)
	if err != nil {
		return 0
	}
	models, err := p.Models()
	if err != nil {
		return 0
	}
	for _, candidate := range models {
		if candidate.ID == model && candidate.OutputLimit > 0 {
			return candidate.OutputLimit
		}
	}
	return 0
}

// resolvedFallback returns the operator-configured fallback when set,
// or defaultManagerFallback otherwise. Lock-aware so callers stay
// thread-safe alongside SetContextFallback.
//
// Side effects:
//   - None.
//
// Expected: parameters for resolvedFallback.
// Returns: result of resolvedFallback.
func (m *Manager) resolvedFallback() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.contextFallback > 0 {
		return m.contextFallback
	}
	return defaultManagerFallback
}

// NewManager creates a new Manager with the given registry, health manager, and stream timeout.
//
// Expected:
//   - registry is a valid, non-nil Registry.
//   - health is a valid, non-nil HealthManager.
//   - timeout is a positive duration for per-attempt streaming.
//
// Returns:
//   - A pointer to an initialised Manager with empty preferences.
//
// Side effects:
//   - None.
func NewManager(registry *provider.Registry, health *HealthManager, timeout time.Duration) *Manager {
	return &Manager{
		registry:        registry,
		health:          health,
		timeout:         timeout,
		contextFallback: defaultManagerFallback,
	}
}

// SetBasePreferences replaces the base preferences from an agent manifest.
//
// Expected:
//   - prefs is a slice of ModelPreference values in priority order.
//
// Side effects:
//   - Replaces the current base preferences (thread-safe).
//
// Returns: result of SetBasePreferences.
func (m *Manager) SetBasePreferences(prefs []provider.ModelPreference) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.basePreferences = prefs
}

// SetModelTiers replaces the tier lookup used to keep equivalent candidates
// grouped without rotating across tier boundaries.
//
// Expected: parameters for SetModelTiers.
// Returns: result of SetModelTiers.
// Side effects: None.
func (m *Manager) SetModelTiers(tiers map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(tiers) == 0 {
		m.modelTiers = nil
		return
	}
	cloned := make(map[string]string, len(tiers))
	for key, value := range tiers {
		cloned[key] = value
	}
	m.modelTiers = cloned
}

// SetOverride sets a user override as the first candidate. The override is prepended
// to base preferences so that base preferences are preserved as fallback.
//
// Expected:
//   - pref is a valid ModelPreference chosen by the user.
//
// Side effects:
//   - Replaces any existing override (thread-safe).
//
// Returns: result of SetOverride.
func (m *Manager) SetOverride(pref provider.ModelPreference) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.override = &pref
}

// ClearOverride removes the user override, restoring base preferences only.
//
// Side effects:
//   - Clears the override field (thread-safe).
//
// Expected: parameters for ClearOverride.
// Returns: result of ClearOverride.
func (m *Manager) ClearOverride() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.override = nil
}

// Preferences returns the effective preferences: override (if set) prepended to base preferences.
//
// Returns:
//   - A slice of ModelPreference values in priority order.
//
// Side effects:
//   - None.
//
// Expected: parameters for Preferences.
func (m *Manager) Preferences() []provider.ModelPreference {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.effectivePreferences()
}

// Candidates returns effective preferences filtered by health. Provider/model pairs
// that are currently rate-limited are skipped, preserving the original order.
//
// Returns:
//   - A slice of healthy ModelPreference values in priority order.
//   - An empty slice if all candidates are rate-limited or no preferences are set.
//
// Side effects:
//   - None.
//
// Expected: parameters for Candidates.
func (m *Manager) Candidates() []provider.ModelPreference {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.healthyCandidates()
}

// LastProvider returns the name of the last successfully used provider.
//
// Returns:
//   - The provider name, or empty string if no provider has been used.
//
// Side effects:
//   - None.
//
// Expected: parameters for LastProvider.
func (m *Manager) LastProvider() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastProvider
}

// LastModel returns the model name of the last successfully used model.
//
// Returns:
//   - The model name, or empty string if no model has been used.
//
// Side effects:
//   - None.
//
// Expected: parameters for LastModel.
func (m *Manager) LastModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastModel
}

// SetLast updates the last-used provider and model state. Called by the streaming
// hook after a successful provider response.
//
// Expected:
//   - providerName is a non-empty string.
//   - model is a non-empty string.
//
// Side effects:
//   - Updates last-used state (thread-safe).
//
// Returns: result of SetLast.
func (m *Manager) SetLast(providerName, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordAttemptLocked(providerName, model)
	m.lastProvider = providerName
	m.lastModel = model
}

// RecordAttempt records an attempted provider/model pair so equivalent
// candidates can rotate by least-recently-used order.
//
// Expected: parameters for RecordAttempt.
// Returns: result of RecordAttempt.
// Side effects: None.
func (m *Manager) RecordAttempt(providerName, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordAttemptLocked(providerName, model)
}

// ListModels returns all available models from all providers in the registry.
//
// Returns:
//   - A slice of all available models from all providers.
//   - An error if no models are available.
//
// Side effects:
//   - May make network calls to providers to fetch model lists.
//
// Expected: parameters for ListModels.
func (m *Manager) ListModels() ([]provider.Model, error) {
	var allModels []provider.Model
	for _, providerName := range m.registry.List() {
		p, err := m.registry.Get(providerName)
		if err != nil {
			continue
		}
		models, err := p.Models()
		if err != nil {
			continue
		}
		allModels = append(allModels, models...)
	}
	if len(allModels) == 0 {
		return nil, errNoModelsAvailable
	}
	return allModels, nil
}

// StreamTimeout returns the configured timeout for per-attempt streaming.
//
// Returns:
//   - The timeout duration.
//
// Side effects:
//   - None.
//
// Expected: parameters for StreamTimeout.
func (m *Manager) StreamTimeout() time.Duration {
	return m.timeout
}

// Health returns the HealthManager used for rate-limit tracking.
//
// Returns:
//   - The HealthManager instance.
//
// Side effects:
//   - None.
//
// Expected: parameters for Health.
func (m *Manager) Health() *HealthManager {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.health
}

// effectivePreferences composes the effective preference list from override and base preferences.
//
// Expected: called under at least an RLock on m.mu.
// Returns: override prepended to base preferences if set, otherwise base preferences alone.
// Side effects: none.
func (m *Manager) effectivePreferences() []provider.ModelPreference {
	if m.override == nil {
		return m.basePreferences
	}
	result := make([]provider.ModelPreference, 0, 1+len(m.basePreferences))
	result = append(result, *m.override)
	result = append(result, m.basePreferences...)
	return result
}

// healthyCandidates returns effective preferences filtered by capability
// (base tail only) and then by health state.
//
// Filtering order and rationale:
//  1. Capability-filter the BASE preferences via capabilityFilteredBase
//     so a tool-incapable model (e.g. ollama/llama3.2) is dropped from
//     the auto-failover tail. The explicit override is NOT subject to
//     this filter — it is the caller's deliberate head choice.
//  2. Compose override (if set) ahead of the capability-filtered base.
//  3. Health-filter the composed list, dropping rate-limited pairs while
//     preserving order.
//
// Expected: called under at least an RLock on m.mu.
// Returns: capability- then health-filtered preferences, preserving order.
// Side effects: none.
func (m *Manager) healthyCandidates() []provider.ModelPreference {
	return rankCandidatesByHealth(m.health, m.modelTiers, m.attempts, m.capabilityFilteredPreferences())
}

// rankCandidates ...
//
// Expected: parameters for rankCandidates.
//
// Returns: result of rankCandidates.
//
// Side effects: None.
func (m *Manager) rankCandidates(candidates []provider.ModelPreference) []provider.ModelPreference {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return rankCandidatesByHealth(m.health, m.modelTiers, m.attempts, candidates)
}

// capabilityFilteredPreferences composes the effective preference list
// with the capability filter applied to the BASE tail only. The override
// (the caller's explicit head choice) is always preserved unfiltered and
// prepended.
//
// Empty-guard: when the capability filter would remove every base
// candidate, the unfiltered base list is used instead — a last-resort
// weak model beats a hard nil. This mirrors the prependAgentChain
// empty-guard in stream_hook.go.
//
// Expected: called under at least an RLock on m.mu.
// Returns: override (if any) prepended to the capability-filtered base.
// Side effects: none.
func (m *Manager) capabilityFilteredPreferences() []provider.ModelPreference {
	base := m.capabilityFilteredBase()
	if m.override == nil {
		return base
	}
	result := make([]provider.ModelPreference, 0, 1+len(base))
	result = append(result, *m.override)
	result = append(result, base...)
	return result
}

// capabilityFilteredBase returns the base preferences with tool-incapable
// pairs removed. When no filter is installed (the legacy default) the base
// is returned untouched. When filtering would empty the list, the
// unfiltered base is returned so the caller never strands on a hard nil.
//
// Expected: called under at least an RLock on m.mu.
// Returns: the base preferences, capability-filtered with empty-guard.
// Side effects: none.
func (m *Manager) capabilityFilteredBase() []provider.ModelPreference {
	if m.capabilityFilter == nil || len(m.basePreferences) == 0 {
		return m.basePreferences
	}
	filtered := make([]provider.ModelPreference, 0, len(m.basePreferences))
	for _, pref := range m.basePreferences {
		if m.capabilityFilter(pref.Provider, pref.Model) {
			filtered = append(filtered, pref)
		}
	}
	if len(filtered) == 0 {
		// Every base candidate is tool-incapable. Better a last-resort
		// weak model than nil — fall back to the unfiltered base.
		return m.basePreferences
	}
	return filtered
}

// rankCandidatesByHealth ...
//
// Expected: parameters for rankCandidatesByHealth.
//
// Returns: result of rankCandidatesByHealth.
//
// Side effects: None.
func rankCandidatesByHealth(
	health *HealthManager,
	tiers map[string]string,
	attempts map[ProviderModel]attemptRecord,
	candidates []provider.ModelPreference,
) []provider.ModelPreference {
	if health == nil || len(candidates) == 0 {
		return rankCandidatesWithTiers(nil, tiers, attempts, candidates)
	}

	return rankCandidatesWithTiers(health, tiers, attempts, candidates)
}

// attemptRecord tracks the last attempt timestamp for a candidate.
type attemptRecord struct {
	lastAttemptAt time.Time
}

// rankedCandidate pairs a provider model preference with its failover rank score.
type rankedCandidate struct {
	candidate provider.ModelPreference
	score     uint64
	tier      string
	index     int
	attempt   attemptRecord
}

// recordAttemptLocked ...
//
// Expected: parameters for recordAttemptLocked.
//
// Returns: result of recordAttemptLocked.
//
// Side effects: None.
func (m *Manager) recordAttemptLocked(providerName, model string) {
	if m.attempts == nil {
		m.attempts = make(map[ProviderModel]attemptRecord)
	}
	m.attempts[ProviderModel{Provider: providerName, Model: model}] = attemptRecord{
		lastAttemptAt: time.Now(),
	}
}

// rankCandidatesWithTiers ...
//
// Expected: parameters for rankCandidatesWithTiers.
//
// Returns: result of rankCandidatesWithTiers.
//
// Side effects: None.
func rankCandidatesWithTiers(
	health *HealthManager,
	tiers map[string]string,
	attempts map[ProviderModel]attemptRecord,
	candidates []provider.ModelPreference,
) []provider.ModelPreference {
	if len(candidates) == 0 {
		return nil
	}
	now := time.Now()
	ranked := make([]rankedCandidate, 0, len(candidates))
	for i, candidate := range candidates {
		if health != nil && health.IsRateLimited(candidate.Provider, candidate.Model) {
			continue
		}
		score := uint64(0)
		if health != nil {
			score = health.HealthScore(candidate.Provider, candidate.Model, now)
		}
		ranked = append(ranked, rankedCandidate{
			candidate: candidate,
			score:     score,
			tier:      equivalentTierForCandidate(tiers, candidate),
			index:     i,
			attempt:   attempts[ProviderModel{Provider: candidate.Provider, Model: candidate.Model}],
		})
	}
	if len(ranked) < 2 {
		return rankedCandidatesToPreferences(ranked)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].score < ranked[j].score
	})
	ordered := make([]provider.ModelPreference, 0, len(ranked))
	for start := 0; start < len(ranked); {
		end := start + 1
		for end < len(ranked) && ranked[end].score == ranked[start].score {
			end++
		}
		ordered = append(ordered, orderEquivalentCandidates(ranked[start:end])...)
		start = end
	}
	return ordered
}

// rankedCandidatesToPreferences ...
//
// Expected: parameters for rankedCandidatesToPreferences.
//
// Returns: result of rankedCandidatesToPreferences.
//
// Side effects: None.
func rankedCandidatesToPreferences(candidates []rankedCandidate) []provider.ModelPreference {
	result := make([]provider.ModelPreference, len(candidates))
	for i, candidate := range candidates {
		result[i] = candidate.candidate
	}
	return result
}

// orderEquivalentCandidates ...
//
// Expected: parameters for orderEquivalentCandidates.
//
// Returns: result of orderEquivalentCandidates.
//
// Side effects: None.
func orderEquivalentCandidates(candidates []rankedCandidate) []provider.ModelPreference {
	if len(candidates) < 2 {
		return rankedCandidatesToPreferences(candidates)
	}
	groups := groupEquivalentCandidates(candidates)
	ordered := make([]provider.ModelPreference, 0, len(candidates))
	for _, group := range groups {
		sortEquivalentGroup(group)
		for _, candidate := range group {
			ordered = append(ordered, candidate.candidate)
		}
	}
	return ordered
}

// groupEquivalentCandidates ...
//
// Expected: parameters for groupEquivalentCandidates.
//
// Returns: result of groupEquivalentCandidates.
//
// Side effects: None.
func groupEquivalentCandidates(candidates []rankedCandidate) [][]rankedCandidate {
	groups := make([][]rankedCandidate, 0, len(candidates))
	groupIndexes := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		if idx, ok := groupIndexes[candidate.tier]; ok {
			groups[idx] = append(groups[idx], candidate)
			continue
		}
		groupIndexes[candidate.tier] = len(groups)
		groups = append(groups, []rankedCandidate{candidate})
	}
	return groups
}

// sortEquivalentGroup ...
//
// Expected: parameters for sortEquivalentGroup.
//
// Side effects: None.
func sortEquivalentGroup(candidates []rankedCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i].attempt
		right := candidates[j].attempt
		switch {
		case left.lastAttemptAt.IsZero() && !right.lastAttemptAt.IsZero():
			return true
		case !left.lastAttemptAt.IsZero() && right.lastAttemptAt.IsZero():
			return false
		case left.lastAttemptAt.Before(right.lastAttemptAt):
			return true
		case right.lastAttemptAt.Before(left.lastAttemptAt):
			return false
		default:
			return candidates[i].index < candidates[j].index
		}
	})
}

// equivalentTierForCandidate ...
//
// Expected: parameters for equivalentTierForCandidate.
//
// Returns: result of equivalentTierForCandidate.
//
// Side effects: None.
func equivalentTierForCandidate(tiers map[string]string, candidate provider.ModelPreference) string {
	if tier, ok := tiers[candidate.Model]; ok {
		return tier
	}
	if tier, ok := tiers[candidate.Provider]; ok {
		return tier
	}
	return ""
}
