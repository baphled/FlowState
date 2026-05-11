package failover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HealthManager manages provider/model rate-limit health with concurrency safety.
//
// The in-memory state is keyed by the comparable ProviderModel struct
// (NOT a "<provider>+<model>" joined string). Bug Hunt May 2026 / M3
// closed the previous string-key collision: provider="a+b"/model="c"
// shared a map key with provider="a"/model="b+c", and the
// GetHealthyAlternatives re-parse split on the first "+" mis-attributed
// the boundary. Struct keys eliminate both halves and keep the public
// IsRateLimited / MarkRateLimited / RateLimitedUntil string-string
// signatures intact.
type HealthManager struct {
	mu          sync.RWMutex
	data        map[ProviderModel]time.Time
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
		data:        make(map[ProviderModel]time.Time),
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

// MarkRateLimited marks a provider/model as rate-limited until retryAfter.
//
// Expected: provider and model are non-empty strings, retryAfter is in the future.
// Returns: nothing.
// Side effects: updates internal rate-limit state and persists to ~/.cache/flowstate/provider-health.json.
func (hm *HealthManager) MarkRateLimited(provider, model string, retryAfter time.Time) {
	hm.mu.Lock()
	key := ProviderModel{Provider: provider, Model: model}
	hm.data[key] = retryAfter
	snapshot := make(map[ProviderModel]time.Time, len(hm.data))
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
	if !ok || !expiry.After(time.Now()) {
		return time.Time{}, false
	}
	return expiry, true
}

// IsRateLimited returns true if provider/model is currently rate-limited.
//
// Expected: provider and model are non-empty strings.
// Returns: true if the provider/model is rate-limited and has not yet expired.
// Side effects: cleans up expired rate-limit entries when checking.
func (hm *HealthManager) IsRateLimited(provider, model string) bool {
	hm.mu.RLock()
	key := ProviderModel{Provider: provider, Model: model}
	expiry, ok := hm.data[key]
	hm.mu.RUnlock()
	if !ok {
		return false
	}
	if expiry.After(time.Now()) {
		return true
	}
	hm.mu.Lock()
	delete(hm.data, key)
	hm.mu.Unlock()
	return false
}

// GetHealthyAlternatives returns all ProviderModels not currently rate-limited.
//
// Expected: provider and model parameters are reserved for future use.
// Returns: a slice of ProviderModel entries that are healthy (not rate-limited).
// Side effects: none (read-only operation).
func (hm *HealthManager) GetHealthyAlternatives(_, _ string) []ProviderModel {
	hm.mu.RLock()
	snapshot := make(map[ProviderModel]time.Time, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.RUnlock()

	var result []ProviderModel
	now := time.Now()
	for k, expiry := range snapshot {
		if !expiry.After(now) && k.Provider != "" && k.Model != "" {
			result = append(result, k)
		}
	}
	return result
}

// persistedEntry is the on-disk representation of one rate-limit
// record. M3 introduced the array-of-records format to replace the
// previous "<provider>+<model>" joined-string map keys, which lost the
// boundary between fields whenever either contained a "+" (e.g.
// openrouter model ids like "mistral/mistral-7b+free").
type persistedEntry struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	ExpiresAt string `json:"expires_at"`
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
func (hm *HealthManager) PersistState(path string, snapshot map[ProviderModel]time.Time) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	entries := make([]persistedEntry, 0, len(snapshot))
	for k, v := range snapshot {
		entries = append(entries, persistedEntry{
			Provider:  k.Provider,
			Model:     k.Model,
			ExpiresAt: v.UTC().Format(time.RFC3339),
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
	snapshot := make(map[ProviderModel]time.Time, len(hm.data))
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
				hm.data[ProviderModel{Provider: e.Provider, Model: e.Model}] = t
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
		hm.data[ProviderModel{Provider: k[:sep], Model: k[sep+1:]}] = t
	}
	return nil
}
