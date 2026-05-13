// Package quota — PR6 persisted cache + LoadSpend hydration.
//
// This file extends the Tracker with the disk-persistence seam the
// app-layer ticker consumes. The PR5 Tracker collected TokenSpend
// Snapshots into a SpendStore (in-memory MemoryStore in v1); PR6
// layers on:
//
//  1. cacheEnvelope / spendCacheEntry — the versioned on-disk
//     serialisation shape. v1 envelope tag is "v1"; unrecognised
//     versions degrade to empty Tracker per the plan §"Rollout Plan"
//     PR6 row "Versioned schema (v1). Boot-time load of persisted
//     state."
//  2. MarshalCache(entries) / UnmarshalCache(data) — pure
//     serialisation helpers callable from the app wireup without the
//     quota package owning file I/O. Persistence applies only to
//     TokenSpend variants per plan OD-2: RateLimit variants are
//     point-in-time and reset on the provider's own clock — pointless
//     to persist.
//  3. LoadSpend(ctx, entries) — re-hydrate the Store from a previously
//     persisted list. Re-Puts each entry verbatim; the next Lookup
//     applies auto-reset rollover if the persisted period has expired
//     since the last shutdown.
//
// Engine boundary discipline ([[ADR - FlowState Engine Boundary]]):
// the quota package stays free of any disk-I/O imports. File reads,
// directory creation, and atomicwrite calls live in the app wireup;
// the quota package supplies only the serialisation contract and the
// hydration method.
//
// Plan §"Rollout Plan" PR6 row 430 + OD-2 RESOLVED 2026-05-13.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CacheEnvelopeVersion is the v1 on-disk envelope tag. Future schema
// changes bump this and UnmarshalCache returns ErrUnknownCacheVersion
// for any other value so the app wireup can log + degrade to empty
// Tracker rather than crash.
const CacheEnvelopeVersion = "v1"

// ErrUnknownCacheVersion is the sentinel UnmarshalCache returns when
// the on-disk envelope's Version field is not the supported v1 tag.
// The app wireup compares with errors.Is, logs a structured warn, and
// continues with an empty Tracker — next persist tick writes the
// current version.
var ErrUnknownCacheVersion = errors.New("quota: unknown cache envelope version")

// CacheEnvelope is the versioned on-disk wrapper. Storing the version
// inline (vs at the file-header level) lets the load path probe
// versions cheaply via a single JSON decode.
//
// Plan §"Rollout Plan" PR6 row 430 — "Versioned schema (v1)".
type CacheEnvelope struct {
	// Version is the schema tag. v1 is the only recognised value;
	// UnmarshalCache returns ErrUnknownCacheVersion for any other.
	Version string `json:"version"`

	// SavedAt records when the snapshot was persisted. Informational
	// only — the Tracker's rollover semantics drive period rotation
	// on load, not the SavedAt field.
	SavedAt time.Time `json:"saved_at"`

	// Snapshots carries the (Key, Snapshot) rows the Tracker held at
	// SavedAt time. Only entries whose Snapshot has a non-nil
	// TokenSpend variant are persisted per OD-2 (RateLimit variants
	// are point-in-time and reset on the provider clock).
	Snapshots []SpendCacheEntry `json:"snapshots"`
}

// SpendCacheEntry is one row in the on-disk envelope — a (Key,
// Snapshot) pair. The field names use JSON tags so a future schema
// migration can rename without breaking the v1 wire format.
type SpendCacheEntry struct {
	// Key partitions the Snapshot by (provider, account, model).
	Key SpendStoreKey `json:"key"`

	// Snapshot carries the persisted TokenSpend variant + adjacent
	// stamp fields (ObservedAt, StoreBackend, PricingSource). Only
	// snapshots whose TokenSpend variant is non-nil are persisted.
	Snapshot Snapshot `json:"snapshot"`
}

