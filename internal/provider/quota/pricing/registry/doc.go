// Package registry implements the remote pricing-table loader for the
// Provider Quota and Spend Visibility plan.
//
// This package handles:
//   - Fetching the registry URL via HTTP with a configurable timeout
//   - Using If-None-Match against cached ETags for incremental updates
//   - Opt-in loading controlled by pricing.registry.enabled and pricing.registry.url
package registry
