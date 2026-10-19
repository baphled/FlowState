// Package failover provides health-aware provider selection, cooldown
// tracking, and failover routing.
//
// HealthManager tracks which provider/model pairs are rate-limited,
// cooldowned or hard-down, and persists that state to disk so cooldowns
// survive application restarts. Manager composes the health state with
// base preferences, overrides and the delivery allowlist to produce the
// ordered candidate pool every dispatch surface reads.
package failover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

const (
	healthScoreFailureWeight = uint64(1) << 32
	healthScoreMaxAge        = uint64(^uint32(0))

	persistDebounceInterval = 5 * time.Second
	persistMutationLimit    = 10
)

// healthEntry holds per-provider/model rate-limit health state. Keyed
// by the comparable ProviderModel struct (never a joined string) so a
// "+" in either id cannot collide two pairs.
type healthEntry struct {
	expiresAt        time.Time
	consecutiveFails int
	lastCooldown     time.Duration
	lastFailureAt    time.Time
	hardDown         bool
	hardDownReason   string
	permanentFails   int
}

// HealthManager manages provider/model health state with concurrency
// safety and on-disk persistence.
type HealthManager struct {
	mu          sync.RWMutex
	data        map[ProviderModel]healthEntry
	persistPath string
	dirty       bool
	mutations   int
	lastPersist time.Time
}

// NewHealthManager creates a new HealthManager persisting to the
// user's cache directory.
func NewHealthManager() *HealthManager {
	persistPath := filepath.Join(os.TempDir(), "flowstate", "provider-health.json")
	if cacheDir, err := os.UserCacheDir(); err == nil {
		persistPath = filepath.Join(cacheDir, "flowstate", "provider-health.json")
	}
	return &HealthManager{
		data:        make(map[ProviderModel]healthEntry),
		persistPath: persistPath,
	}
}

// Flush persists any unsaved mutations immediately.
func (hm *HealthManager) Flush() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if !hm.dirty {
		return nil
	}
	if err := hm.PersistState(hm.persistPath, hm.snapshotLocked()); err != nil {
		return err
	}
	hm.dirty = false
	hm.mutations = 0
	hm.lastPersist = time.Now()
	return nil
}

// Stop performs a final flush; the manager remains usable afterwards.
func (hm *HealthManager) Stop() error { return hm.Flush() }

// snapshotLocked copies the health map; callers hold hm.mu appropriately.
func (hm *HealthManager) snapshotLocked() map[ProviderModel]healthEntry {
	snapshot := make(map[ProviderModel]healthEntry, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	return snapshot
}

// maybePersistLocked flushes on the debounce interval or mutation
// budget so Mark* calls avoid a synchronous write each time.
func (hm *HealthManager) maybePersistLocked() {
	hm.dirty = true
	hm.mutations++
	if hm.lastPersist.IsZero() {
		hm.lastPersist = time.Now()
	}
	if hm.mutations < persistMutationLimit && time.Since(hm.lastPersist) < persistDebounceInterval {
		return
	}
	if err := hm.PersistState(hm.persistPath, hm.snapshotLocked()); err == nil {
		hm.dirty = false
		hm.mutations = 0
		hm.lastPersist = time.Now()
	}
}

// SetPersistPath updates the persistence path.
func (hm *HealthManager) SetPersistPath(path string) {
	hm.mu.Lock()
	hm.persistPath = path
	hm.mu.Unlock()
}

// PersistPath returns the current persistence path.
func (hm *HealthManager) PersistPath() string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.persistPath
}

// GetHealthyAlternatives returns all pairs not currently unavailable.
func (hm *HealthManager) GetHealthyAlternatives(_, _ string) []ProviderModel {
	hm.mu.RLock()
	snapshot := hm.snapshotLocked()
	hm.mu.RUnlock()
	var result []ProviderModel
	now := time.Now()
	for k, entry := range snapshot {
		if !entry.hardDown && !entry.expiresAt.After(now) && k.Provider != "" && k.Model != "" {
			result = append(result, k)
		}
	}
	return result
}