// MarshalCache renders the supplied entries into the v1 on-disk
// envelope. Filters out entries whose Snapshot does NOT carry a
// TokenSpend variant — RateLimit and NotConfigured Snapshots are
// point-in-time and pointless to persist (plan OD-2).
//
// Returns the JSON-encoded envelope ready for atomic-write to disk.
// `now` is stamped into SavedAt; pass time.Now() in production.
func MarshalCache(entries []SpendStoreEntry, now time.Time) ([]byte, error) {
	out := make([]SpendCacheEntry, 0, len(entries))
	for _, e := range entries {
		if e.Snapshot.TokenSpend == nil {
			continue
		}
		out = append(out, SpendCacheEntry{
			Key:      e.Key,
			Snapshot: e.Snapshot,
		})
	}
	env := CacheEnvelope{
		Version:   CacheEnvelopeVersion,
		SavedAt:   now.UTC(),
		Snapshots: out,
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("quota: marshalling cache envelope: %w", err)
	}
	return b, nil
}

// UnmarshalCache parses a previously-MarshalCache'd byte buffer into
// the entries the Tracker.LoadSpend method consumes.
//
// Returns:
//   - The flattened entries on success.
//   - ErrUnknownCacheVersion when the envelope's Version field is not
//     CacheEnvelopeVersion. The app wireup logs + degrades to empty
//     Tracker; the next persist tick writes the current version.
//   - A wrapped JSON error when the bytes are not valid JSON or the
//     envelope shape is malformed.
//
// Empty input data returns (nil, nil) — an empty cache file is a
// valid "no spend yet" state.
func UnmarshalCache(data []byte) ([]SpendStoreEntry, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var env CacheEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("quota: unmarshalling cache envelope: %w", err)
	}
	if env.Version != CacheEnvelopeVersion {
		return nil, fmt.Errorf("%w: got %q, want %q",
			ErrUnknownCacheVersion, env.Version, CacheEnvelopeVersion)
	}
	out := make([]SpendStoreEntry, 0, len(env.Snapshots))
	for _, e := range env.Snapshots {
		if e.Snapshot.TokenSpend == nil {
			// Defensive: an envelope written by a future bug could
			// have leaked a non-TokenSpend row in. Drop on load
			// rather than re-hydrate a stale RateLimit.
			continue
		}
		out = append(out, SpendStoreEntry{
			Key:      e.Key,
			Snapshot: e.Snapshot,
		})
	}
	return out, nil
}

// LoadSpend re-hydrates the Tracker's SpendStore from a previously-
// persisted entry list. Used by the app wireup at boot to restore
// cumulative spend across process restart.
//
// Contract:
//   - Each entry's Snapshot is Put into the Store verbatim. The Store's
//     own validation (empty ProviderID rejected) is honoured.
//   - The Tracker's per-request cumulative cache is NOT seeded — that
//     map partitions by RequestID, which is per-stream and does not
//     survive a restart. The next UsageDelta on a (provider, model)
//     starts a fresh request entry, and the delta-cost calculation
//     against the persisted cumulative Snapshot stays correct.
//   - Returns the first error from Store.Put; partial hydration is
//     possible if a mid-list Put fails. Callers SHOULD treat any
//     error as "cache load failed" and proceed with whatever state
//     was hydrated to that point.
//
// No-op (returns nil) when the Tracker has no spend wiring
// (constructed via NewTracker / NewTrackerWithPricing rather than
// NewTrackerWithSpend) — callers don't need to gate on tracker
// configuration.
func (t *Tracker) LoadSpend(ctx context.Context, entries []SpendStoreEntry) error {
	if t == nil || t.spend == nil || t.spend.storeBackend == nil {
		return nil
	}
	for _, e := range entries {
		if e.Snapshot.TokenSpend == nil {
			continue
		}
		if err := t.spend.storeBackend.Put(ctx, e.Key, e.Snapshot); err != nil {
			return fmt.Errorf("quota: hydrating cache entry %s/%s/%s: %w",
				e.Key.ProviderID, e.Key.AccountHash, e.Key.ModelID, err)
		}
	}
	return nil
}
