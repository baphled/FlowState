package failover

import (
	"errors"
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
func (m *Manager) SetBasePreferences(prefs []provider.ModelPreference) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.basePreferences = prefs
}

// SetOverride sets a user override as the first candidate. The override is prepended
// to base preferences so that base preferences are preserved as fallback.
//
// Expected:
//   - pref is a valid ModelPreference chosen by the user.
//
// Side effects:
//   - Replaces any existing override (thread-safe).
func (m *Manager) SetOverride(pref provider.ModelPreference) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.override = &pref
}

// ClearOverride removes the user override, restoring base preferences only.
//
// Side effects:
//   - Clears the override field (thread-safe).
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
func (m *Manager) SetLast(providerName, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastProvider = providerName
	m.lastModel = model
}

// ListModels returns all available models from all providers in the registry.
//
// Returns:
//   - A slice of all available models from all providers.
//   - An error if no models are available.
//
// Side effects:
//   - May make network calls to providers to fetch model lists.
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
	prefs := m.capabilityFilteredPreferences()
	if len(prefs) == 0 {
		return nil
	}
	result := make([]provider.ModelPreference, 0, len(prefs))
	for _, pref := range prefs {
		if !m.health.IsRateLimited(pref.Provider, pref.Model) {
			result = append(result, pref)
		}
	}
	return result
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