// ResetProviderHealth clears health state for an optional pair, or all
// state when both arguments are empty.
func (hm *HealthManager) ResetProviderHealth(providerName, model string) error {
	hm.mu.Lock()
	if providerName == "" && model == "" {
		hm.data = make(map[ProviderModel]healthEntry)
	} else {
		delete(hm.data, ProviderModel{Provider: providerName, Model: model})
	}
	hm.dirty = true
	hm.mu.Unlock()
	return hm.PersistState(hm.persistPath, hm.snapshotLocked())
}

// ConsecutiveFailures returns the consecutive failure count for a pair.
func (hm *HealthManager) ConsecutiveFailures(providerName, model string) int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	entry, ok := hm.data[ProviderModel{Provider: providerName, Model: model}]
	if !ok {
		return 0
	}
	return entry.consecutiveFails
}

// LastCooldown returns the last recorded cooldown for a pair.
func (hm *HealthManager) LastCooldown(providerName, model string) time.Duration {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	entry, ok := hm.data[ProviderModel{Provider: providerName, Model: model}]
	if !ok {
		return 0
	}
	return entry.lastCooldown
}

// HealthScore returns a ranking score where lower is healthier.
func (hm *HealthManager) HealthScore(providerName, model string, now time.Time) uint64 {
	entry, ok := hm.healthEntry(providerName, model)
	if !ok || entry.consecutiveFails <= 0 {
		return 0
	}
	score := uint64(entry.consecutiveFails) * healthScoreFailureWeight
	if entry.lastFailureAt.IsZero() {
		return score + healthScoreMaxAge
	}
	age := now.Sub(entry.lastFailureAt)
	if age < 0 {
		age = 0
	}
	ageSeconds := uint64(age / time.Second)
	if ageSeconds > healthScoreMaxAge {
		ageSeconds = healthScoreMaxAge
	}
	return score + (healthScoreMaxAge - ageSeconds)
}

// healthEntry returns the entry for a pair under RLock.
func (hm *HealthManager) healthEntry(providerName, model string) (healthEntry, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	entry, ok := hm.data[ProviderModel{Provider: providerName, Model: model}]
	return entry, ok
}

// MarkRateLimited marks a pair rate-limited until retryAfter, with
// cooldown escalation capped at 24h. A past retryAfter clears the
// cooldown (tests) without clearing a hard-down state.
func (hm *HealthManager) MarkRateLimited(providerName, model string, retryAfter time.Time) {
	hm.mu.Lock()
	key := ProviderModel{Provider: providerName, Model: model}
	now := time.Now()
	if !retryAfter.After(now) {
		entry := healthEntry{expiresAt: retryAfter, lastFailureAt: retryAfter}
		if existing, ok := hm.data[key]; ok {
			entry.consecutiveFails = existing.consecutiveFails
			entry.lastCooldown = existing.lastCooldown
			entry.hardDown = existing.hardDown
			entry.hardDownReason = existing.hardDownReason
			entry.permanentFails = existing.permanentFails
		} else {
			entry.consecutiveFails = 1
		}
		hm.data[key] = entry
		hm.maybePersistLocked()
		hm.mu.Unlock()
		return
	}
	newCooldown := retryAfter.Sub(now)
	newEntry := healthEntry{
		expiresAt:     retryAfter,
		lastCooldown:  newCooldown,
		lastFailureAt: now,
	}
	if existing, ok := hm.data[key]; ok {
		newEntry.hardDown = existing.hardDown
		newEntry.hardDownReason = existing.hardDownReason
		newEntry.permanentFails = existing.permanentFails
		if existing.expiresAt.After(now) || existing.hardDown {
			escalated := existing.lastCooldown * 2
			if escalated > 24*time.Hour {
				escalated = 24 * time.Hour
			}
			if newCooldown < escalated {
				newCooldown = escalated
			}
			newEntry.expiresAt = now.Add(newCooldown)
			newEntry.lastCooldown = newCooldown
			newEntry.consecutiveFails = existing.consecutiveFails + 1
		} else {
			newEntry.consecutiveFails = 1
		}
	} else {
		newEntry.consecutiveFails = 1
	}
	hm.data[key] = newEntry
	hm.maybePersistLocked()
	hm.mu.Unlock()
}

