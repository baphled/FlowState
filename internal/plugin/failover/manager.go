package failover

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

var errNoModelsAvailable = errors.New("no models available from any provider")

// Manager is the central component for provider selection, preference
// management, and state tracking during failover.
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
	// capabilityFilter, when non-nil, reports whether a pair is
	// tool-capable enough to be an auto-failover target. nil means no
	// capability filtering (legacy behaviour).
	capabilityFilter func(providerName, model string) bool
	// deliveryAllowlist, when non-nil, is the delivery-scoped
	// provider/model admission predicate (M01). It gates the candidate
	// pool and — unlike the capability filter — FAILS CLOSED: when
	// filtering empties the pool the empty result stands, because
	// crossing to an out-of-scope provider is a policy violation, not a
	// degradation. nil = legacy unfiltered.
	deliveryAllowlist *DeliveryAllowlist
}

// NewManager creates a Manager over the registry and health state.
// A nil registry disables model listing; a nil health manager disables
// health filtering.
func NewManager(registry *provider.Registry, health *HealthManager, timeout time.Duration) *Manager {
	if health == nil {
		health = NewHealthManager()
	}
	return &Manager{
		registry:   registry,
		health:     health,
		timeout:    timeout,
		attempts:   make(map[ProviderModel]attemptRecord),
		modelTiers: make(map[string]string),
	}
}

// SetBasePreferences replaces the ordered base preference chain.
func (m *Manager) SetBasePreferences(prefs []provider.ModelPreference) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.basePreferences = prefs
}

// SetOverride pins an explicit head preference (nil clears it).
func (m *Manager) SetOverride(pref *provider.ModelPreference) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pref == nil {
		m.override = nil
		return
	}
	cp := *pref
	m.override = &cp
}

// ClearOverride removes the override.
func (m *Manager) ClearOverride() { m.SetOverride(nil) }

// Override returns the current override, if any.
func (m *Manager) Override() (provider.ModelPreference, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.override == nil {
		return provider.ModelPreference{}, false
	}
	return *m.override, true
}

// SetCapabilityFilter installs the tool-capability filter (nil clears).
func (m *Manager) SetCapabilityFilter(filter func(providerName, model string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.capabilityFilter = filter
}

// SetDeliveryAllowlist installs the delivery-scoped allowlist. Once
// set, Candidates() admits only allowlisted pairs, failing closed on
// exhaustion. nil removes the allowlist (legacy behaviour).
func (m *Manager) SetDeliveryAllowlist(allow *DeliveryAllowlist) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deliveryAllowlist = allow
}

// SetModelTiers replaces the tier lookup used to group equivalent
// candidates without rotating across tier boundaries.
func (m *Manager) SetModelTiers(tiers map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelTiers = tiers
}

// Preferences returns the effective preference list (override first).
func (m *Manager) Preferences() []provider.ModelPreference {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.effectivePreferences()
}

// Candidates returns effective preferences filtered by capability,
// delivery allowlist, and health.
func (m *Manager) Candidates() []provider.ModelPreference {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return rankCandidatesByHealth(m.health, m.modelTiers, m.attempts, m.capabilityFilteredPreferences())
}

// LastProvider returns the last attempted provider name.
func (m *Manager) LastProvider() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastProvider
}

// LastModel returns the last attempted model.
func (m *Manager) LastModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastModel
}

// SetLast records the last attempted pair.
func (m *Manager) SetLast(providerName, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastProvider = providerName
	m.lastModel = model
}

// RecordAttempt stamps the attempt clock for a pair.
func (m *Manager) RecordAttempt(providerName, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.attempts[ProviderModel{Provider: providerName, Model: model}]
	rec.lastAttemptAt = time.Now()
	m.attempts[ProviderModel{Provider: providerName, Model: model}] = rec
}

// ListModels returns catalogue metadata across the registry.
func (m *Manager) ListModels() ([]provider.Model, error) {
	if m.registry == nil {
		return nil, errNoModelsAvailable
	}
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
		for _, m := range models {
			allModels = append(allModels, provider.Model{ID: m.ID, Provider: p.Name(), ContextLength: m.ContextLength, OutputLimit: m.OutputLimit})
		}
	}
	if len(allModels) == 0 {
		return nil, errNoModelsAvailable
	}
	return allModels, nil
}

// StreamTimeout returns the per-attempt timeout.
func (m *Manager) StreamTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.timeout
}

// Health returns the health manager.
func (m *Manager) Health() *HealthManager {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.health
}

