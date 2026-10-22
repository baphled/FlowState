// Package quota provides token spend tracking, persisted cache, and
// LoadSpend hydration for provider quota management.
//
// This package handles:
//   - Collecting TokenSpend snapshots into a SpendStore
//   - Versioned on-disk serialisation via cacheEnvelope and spendCacheEntry
//   - MarshalCache and UnmarshalCache pure serialisation helpers
//   - Boot-time load of persisted state with version-aware degradation
package quota
