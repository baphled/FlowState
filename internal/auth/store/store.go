// Package store defines the cluster-ready session-record persistence
// interface for internal/auth and ships three implementations:
//
//   - MemoryStore — sync.Map-backed, full implementation, v1 default.
//   - RedisStore  — stub returning ErrNotImplemented from every method.
//   - PostgresStore — same stub shape as RedisStore.
//
// The interface is the load-bearing v1 commitment from the API Auth Track
// plan (May 2026), §"Session Store Interface" (lines 205-301): v3 must
// drop in a real Redis or Postgres implementation without touching
// middleware, login, or CSRF code. interface_compat_test.go pins the
// compile-time surface for all three impls; interface_contract_test.go
// parameterises the 8-row contract ladder over a constructor list (v1
// runs MemoryStore; stub impls Skip via isStub).
package store

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrSessionNotFound is returned by Get when no record exists for the
// token, when the record has expired (read-time check), or when the
// caller passes an empty token. CSRF lookups also use this error.
//
// Sentinel; callers compare with errors.Is.
var ErrSessionNotFound = errors.New("auth/store: session not found")

// ErrInvalidToken is returned by Put when rec.Token is empty. Storing a
// record under the empty key would collide with Get("") and is treated
// as a programmer error.
//
// Sentinel; callers compare with errors.Is.
var ErrInvalidToken = errors.New("auth/store: invalid token")

// ErrNotImplemented is returned by every method on the v1 stub stores
// (RedisStore, PostgresStore). v3 swap-in replaces these stubs with
// real implementations; the constructor and method set stay identical
// so callers see no surface change.
//
// Sentinel; callers compare with errors.Is.
var ErrNotImplemented = errors.New("auth/store: backend not implemented in v1; configure auth.store.backend = memory")

// Record is the persisted session payload. Opaque to the store —
// mode-specific principal data lives in Data.
//
// Field semantics (plan §"Session Store Interface" lines 229-238):
//
//   - Token       — opaque 256-bit random session id; primary key.
//   - Mode        — "shared-secret" | "per-deployment-login" | "multi-user".
//     Stamped at mint; checked at RequireSession (PR2/C4); mode-mismatch → 401.
//   - PrincipalID — identity-source-provided principal id (e.g. username,
//     deployment id).
//   - CSRFToken   — unmasked plain-text token bound to this record. NOT the
//     signed cookie value; the layered RequireCSRF middleware (PR2/C5)
//     compares the X-CSRF-Token header against this field after gorilla/csrf
//     has validated the signed cookie.
//   - CreatedAt   — mint timestamp.
//   - ExpiresAt   — TTL boundary; Get returns ErrSessionNotFound past this
//     instant. Cleanup sweeps records older than the supplied `before`.
//   - Data        — mode-specific fields (e.g. multi-user display name).
type Record struct {
	Token       string
	Mode        string
	PrincipalID string
	CSRFToken   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Data        map[string]string
}

// Store is the cluster-ready persistence interface.
//
// Implementations MUST be safe for concurrent use. Cleanup MAY be a no-op
// for stores with native TTL (Redis) and a sweep for stores without
// (Postgres, Memory).
//
// Contract (interface_contract_test.go, plan lines 274-285):
//  1. Get-after-Put round-trip equality — every field preserved bytewise.
//  2. Get-after-Delete returns ErrSessionNotFound (sentinel).
//  3. Get-after-expiry returns ErrSessionNotFound (read-time check).
//  4. Cleanup is idempotent.
//  5. Concurrent Put/Get on the same token is sequentially consistent.
//  6. Put with an existing token overwrites.
//  7. Empty-token handling: Get("") → ErrSessionNotFound; Put with empty
//     token → ErrInvalidToken.
//  8. Context cancellation honoured: Cleanup with a cancelled ctx returns
//     ctx.Err().
type Store interface {
	// Get returns the Record for the given token, or ErrSessionNotFound.
	// Expired records return ErrSessionNotFound even before Cleanup runs.
	Get(ctx context.Context, token string) (*Record, error)

	// Put stores the Record, overwriting any prior record for the same
	// token. Empty rec.Token returns ErrInvalidToken. Stores SHOULD honour
	// rec.ExpiresAt as TTL when supported natively.
	Put(ctx context.Context, rec *Record) error

	// Delete removes the Record for the given token. Idempotent —
	// deleting a non-existent token is a no-op (returns nil).
	Delete(ctx context.Context, token string) error

	// Cleanup sweeps records with ExpiresAt < before. Implementations
	// with native TTL MAY no-op. Honours ctx cancellation: a cancelled
	// ctx returns ctx.Err() promptly.
	Cleanup(ctx context.Context, before time.Time) error
}

// Compile-time conformance checks (plan §"Session Store Interface"
// line 271 — interface_compat_test.go equivalent). Catches surface
// drift the moment any impl falls behind the interface.
var (
	_ Store = (*MemoryStore)(nil)
	_ Store = (*RedisStore)(nil)
	_ Store = (*PostgresStore)(nil)
)