// RateLimitedUntil reports when a pair's cooldown expires.
func (hm *HealthManager) RateLimitedUntil(providerName, model string) (time.Time, bool) {
	entry, ok := hm.healthEntry(providerName, model)
	if !ok || entry.hardDown {
		return time.Time{}, false
	}
	if !entry.expiresAt.After(time.Now()) {
		return time.Time{}, false
	}
	return entry.expiresAt, true
}

// IsRateLimited reports whether a pair is rate-limited or hard-down.
func (hm *HealthManager) IsRateLimited(providerName, model string) bool {
	entry, ok := hm.healthEntry(providerName, model)
	if !ok {
		return false
	}
	return entry.hardDown || entry.expiresAt.After(time.Now())
}

// hardDownAuthCodes are auth-layer error codes describing billing-class
// account states that no credential refresh can recover.
var hardDownAuthCodes = map[string]bool{
	"account_deactivated": true,
	"billing_not_active":  true,
}

// hardDownPermanentFailThreshold is the consecutive auth-failure count
// that trips the breaker for non-billing-class codes.
const hardDownPermanentFailThreshold = 3

// permanentFailureClass reports whether an error class counts toward
// the breaker and whether it trips immediately.
func permanentFailureClass(errorType provider.ErrorType, errorCode string) (permanent, immediate bool) {
	switch errorType {
	case provider.ErrorTypeBilling:
		return true, true
	case provider.ErrorTypeAuthFailure:
		return true, hardDownAuthCodes[errorCode]
	default:
		return false, false
	}
}

// hardDownReason renders the trip cause.
func hardDownReason(errorType provider.ErrorType, errorCode string) string {
	if errorCode != "" {
		return string(errorType) + "/" + errorCode
	}
	return string(errorType)
}

// MarkPermanentFailure records a permanent-class failure and applies
// the hard-down breaker thresholds. Returns true when the pair tripped.
func (hm *HealthManager) MarkPermanentFailure(providerName, model string, errorType provider.ErrorType, errorCode string) bool {
	permanent, immediate := permanentFailureClass(errorType, errorCode)
	if !permanent {
		return false
	}
	hm.mu.Lock()
	key := ProviderModel{Provider: providerName, Model: model}
	entry := hm.data[key]
	entry.permanentFails++
	entry.lastFailureAt = time.Now()
	threshold := hardDownPermanentFailThreshold
	if immediate {
		threshold = 1
	}
	if entry.permanentFails >= threshold && !entry.hardDown {
		entry.hardDown = true
		entry.hardDownReason = hardDownReason(errorType, errorCode)
		hm.data[key] = entry
		if err := hm.PersistState(hm.persistPath, hm.snapshotLocked()); err == nil {
			hm.dirty = false
			hm.mutations = 0
			hm.lastPersist = time.Now()
		}
		hm.mu.Unlock()
		return true
	}
	hm.data[key] = entry
	hm.maybePersistLocked()
	hm.mu.Unlock()
	return entry.hardDown
}

// MarkHardDown trips the breaker directly (operator tooling).
func (hm *HealthManager) MarkHardDown(providerName, model, reason string) {
	hm.mu.Lock()
	key := ProviderModel{Provider: providerName, Model: model}
	entry := hm.data[key]
	entry.hardDown = true
	entry.hardDownReason = reason
	entry.lastFailureAt = time.Now()
	hm.data[key] = entry
	if err := hm.PersistState(hm.persistPath, hm.snapshotLocked()); err == nil {
		hm.dirty = false
		hm.mutations = 0
		hm.lastPersist = time.Now()
	}
	hm.mu.Unlock()
}

// IsHardDown reports whether the breaker has tripped for a pair.
func (hm *HealthManager) IsHardDown(providerName, model string) bool {
	entry, ok := hm.healthEntry(providerName, model)
	return ok && entry.hardDown
}

// HealthStateEntry is a public snapshot of one pair's health state.
type HealthStateEntry struct {
	Provider         string
	Model            string
	ExpiresAt        time.Time
	ConsecutiveFails int
	LastCooldown     time.Duration
	LastFailureAt    time.Time
	HardDown         bool
	HardDownReason   string
}

