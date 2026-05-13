// Package store defines the cluster-ready Snapshot-record persistence
// interface for internal/provider/quota and ships three
// implementations:
//
//   - MemoryStore — sync.RWMutex-backed map, full implementation, v1
//     default.
//   - RedisStore  — stub returning ErrNotImplemented from every method.
//   - PostgresStore — same stub shape as RedisStore.
//
// The interface is the load-bearing v1 commitment from the Provider
// Quota and Spend Visibility plan (May 2026), §"`internal/provider/
// quota/store/` — cluster-ready Store interface (B3 Option A)"
// (lines 236-298): v3 must drop in a real Redis or Postgres
// implementation without touching the engine, the Tracker, or any
// per-provider adapter. The 8-row contract ladder parameterises over
// a constructor list (v1 runs MemoryStore; stub impls Skip via
// IsStub) — identical pattern to internal/auth/store from the API
// Auth Track plan that shipped 2026-05-13.
//
// Atomic-write discipline (per memory feedback_atomicity_awareness
// _uneven): disk persistence is OD-2 deferred beyond PR1 per the
// plan's "Rollout Plan" (PR6 row 430). PR1 stays in-memory
// deliberately.
package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider/quota"
)

// ErrSnapshotNotFound is returned by Get when no Snapshot exists for
// the Key. Reset/Delete-after-Reset Get calls also surface this
// sentinel.
//
// Sentinel; callers compare with errors.Is.
var ErrSnapshotNotFound = errors.New("quota/store: snapshot not found")

// ErrInvalidKey is returned by Put when key.ProviderID is empty.
// Storing a Snapshot under an empty-provider key would collide with
// the zero Key sentinel and is treated as a programmer error. Empty
// AccountHash is tolerated (ollama-style no-key providers); empty
// ModelID is tolerated (account-wide snapshots).
//
// Sentinel; callers compare with errors.Is.
var ErrInvalidKey = errors.New("quota/store: invalid key (empty ProviderID)")

// ErrNotImplemented is returned by every method on the v1 stub stores
// (RedisStore, PostgresStore). v3 swap-in replaces these stubs with
// real implementations; the constructor and method set stay identical
// so callers see no surface change.
//
// Sentinel; callers compare with errors.Is.
var ErrNotImplemented = errors.New("quota/store: backend not implemented in v1; configure quota.store.backend = memory")

// Key partitions Snapshots by (provider, account, model). Per OD-3
// resolution (plan lines 234-235), the partition has no principal
// component — multi-user deployments share one Snapshot per Key
// across all authenticated users.
//
// Plan §"`internal/provider/quota/store/`" lines 254-258.
type Key struct {
	ProviderID  string
	AccountHash string
	ModelID     string
}

// Store is the cluster-ready persistence interface.
//
// Implementations MUST be safe for concurrent use. Cleanup MAY be a
// no-op for stores with native TTL (Redis) and a sweep for stores
// without (Postgres, Memory).
//
// Contract (interface_contract_test.go — mirrors internal/auth/store's
// 8-row ladder from the API Auth Track plan, applied to the
// Snapshot/Key shape):
//  1. Get-after-Put round-trip equality — every field preserved.
//  2. Get-after-Delete returns ErrSnapshotNotFound (sentinel).
//  3. Get-after-Reset returns ErrSnapshotNotFound — Reset clears,
//     subsequent Put restores.
//  4. Cleanup is idempotent.
//  5. Concurrent Put/Get/Delete on disjoint Keys is sequentially
//     consistent (run under -race).
//  6. Put with an existing Key overwrites.
//  7. Empty-key handling: Get(zero Key) → ErrSnapshotNotFound; Put
//     with empty ProviderID → ErrInvalidKey.
//  8. Context cancellation honoured: Cleanup with a cancelled ctx
//     returns ctx.Err().
//
// Plan §"`internal/provider/quota/store/`" lines 244-252.
type Store interface {
	// Get returns the Snapshot for the given Key, or ErrSnapshotNotFound.
	Get(ctx context.Context, key Key) (quota.Snapshot, error)

	// Put stores the Snapshot under the given Key, overwriting any
	// prior Snapshot for the same Key. Empty key.ProviderID returns
	// ErrInvalidKey.
	Put(ctx context.Context, key Key, snap quota.Snapshot) error

	// Delete removes the Snapshot for the given Key. Idempotent —
	// deleting a non-existent Key is a no-op (returns nil).
	Delete(ctx context.Context, key Key) error

	// Reset clears the Snapshot for the given Key. Idempotent —
	// resetting a non-existent Key is a no-op. Semantically distinct
	// from Delete only for stores that may retain audit trails of the
	// reset (future v3 Postgres impl); MemoryStore Reset == Delete.
	// Used by the PR4 PeriodStart-rollover ticker and the PR5
	// "Reset spend counter" UI button per OD-8.
	Reset(ctx context.Context, key Key) error

	// Cleanup sweeps stale RateLimit-variant Snapshots past their
	// TightestResetAt wall-clock. Implementations with native TTL
	// MAY no-op. Honours ctx cancellation: a cancelled ctx returns
	// ctx.Err() promptly. TokenSpend variants are never cleaned —
	// they persist across the period boundary until Reset.
	Cleanup(ctx context.Context, now time.Time) error
}