// effectivePreferences composes override ahead of base preferences.
func (m *Manager) effectivePreferences() []provider.ModelPreference {
	if m.override == nil {
		return m.basePreferences
	}
	result := make([]provider.ModelPreference, 0, 1+len(m.basePreferences))
	result = append(result, *m.override)
	result = append(result, m.basePreferences...)
	return result
}

// capabilityFilteredPreferences composes the effective preference list
// with the capability filter and the delivery allowlist applied to the
// BASE tail only. The override (the caller's explicit head choice) is
// preserved unfiltered and prepended.
func (m *Manager) capabilityFilteredPreferences() []provider.ModelPreference {
	base := m.capabilityFilteredBase()
	base = m.deliveryAllowlisted(base, false)
	if m.override == nil {
		return base
	}
	result := make([]provider.ModelPreference, 0, 1+len(base))
	result = append(result, *m.override)
	result = append(result, base...)
	return result
}

// capabilityFilteredBase returns base preferences with tool-incapable
// pairs removed, guarding against an empty result.
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

// deliveryAllowlisted filters prefs against the installed delivery
// allowlist, failing closed (an all-rejected list STAYS empty — the
// empty-guard rationale that applies to tool capability does not apply
// to policy). The head is retained when it is the caller's explicit
// override: a pinned pair is the caller's deliberate choice and its
// health is evaluated at attempt time.
func (m *Manager) deliveryAllowlisted(prefs []provider.ModelPreference, retainHead bool) []provider.ModelPreference {
	if m.deliveryAllowlist == nil || len(prefs) == 0 {
		return prefs
	}
	out := make([]provider.ModelPreference, 0, len(prefs))
	for i, pref := range prefs {
		if i == 0 && retainHead {
			out = append(out, pref)
			continue
		}
		if m.deliveryAllowlist.Allowed(pref.Provider, pref.Model) {
			out = append(out, pref)
		}
	}
	return out
}

// attemptRecord tracks attempt/success timestamps for LRU rotation
// between equivalent candidates.
type attemptRecord struct {
	lastAttemptAt time.Time
	lastSuccessAt time.Time
}

// rankCandidatesByHealth drops rate-limited candidates and orders the
// remainder by health score, keeping equivalent candidates grouped.
func rankCandidatesByHealth(
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

type rankedCandidate struct {
	candidate provider.ModelPreference
	score     uint64
	tier      string
	index     int
	attempt   attemptRecord
}

// rankedCandidatesToPreferences projects ranked candidates back to
// preferences.
func rankedCandidatesToPreferences(candidates []rankedCandidate) []provider.ModelPreference {
	result := make([]provider.ModelPreference, len(candidates))
	for i, candidate := range candidates {
		result[i] = candidate.candidate
	}
	return result
}

// orderEquivalentCandidates orders same-score candidates with
// stickiness and LRU rotation inside tier groups.
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

// groupEquivalentCandidates buckets candidates by tier.
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

// sortEquivalentGroup sorts one tier group: sticky successes first,
// then least-recently-attempted, preserving original order on ties.
func sortEquivalentGroup(candidates []rankedCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i].attempt
		right := candidates[j].attempt
		if !left.lastSuccessAt.IsZero() || !right.lastSuccessAt.IsZero() {
			switch {
			case left.lastSuccessAt.IsZero():
				return false
			case right.lastSuccessAt.IsZero():
				return true
			case left.lastSuccessAt.After(right.lastSuccessAt):
				return true
			case right.lastSuccessAt.After(left.lastSuccessAt):
				return false
			default:
				return candidates[i].index < candidates[j].index
			}
		}
		switch {
		case left.lastAttemptAt.IsZero() && !right.lastAttemptAt.IsZero():
			return false
		case !left.lastAttemptAt.IsZero() && right.lastAttemptAt.IsZero():
			return true
		case right.lastAttemptAt.After(left.lastAttemptAt):
			return true
		case left.lastAttemptAt.After(right.lastAttemptAt):
			return false
		default:
			return candidates[i].index < candidates[j].index
		}
	})
}

// equivalentTierForCandidate resolves a candidate's tier by model then
// provider.
func equivalentTierForCandidate(tiers map[string]string, candidate provider.ModelPreference) string {
	if tier, ok := tiers[candidate.Model]; ok {
		return tier
	}
	if tier, ok := tiers[candidate.Provider]; ok {
		return tier
	}
	return ""
}
