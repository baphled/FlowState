package failover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	healthScoreFailureWeight = uint64(1) << 32
	healthScoreMaxAge        = uint64(^uint32(0))
)

// healthEntry holds per-provider/model rate-limit health state.
//
// The in-memory state is keyed by the comparable ProviderModel struct
// (NOT a "<provider>+<model>" joined string). Bug Hunt May 2026 / M3
// closed the previous string-key collision: provider="a+b"/model="c"
// shared a map key with provider="a"/model="b+c", and the
// GetHealthyAlternatives re-parse split on the first "+" mis-attributed
// the boundary. Struct keys eliminate both halves and keep the public
// IsRateLimited / MarkRateLimited / RateLimitedUntil string-string
// signatures intact.
type healthEntry struct {
	expiresAt        time.Time
	consecutiveFails int
	lastCooldown     time.Duration
	lastFailureAt    time.Time
}

// HealthManager manages provider/model rate-limit health with concurrency safety.
type HealthManager struct {
	mu          sync.RWMutex
	data        map[ProviderModel]healthEntry
	persistPath string
}

// NewHealthManager creates a new HealthManager instance.
//
// Returns: a new HealthManager with empty rate-limit tracking.
// Side effects: allocates a new map for rate-limit state.
func NewHealthManager() *HealthManager {
	cacheDir, err := os.UserCacheDir()
	persistPath := filepath.Join(cacheDir, "flowstate", "provider-health.json")
	if err != nil {
		persistPath = filepath.Join(os.TempDir(), "flowstate", "provider-health.json")
	}
	return &HealthManager{
		data:        make(map[ProviderModel]healthEntry),
		persistPath: persistPath,
	}
}

// SetPersistPath updates the path used when persisting rate-limit state.
//
// Expected: path is a valid filesystem path writable by the process.
// Returns: nothing.
// Side effects: updates the persist path used on next MarkRateLimited call.
func (hm *HealthManager) SetPersistPath(path string) {
	hm.mu.Lock()
	hm.persistPath = path
	hm.mu.Unlock()
}

// PersistPath returns the current path used for persisting rate-limit state.
//
// Returns:
//   - The filesystem path where health state is persisted.
//
// Side effects:
//   - None.
//
// Expected: parameters for PersistPath.
func (hm *HealthManager) PersistPath() string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.persistPath
}

// GetHealthState returns a snapshot of the current health state for
// all tracked provider/model pairs. Used by the health CLI and TUI
// slash command (S5).
//
// Expected:
//   - None.
//
// Returns:
//   - A map of ProviderModel to healthEntry (expired entries excluded).
//
// Side effects:
//   - None (read-only).
func (hm *HealthManager) GetHealthState() map[ProviderModel]healthEntry {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	snapshot := make(map[ProviderModel]healthEntry, len(hm.data))
	now := time.Now()
	for k, v := range hm.data {
		if v.expiresAt.After(now) {
			snapshot[k] = v
		}
	}
	return snapshot
}

// GetHealthStateEntries returns a slice of exported HealthStateEntry
// structs for all tracked provider/model pairs. Used by the health CLI
// and TUI slash command (S5) which cannot access unexported fields.
//
// Expected:
//   - None.
//
// Returns:
//   - A slice of HealthStateEntry (expired entries excluded).
//
// Side effects:
//   - None (read-only).
func (hm *HealthManager) GetHealthStateEntries() []HealthStateEntry {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	now := time.Now()
	var result []HealthStateEntry
	for k, v := range hm.data {
		if v.expiresAt.After(now) {
			result = append(result, HealthStateEntry{
				Provider:         k.Provider,
				Model:            k.Model,
				ExpiresAt:        v.expiresAt,
				ConsecutiveFails: v.consecutiveFails,
				LastCooldown:     v.lastCooldown,
				LastFailureAt:    v.lastFailureAt,
			})
		}
	}
	return result
}