// GetHealthStateEntries returns public snapshots of tracked pairs.
func (hm *HealthManager) GetHealthStateEntries() []HealthStateEntry {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	now := time.Now()
	var result []HealthStateEntry
	for k, v := range hm.data {
		if v.hardDown || v.expiresAt.After(now) {
			result = append(result, HealthStateEntry{
				Provider:         k.Provider,
				Model:            k.Model,
				ExpiresAt:        v.expiresAt,
				ConsecutiveFails: v.consecutiveFails,
				LastCooldown:     v.lastCooldown,
				LastFailureAt:    v.lastFailureAt,
				HardDown:         v.hardDown,
				HardDownReason:   v.hardDownReason,
			})
		}
	}
	return result
}

// persistedEntry is the on-disk record format.
type persistedEntry struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ExpiresAt        string `json:"expires_at"`
	ConsecutiveFails int    `json:"consecutive_fails,omitempty"`
	LastCooldownMs   int64  `json:"last_cooldown_ms,omitempty"`
	LastFailureAt    string `json:"last_failure_at,omitempty"`
	HardDown         bool   `json:"hard_down,omitempty"`
	HardDownReason   string `json:"hard_down_reason,omitempty"`
	PermanentFails   int    `json:"permanent_fails,omitempty"`
}

// PersistState writes the health state to disk atomically.
func (hm *HealthManager) PersistState(path string, snapshot map[ProviderModel]healthEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	entries := make([]persistedEntry, 0, len(snapshot))
	for k, v := range snapshot {
		entries = append(entries, persistedEntry{
			Provider:         k.Provider,
			Model:            k.Model,
			ExpiresAt:        v.expiresAt.UTC().Format(time.RFC3339),
			ConsecutiveFails: v.consecutiveFails,
			LastCooldownMs:   v.lastCooldown.Milliseconds(),
			LastFailureAt:    v.lastFailureAt.UTC().Format(time.RFC3339),
			HardDown:         v.hardDown,
			HardDownReason:   v.hardDownReason,
			PermanentFails:   v.permanentFails,
		})
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// LoadState loads persisted health state, dropping expired entries
// (hard-down entries survive).
func (hm *HealthManager) LoadState(path string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	now := time.Now()
	var entries []persistedEntry
	if jerr := json.Unmarshal(b, &entries); jerr == nil {
		for _, e := range entries {
			if e.Provider == "" || e.Model == "" {
				continue
			}
			t, perr := time.Parse(time.RFC3339, e.ExpiresAt)
			if perr != nil && !e.HardDown {
				continue
			}
			if e.HardDown || t.After(now) {
				failureAt := t
				if e.LastFailureAt != "" {
					if parsed, ferr := time.Parse(time.RFC3339, e.LastFailureAt); ferr == nil {
						failureAt = parsed
					}
				} else if e.LastCooldownMs > 0 {
					failureAt = t.Add(-time.Duration(e.LastCooldownMs) * time.Millisecond)
				}
				hm.data[ProviderModel{Provider: e.Provider, Model: e.Model}] = healthEntry{
					expiresAt:        t,
					consecutiveFails: e.ConsecutiveFails,
					lastCooldown:     time.Duration(e.LastCooldownMs) * time.Millisecond,
					lastFailureAt:    failureAt,
					hardDown:         e.HardDown,
					hardDownReason:   e.HardDownReason,
					permanentFails:   e.PermanentFails,
				}
			}
		}
		return nil
	}
	legacy := make(map[string]string)
	if jerr := json.Unmarshal(b, &legacy); jerr != nil {
		return fmt.Errorf("unmarshal: %w", jerr)
	}
	for k, v := range legacy {
		t, perr := time.Parse(time.RFC3339, v)
		if perr != nil || !t.After(now) {
			continue
		}
		for i := range len(k) {
			if k[i] == '+' && i > 0 && i < len(k)-1 {
				hm.data[ProviderModel{Provider: k[:i], Model: k[i+1:]}] = healthEntry{expiresAt: t, lastFailureAt: t}
				break
			}
		}
	}
	return nil
}
