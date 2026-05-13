package quota

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// Tracker is the central in-memory accumulator. The engine constructs
// one per session-server (NewEngine wires it in PR4); it subscribes to
// provider responses via the Quota interface and persists the latest
// observed Snapshot per (provider, account_hash, model) tuple via the
// configured Store.
//
// PR1 scope (plan §"Engine integration / spend accumulation rules"
// lines 299-318): the Tracker records the latest observed Snapshot
// from a registered per-provider adapter and serves it back via
// Lookup. Spend math is NOT in scope for PR1 — that's PR4. The
// PR1 Tracker exists so the engine has a callable seam and PR1
// Anthropic responses produce a non-stale Snapshot the chip can read.
//
// Concurrency: RecordResponse and Lookup are safe for concurrent use
// across goroutines. The Tracker uses a sync.RWMutex internally; per-
// adapter calls go through the adapter's own concurrency contract.
type Tracker struct {
	mu        sync.RWMutex
	adapters  map[string]Quota // providerID → Quota adapter
	storeBackend string         // surfaced into Snapshot.StoreBackend
}

// NewTracker constructs an empty Tracker. The storeBackend string
// ("memory" | "redis" | "postgres") is surfaced into every Snapshot
// the Tracker emits so the chip's tooltip can render the
// single-instance-scope disclosure (plan B3 fold, line 174).
func NewTracker(storeBackend string) *Tracker {
	return &Tracker{
		adapters:     make(map[string]Quota),
		storeBackend: storeBackend,
	}
}

// Register binds a per-provider Quota adapter under the given
// providerID. Re-registering the same providerID overwrites the prior
// adapter. The engine calls this once per configured provider at boot.
func (t *Tracker) Register(providerID string, adapter Quota) {
	if adapter == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.adapters[providerID] = adapter
}

// Lookup returns the current Snapshot for (providerID, modelID) by
// delegating to the registered adapter. Returns a NotConfigured
// Snapshot when no adapter is registered for providerID — this is
// the v1 fallback for unknown providers (e.g. a future provider not
// yet wired into the per-provider matrix).
//
// The returned Snapshot is stamped with t.storeBackend so consumers
// don't need to thread it separately.
func (t *Tracker) Lookup(ctx context.Context, providerID, modelID string) (Snapshot, error) {
	t.mu.RLock()
	adapter, ok := t.adapters[providerID]
	t.mu.RUnlock()
	if !ok {
		return Snapshot{
			Provider:      providerID,
			Model:         modelID,
			ObservedAt:    time.Now(),
			StoreBackend:  t.storeBackend,
			NotConfigured: &NotConfiguredVariant{Reason: "no-adapter-registered"},
		}, nil
	}
	snap, err := adapter.Remaining(ctx, providerID, modelID)
	if err != nil {
		return Snapshot{}, err
	}
	// Stamp the store-backend into the snapshot so the chip tooltip
	// can disclose single-instance scope. Adapters need not know the
	// backend.
	snap.StoreBackend = t.storeBackend
	return snap, nil
}

// RecordResponse fans out a provider response to the registered
// adapter. The engine calls this from the streaming pipe after every
// chunk that carries Usage data, and from the chat pipe after every
// non-stream response.
//
// No-op when no adapter is registered for providerID — the engine
// must not crash because a future provider isn't wired in yet.
func (t *Tracker) RecordResponse(providerID, modelID string, headers http.Header, usage provider.Usage) {
	t.mu.RLock()
	adapter, ok := t.adapters[providerID]
	t.mu.RUnlock()
	if !ok {
		return
	}
	adapter.RecordResponse(providerID, modelID, headers, usage)
}

// StoreBackend returns the backend label the Tracker stamps into
// every Snapshot. Exposed for tests and for the boot-validation
// audit trail.
func (t *Tracker) StoreBackend() string {
	return t.storeBackend
}
