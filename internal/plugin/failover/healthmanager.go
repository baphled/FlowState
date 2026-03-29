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
type HealthManager struct {
	mu          sync.RWMutex
	data        map[string]time.Time
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
		data:        make(map[string]time.Time),
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
// Returns: an error if persisting state to disk fails.
// Side effects: updates internal rate-limit state and persists to ~/.cache/flowstate/provider-health.json.
func (hm *HealthManager) MarkRateLimited(provider, model string, retryAfter time.Time) error {
	hm.mu.Lock()
	key := provider + "+" + model
	hm.data[key] = retryAfter
	snapshot := make(map[string]time.Time, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.Unlock()
	return hm.PersistState(hm.persistPath, snapshot)
}

// IsRateLimited returns true if provider/model is currently rate-limited.
//
// Expected: provider and model are non-empty strings.
// Returns: true if the provider/model is rate-limited and has not yet expired.
// Side effects: cleans up expired rate-limit entries when checking.
func (hm *HealthManager) IsRateLimited(provider, model string) bool {
	hm.mu.RLock()
	key := provider + "+" + model
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
	snapshot := make(map[string]time.Time, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.RUnlock()

	var result []ProviderModel
	now := time.Now()
	for k, expiry := range snapshot {
		if !expiry.After(now) {
			sep := -1
			for i := range len(k) {
				if k[i] == '+' {
					sep = i
					break
				}
			}
			if sep > 0 && sep < len(k)-1 {
				prov := k[:sep]
				mod := k[sep+1:]
				result = append(result, ProviderModel{Provider: prov, Model: mod})
			}
		}
	}
	return result
}

// PersistState writes the health state to disk atomically.
//
// Expected: path is a valid file path, snapshot is a map of provider+model keys to expiry times.
// Returns: an error if directory creation, marshalling, or file operations fail.
// Side effects: creates directories and writes JSON file atomically via temp+rename to path.
func (hm *HealthManager) PersistState(path string, snapshot map[string]time.Time) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	m := make(map[string]string, len(snapshot))
	for k, v := range snapshot {
		m[k] = v.UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(m, "", "  ")
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
	snapshot := make(map[string]time.Time, len(hm.data))
	for k, v := range hm.data {
		snapshot[k] = v
	}
	hm.mu.RUnlock()
	return hm.PersistState(path, snapshot)
}

// LoadState loads the health state from disk, cleaning expired entries.
//
// Expected: path points to a valid JSON file containing provider+model keys.
// Returns: an error if reading or unmarshalling the file fails.
// Side effects: populates internal rate-limit state, discarding any expired entries.
func (hm *HealthManager) LoadState(path string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	m := make(map[string]string)
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	now := time.Now()
	for k, v := range m {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			continue
		}
		if t.After(now) {
			hm.data[k] = t
		}
	}
	return nil
}