// Compile-time conformance checks (plan §"`internal/provider/quota/
// store/`" line 264 — the `var _ Store = (*RedisStore)(nil)` style).
// Catches surface drift the moment any impl falls behind the
// interface.
var (
	_ Store = (*MemoryStore)(nil)
	_ Store = (*RedisStore)(nil)
	_ Store = (*PostgresStore)(nil)
)

// MemoryStore is the v1 default in-memory Store implementation, backed
// by a map under sync.RWMutex. No disk persistence in PR1; that's the
// PR6 row of the rollout plan (plan line 430). Restart invalidates
// all in-flight RateLimit snapshots — acceptable for v1 since
// providers refresh them on the next request.
//
// Atomic-write discipline (per memory feedback_atomicity_awareness
// _uneven): disk persistence lands in PR6's atomic JSON sidecar
// (HealthManager pattern at internal/plugin/failover/healthmanager.go:
// 148-188). PR1 stays in-memory deliberately.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[Key]quota.Snapshot
}

// NewMemoryStore constructs an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[Key]quota.Snapshot)}
}

// Get returns the Snapshot for key, or ErrSnapshotNotFound if absent.
// Honours ctx cancellation up-front.
func (m *MemoryStore) Get(ctx context.Context, key Key) (quota.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return quota.Snapshot{}, err
	}
	if key.ProviderID == "" {
		return quota.Snapshot{}, ErrSnapshotNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap, ok := m.data[key]
	if !ok {
		return quota.Snapshot{}, ErrSnapshotNotFound
	}
	return snap, nil
}

