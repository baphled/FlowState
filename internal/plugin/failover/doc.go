// Package failover provides health-aware provider selection, cooldown tracking, and failover routing for the plugin system.
//
// This package manages provider/model health state and chooses healthy alternatives:
//   - Health state tracking with thread-safe RWMutex
//   - Persistent health state caching to disk
//   - Rate-limit and error-type cooldown classification
//   - Health-aware candidate selection and tier-aware fallback helpers
//
// HealthManager tracks which providers are rate-limited and when they will be healthy again.
// PersistState and LoadState handle disk caching to survive application restarts.
package failover