// ResetProviderHealth clears health state for an optional specific
// (provider, model) pair. When both are empty, ALL entries are cleared
// and the persist file is written with an empty array.
//
// Expected:
//   - provider and model may be "" to clear all.
//
// Returns:
//   - An error if persistence fails.
//
// Side effects:
//   - Clears health state and rewrites the persist file.
func (hm *HealthManager) ResetProviderHealth(provider, model string) error {
	hm.mu.Lock()
	if provider == "" && model == "" {
		hm.data = make(map[ProviderModel]healthEntry)
	} else {
		delete(hm.data, ProviderModel{Provider: provider, Model: model})
	}
	snapshot := make(map[ProviderModel]healthEntry, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.Unlock()
	return hm.PersistState(hm.persistPath, snapshot)
}

// ConsecutiveFailures returns the consecutive failure count for a
// provider/model pair. Returns 0 when the pair is not tracked.
//
// Expected:
//   - provider and model are non-empty strings.
//
// Returns:
//   - The number of consecutive failures.
//
// Side effects:
//   - None.
func (hm *HealthManager) ConsecutiveFailures(provider, model string) int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	entry, ok := hm.data[ProviderModel{Provider: provider, Model: model}]
	if !ok {
		return 0
	}
	return entry.consecutiveFails
}

// LastCooldown returns the last applied cooldown duration for a
// provider/model pair. Returns 0 when the pair is not tracked.
//
// Expected:
//   - provider and model are non-empty strings.
//
// Returns:
//   - The last cooldown duration.
//
// Side effects:
//   - None.
func (hm *HealthManager) LastCooldown(provider, model string) time.Duration {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	entry, ok := hm.data[ProviderModel{Provider: provider, Model: model}]
	if !ok {
		return 0
	}
	return entry.lastCooldown
}