// Put stores snap under key, overwriting any prior Snapshot. Empty
// key.ProviderID returns ErrInvalidKey (ladder row 7).
func (m *MemoryStore) Put(ctx context.Context, key Key, snap quota.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key.ProviderID == "" {
		return ErrInvalidKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = snap
	return nil
}

// Delete removes the Snapshot for key. Idempotent (ladder row 2).
func (m *MemoryStore) Delete(ctx context.Context, key Key) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// Reset clears the Snapshot for key. For MemoryStore semantically
// identical to Delete; future v3 Postgres impl may retain audit
// trails of the reset (out of v1 scope).
func (m *MemoryStore) Reset(ctx context.Context, key Key) error {
	return m.Delete(ctx, key)
}

// Cleanup sweeps RateLimit-variant Snapshots whose TightestResetAt
// has passed. TokenSpend variants are never cleaned — they persist
// across the period boundary until Reset. Honours ctx cancellation
// between records (ladder row 8). Idempotent (ladder row 4).
func (m *MemoryStore) Cleanup(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, snap := range m.data {
		if err := ctx.Err(); err != nil {
			return err
		}
		if snap.RateLimit == nil {
			continue
		}
		// Sweep RateLimit Snapshots whose TightestResetAt has strictly
		// passed. Zero-time reset means "no signal"; never swept.
		if !snap.RateLimit.TightestResetAt.IsZero() && snap.RateLimit.TightestResetAt.Before(now) {
			delete(m.data, k)
		}
	}
	return nil
}

// Entry is one row returned from List — a (Key, Snapshot) pair. Used
// by the PR5 dashboard aggregator to render one row per partition
// key.
type Entry struct {
	Key      Key
	Snapshot quota.Snapshot
}

// List returns every (Key, Snapshot) the MemoryStore holds. Order is
// unspecified; callers that need a deterministic sequence sort at the
// API layer. PR5 dashboard aggregator entry point.
//
// Honours ctx cancellation up-front; the snapshot is taken under
// RLock so concurrent Put / Delete during the copy is safe.
func (m *MemoryStore) List(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := make([]Entry, 0, len(m.data))
	for k, snap := range m.data {
		entries = append(entries, Entry{Key: k, Snapshot: snap})
	}
	return entries, nil
}

// RedisStore is the v1 stub for the Redis backend. Every method
// returns ErrNotImplemented; the constructor compiles. v3 replaces
// the method bodies with the real impl — the constructor and method
// set stay identical so callers see no surface change.
//
// Plan §"`internal/provider/quota/store/`" line 264.
type RedisStore struct{}

// NewRedisStore constructs an empty RedisStore stub. Exists so
// operators can wire `quota.store.backend = redis` in v1 config for
// forward compatibility; the first method call surfaces
// ErrNotImplemented.
func NewRedisStore() *RedisStore { return &RedisStore{} }

// Get returns ErrNotImplemented. v3 swap-in.
func (*RedisStore) Get(context.Context, Key) (quota.Snapshot, error) {
	return quota.Snapshot{}, ErrNotImplemented
}

// Put returns ErrNotImplemented. v3 swap-in.
func (*RedisStore) Put(context.Context, Key, quota.Snapshot) error { return ErrNotImplemented }

// Delete returns ErrNotImplemented. v3 swap-in.
func (*RedisStore) Delete(context.Context, Key) error { return ErrNotImplemented }

// Reset returns ErrNotImplemented. v3 swap-in.
func (*RedisStore) Reset(context.Context, Key) error { return ErrNotImplemented }

// Cleanup returns ErrNotImplemented. v3 swap-in (Redis may no-op
// once native TTL is enabled).
func (*RedisStore) Cleanup(context.Context, time.Time) error { return ErrNotImplemented }

// PostgresStore is the v1 stub for the Postgres backend, identical
// in shape to RedisStore. Plan §"`internal/provider/quota/store/`"
// line 265.
type PostgresStore struct{}

// NewPostgresStore constructs an empty PostgresStore stub.
func NewPostgresStore() *PostgresStore { return &PostgresStore{} }

// Get returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Get(context.Context, Key) (quota.Snapshot, error) {
	return quota.Snapshot{}, ErrNotImplemented
}

// Put returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Put(context.Context, Key, quota.Snapshot) error { return ErrNotImplemented }

// Delete returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Delete(context.Context, Key) error { return ErrNotImplemented }

// Reset returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Reset(context.Context, Key) error { return ErrNotImplemented }

// Cleanup returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Cleanup(context.Context, time.Time) error { return ErrNotImplemented }

// IsStub reports whether s is a v1 stub implementation (RedisStore /
// PostgresStore) versus the full v1 impl (MemoryStore) or a future
// v3 real impl. The contract test uses this to Skip rows on stubs
// while still asserting interface conformance.
//
// v3 impls MUST return false from IsStub so the contract ladder
// runs. The check is by concrete type rather than a method on Store
// so the interface surface stays minimal (the five CRUD+Reset
// methods).
func IsStub(s Store) bool {
	switch s.(type) {
	case *RedisStore, *PostgresStore:
		return true
	default:
		return false
	}
}

// ValidateDeploymentTopology rejects the misconfigured
// `quota.store.backend = memory` + `quota.store.deployment_topology
// = multi-instance` combination at boot. Per plan §"Boot validation"
// lines 289-291: boot fails ONLY when an operator explicitly sets
// `multi-instance` AND leaves `backend = memory` (the default). A
// fresh install (both keys absent → both default to
// single-instance + memory) boots quietly.
//
// Returns nil for the four valid combinations:
//   - single-instance + memory  (the quiet default)
//   - single-instance + redis   (operator opted into stub backend)
//   - multi-instance  + redis   (the intended v3 path)
//   - multi-instance  + postgres
//
// Returns an error for the only honest-stance failure mode:
//   - multi-instance + memory   (silent double-count without this gate)
//
// Plan B4 / B3 resolution.
func ValidateDeploymentTopology(backend, topology string) error {
	if topology == "multi-instance" && backend == "memory" {
		return errors.New(
			"quota.store.backend = memory is incompatible with multi-instance topology; " +
				"configure backend = redis or set deployment_topology = single-instance",
		)
	}
	return nil
}