// MemoryStore is the v1 default in-memory Store implementation, backed
// by sync.Map for concurrent safety. No disk persistence; restart
// invalidates all sessions (acceptable for single-instance v1 per the
// auth track's "single-user-local perimeter" framing).
//
// Atomic-write discipline (per feedback_atomicity_awareness_uneven):
// disk persistence lands in OD-2 (PR2's session.go atomic JSON sidecar)
// or beyond; PR1 stays in-memory deliberately.
type MemoryStore struct {
	records sync.Map // map[string]*Record
}

// NewMemoryStore constructs an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Get returns the Record for token, or ErrSessionNotFound if missing,
// expired, or the token is empty. Honours ctx cancellation up-front
// (cheap check; no long-running work in this impl).
func (m *MemoryStore) Get(ctx context.Context, token string) (*Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, ErrSessionNotFound
	}
	val, ok := m.records.Load(token)
	if !ok {
		return nil, ErrSessionNotFound
	}
	rec, ok := val.(*Record)
	if !ok || rec == nil {
		return nil, ErrSessionNotFound
	}
	// Read-time expiry check (plan ladder row 3): impls MUST NOT depend
	// on Cleanup having run first.
	if !rec.ExpiresAt.IsZero() && !time.Now().Before(rec.ExpiresAt) {
		return nil, ErrSessionNotFound
	}
	return rec, nil
}

// Put stores rec, overwriting any prior record under the same token.
// Empty rec.Token returns ErrInvalidToken (plan ladder row 7).
func (m *MemoryStore) Put(ctx context.Context, rec *Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec == nil || rec.Token == "" {
		return ErrInvalidToken
	}
	m.records.Store(rec.Token, rec)
	return nil
}

// Delete removes the record for token. Idempotent: deleting a
// non-existent token returns nil (plan ladder row 2 — Delete is NOT
// a sentinel-returning operation).
func (m *MemoryStore) Delete(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.records.Delete(token)
	return nil
}

// Cleanup sweeps records with ExpiresAt strictly before `before`.
// Idempotent (plan ladder row 4) and honours ctx cancellation between
// records (plan ladder row 8).
func (m *MemoryStore) Cleanup(ctx context.Context, before time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var sweepErr error
	m.records.Range(func(key, value any) bool {
		if err := ctx.Err(); err != nil {
			sweepErr = err
			return false
		}
		rec, ok := value.(*Record)
		if !ok || rec == nil {
			m.records.Delete(key)
			return true
		}
		if !rec.ExpiresAt.IsZero() && rec.ExpiresAt.Before(before) {
			m.records.Delete(key)
		}
		return true
	})
	return sweepErr
}

// RedisStore is the v1 stub for the Redis backend. Every method returns
// ErrNotImplemented; the constructor compiles. v3 replaces the method
// bodies with the real impl — the constructor and method set stay
// identical so callers see no surface change.
//
// Plan §"Session Store Interface" line 265.
type RedisStore struct{}

// NewRedisStore constructs an empty RedisStore stub. The constructor
// exists so callers can wire `auth.store.backend = redis` in v1 config
// for forward compatibility; the first method call surfaces
// ErrNotImplemented.
func NewRedisStore() *RedisStore { return &RedisStore{} }

// Get returns ErrNotImplemented. v3 swap-in.
func (*RedisStore) Get(context.Context, string) (*Record, error) {
	return nil, ErrNotImplemented
}

// Put returns ErrNotImplemented. v3 swap-in.
func (*RedisStore) Put(context.Context, *Record) error { return ErrNotImplemented }

// Delete returns ErrNotImplemented. v3 swap-in.
func (*RedisStore) Delete(context.Context, string) error { return ErrNotImplemented }

// Cleanup returns ErrNotImplemented. v3 swap-in (Redis may no-op once
// native TTL is enabled).
func (*RedisStore) Cleanup(context.Context, time.Time) error { return ErrNotImplemented }

// PostgresStore is the v1 stub for the Postgres backend, identical in
// shape to RedisStore. Plan §"Session Store Interface" line 266.
type PostgresStore struct{}

// NewPostgresStore constructs an empty PostgresStore stub.
func NewPostgresStore() *PostgresStore { return &PostgresStore{} }

// Get returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Get(context.Context, string) (*Record, error) {
	return nil, ErrNotImplemented
}

// Put returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Put(context.Context, *Record) error { return ErrNotImplemented }

// Delete returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Delete(context.Context, string) error { return ErrNotImplemented }

// Cleanup returns ErrNotImplemented. v3 swap-in.
func (*PostgresStore) Cleanup(context.Context, time.Time) error { return ErrNotImplemented }

// IsStub reports whether s is a v1 stub implementation (RedisStore /
// PostgresStore) versus the full v1 impl (MemoryStore) or a future v3
// real impl. The contract test uses this to Skip rows on stubs while
// still asserting interface conformance.
//
// v3 impls MUST return false from IsStub so the contract ladder runs.
// The check is by concrete type rather than a method on Store so the
// interface surface stays minimal (the four CRUD methods).
func IsStub(s Store) bool {
	switch s.(type) {
	case *RedisStore, *PostgresStore:
		return true
	default:
		return false
	}
}
