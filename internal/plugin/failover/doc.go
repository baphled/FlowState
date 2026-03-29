// Package failover provides provider health tracking and rate-limit detection for the plugin system.
//
// This package manages health state for language model providers, detecting rate limits
// and coordinating failover to healthy alternatives:
//   - Health state tracking with thread-safe RWMutex
//   - Persistent health state caching to disk
//   - Rate-limit detection and provider switching
//   - Tier-based fallback chains for automatic failover
//
// HealthManager tracks which providers are rate-limited and when they will be healthy again.
// PersistStateInternal and LoadState handle disk caching to survive application restarts.
package failover
