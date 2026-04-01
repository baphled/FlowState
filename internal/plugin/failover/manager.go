package failover

import (
	"errors"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

var errNoModelsAvailable = errors.New("no models available from any provider")

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
}

// ResolveContextLength returns the context length for a given provider/model, or 4096 if unknown.
//
// Expected:
//   - providerName is the name of the provider to query.
//   - model is the model identifier to look up.
//
// Returns:
//   - The context length for the given model, or 4096 if the provider or model is unknown.
//
// Side effects:
//   - None.
func (m *Manager) ResolveContextLength(providerName, model string) int {
	p, err := m.registry.Get(providerName)
	if err != nil {
		return 4096
	}
	models, err := p.Models()
	if err != nil {
		return 4096
	}
	for _, m := range models {
		if m.ID == model {
			if m.ContextLength > 0 {
				return m.ContextLength
			}
		}
	}
	return 4096
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
		registry: registry,
		health:   health,
		timeout:  timeout,
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

// healthyCandidates returns effective preferences filtered by health state.
//
// Expected: called under at least an RLock on m.mu.
// Returns: preferences with rate-limited entries removed, preserving order.
// Side effects: none.
func (m *Manager) healthyCandidates() []provider.ModelPreference {
	prefs := m.effectivePreferences()
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