// HealthScore returns a read-only ranking score for a provider/model pair.
//
// Lower scores are healthier. Provider/model pairs with no recorded failure
// history score best, then pairs with fewer consecutive failures, then pairs
// whose last failure is older.
//
// Expected: parameters for HealthScore.
// Returns: result of HealthScore.
// Side effects: None.
func (hm *HealthManager) HealthScore(provider, model string, now time.Time) uint64 {
	entry, ok := hm.healthEntry(provider, model)
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

// healthEntry ...
//
// Expected: parameters for healthEntry.
//
// Returns: result of healthEntry.
//
// Side effects: None.
func (hm *HealthManager) healthEntry(provider, model string) (healthEntry, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	entry, ok := hm.data[ProviderModel{Provider: provider, Model: model}]
	return entry, ok
}

// MarkRateLimited marks a provider/model as rate-limited until retryAfter.
//
// Expected: provider and model are non-empty strings, retryAfter is in the future.
// Returns: nothing.
// Side effects: updates internal rate-limit state and persists to ~/.cache/flowstate/provider-health.json.
func (hm *HealthManager) MarkRateLimited(provider, model string, retryAfter time.Time) {
	hm.mu.Lock()
	key := ProviderModel{Provider: provider, Model: model}

	now := time.Now()

	// When the caller passes a past time, they want to clear the entry
	// (used by tests and the Sleep function to re-open a candidate).
	// Don't escalate — just record the past expiry so IsRateLimited
	// returns false on the next check.
	if !retryAfter.After(now) {
		entry := healthEntry{expiresAt: retryAfter, lastFailureAt: retryAfter}
		if existing, ok := hm.data[key]; ok {
			entry.consecutiveFails = existing.consecutiveFails
			entry.lastCooldown = existing.lastCooldown
		} else {
			entry.consecutiveFails = 1
		}
		hm.data[key] = entry
		hm.mu.Unlock()
		return
	}

	newCooldown := retryAfter.Sub(now)
	newEntry := healthEntry{
		expiresAt:     retryAfter,
		lastCooldown:  newCooldown,
		lastFailureAt: now,
	}

	// Escalation: when the existing entry has not yet expired AND the
	// error type is the same (proxied by newCooldown >= existing cooldown),
	// double the cooldown capped at 24h and increment consecutiveFails.
	// When the existing entry has already expired, start fresh.
	if existing, ok := hm.data[key]; ok {
		if existing.expiresAt.After(now) {
			// Same-pair failure with live cooldown — escalate.
			escalated := existing.lastCooldown * 2
			cap := 24 * time.Hour
			if escalated > cap {
				escalated = cap
			}
			// Use the longer of existing escalated or new cooldown.
			if newCooldown < escalated {
				newCooldown = escalated
			}
			newEntry.expiresAt = now.Add(newCooldown)
			newEntry.lastCooldown = newCooldown
			newEntry.consecutiveFails = existing.consecutiveFails + 1
			newEntry.lastFailureAt = now
		} else {
			// Expired — reset.
			newEntry.consecutiveFails = 1
		}
	} else {
		newEntry.consecutiveFails = 1
	}

	hm.data[key] = newEntry
	snapshot := make(map[ProviderModel]healthEntry, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.Unlock()
	if err := hm.PersistState(hm.persistPath, snapshot); err != nil {
		_ = err
	}
}

// RateLimitedUntil reports the wall-clock time at which the
// provider/model's rate-limit cooldown expires.
//
// Returns the zero time and false when the pair is not currently
// rate-limited. Used by failover diagnostics and tests that need to
// assert the carrier-issued back-off (parsed from `retry-after`)
// produced a shorter cooldown than the per-error-type default.
//
// Expected:
//   - provider and model are non-empty strings.
//
// Returns:
//   - The cooldown expiry time and true when rate-limited.
//   - The zero time.Time and false when not rate-limited or expired.
//
// Side effects:
//   - None (read-only; expiry sweeping happens in IsRateLimited).
func (hm *HealthManager) RateLimitedUntil(provider, model string) (time.Time, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	expiry, ok := hm.data[ProviderModel{Provider: provider, Model: model}]
	if !ok || !expiry.expiresAt.After(time.Now()) {
		return time.Time{}, false
	}
	return expiry.expiresAt, true
}

// IsRateLimited returns true if provider/model is currently rate-limited.
//
// Expected: provider and model are non-empty strings.
// Returns: true if the provider/model is rate-limited and has not yet expired.
// Side effects: none.
func (hm *HealthManager) IsRateLimited(provider, model string) bool {
	entry, ok := hm.healthEntry(provider, model)
	if !ok {
		return false
	}
	if entry.expiresAt.After(time.Now()) {
		return true
	}
	return false
}

// GetHealthyAlternatives returns all ProviderModels not currently rate-limited.
//
// Expected: provider and model parameters are reserved for future use.
// Returns: a slice of ProviderModel entries that are healthy (not rate-limited).
// Side effects: none (read-only operation).
func (hm *HealthManager) GetHealthyAlternatives(_, _ string) []ProviderModel {
	hm.mu.RLock()
	snapshot := make(map[ProviderModel]healthEntry, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.RUnlock()

	var result []ProviderModel
	now := time.Now()
	for k, entry := range snapshot {
		if !entry.expiresAt.After(now) && k.Provider != "" && k.Model != "" {
			result = append(result, k)
		}
	}
	return result
}

// HealthStateEntry is a publicly-visible snapshot of a provider/model's
// health state. Used by the health CLI and TUI slash command (S5).
type HealthStateEntry struct {
	Provider         string
	Model            string
	ExpiresAt        time.Time
	ConsecutiveFails int
	LastCooldown     time.Duration
	LastFailureAt    time.Time
}

// persistedEntry is the on-disk representation of one rate-limit
// record. M3 introduced the array-of-records format to replace the
// previous "<provider>+<model>" joined-string map keys, which lost the
// boundary between fields whenever either contained a "+" (e.g.
// openrouter model ids like "mistral/mistral-7b+free").
type persistedEntry struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ExpiresAt        string `json:"expires_at"`
	ConsecutiveFails int    `json:"consecutive_fails,omitempty"`
	LastCooldownMs   int64  `json:"last_cooldown_ms,omitempty"`
	LastFailureAt    string `json:"last_failure_at,omitempty"`
}

// PersistState writes the health state to disk atomically.
//
// Expected: path is a valid file path, snapshot is a map of ProviderModel keys to expiry times.
// Returns: an error if directory creation, marshalling, or file operations fail.
// Side effects: creates directories and writes JSON file atomically via temp+rename to path.
//
// Wire format: a JSON array of {provider, model, expires_at} objects.
// The structured shape is unambiguous regardless of "+" characters in
// either id, closing the M3 collision (Bug Hunt May 2026).
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

// PersistStateInternal builds a snapshot under RLock and persists it.
//
// Expected: path is a valid file path.
// Returns: an error if taking a snapshot or persisting fails.
// Side effects: reads current rate-limit state and writes to disk atomically.
func (hm *HealthManager) PersistStateInternal(path string) error {
	hm.mu.RLock()
	snapshot := make(map[ProviderModel]healthEntry, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.RUnlock()
	return hm.PersistState(path, snapshot)
}

// LoadState loads the health state from disk, cleaning expired entries.
//
// Expected: path points to a JSON file written by PersistState. The
// post-M3 format is a JSON array of {provider, model, expires_at}
// records; for backwards compatibility, a pre-M3 JSON object with
// "<provider>+<model>" keys is also accepted and parsed using the same
// first-"+" split semantics as the legacy code so behaviour is
// unchanged for already-persisted state (the bug is closed for any
// newly written state).
//
// Returns: an error if reading or unmarshalling the file fails.
// Side effects: populates internal rate-limit state, discarding any expired entries.
func (hm *HealthManager) LoadState(path string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	now := time.Now()

	// Post-M3: array of records.
	var entries []persistedEntry
	if jerr := json.Unmarshal(b, &entries); jerr == nil {
		for _, e := range entries {
			if e.Provider == "" || e.Model == "" {
				continue
			}
			t, perr := time.Parse(time.RFC3339, e.ExpiresAt)
			if perr != nil {
				continue
			}
			if t.After(now) {
				failureAt := t
				if e.LastFailureAt != "" {
					if parsedFailureAt, ferr := time.Parse(time.RFC3339, e.LastFailureAt); ferr == nil {
						failureAt = parsedFailureAt
					}
				} else if e.LastCooldownMs > 0 {
					failureAt = t.Add(-time.Duration(e.LastCooldownMs) * time.Millisecond)
				}
				hm.data[ProviderModel{Provider: e.Provider, Model: e.Model}] = healthEntry{
					expiresAt:        t,
					consecutiveFails: e.ConsecutiveFails,
					lastCooldown:     time.Duration(e.LastCooldownMs) * time.Millisecond,
					lastFailureAt:    failureAt,
				}
			}
		}
		return nil
	}

	// Pre-M3 fallback: object map with "<provider>+<model>" string keys.
	// Parsed using first-"+" split to match legacy semantics.
	legacy := make(map[string]string)
	if jerr := json.Unmarshal(b, &legacy); jerr != nil {
		return fmt.Errorf("unmarshal: %w", jerr)
	}
	for k, v := range legacy {
		t, perr := time.Parse(time.RFC3339, v)
		if perr != nil {
			continue
		}
		if !t.After(now) {
			continue
		}
		sep := -1
		for i := range len(k) {
			if k[i] == '+' {
				sep = i
				break
			}
		}
		if sep <= 0 || sep >= len(k)-1 {
			continue
		}
		hm.data[ProviderModel{Provider: k[:sep], Model: k[sep+1:]}] = healthEntry{
			expiresAt:        t,
			consecutiveFails: 0,
			lastCooldown:     0,
			lastFailureAt:    t,
		}
	}
	return nil
}
