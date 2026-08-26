// Package pricing implements the three-tier pricing-table resolution
// for provider quota and spend visibility.
//
// This package handles:
//   - Resolving per-model pricing via override, registry, and embedded tiers
//   - Loading operator-supplied pricing table overrides with highest precedence
//   - Merging partial override files over the embedded baseline
package pricing
