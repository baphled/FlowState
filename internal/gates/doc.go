// Package gates provides v0 ext-gate manifest loading and on-disk discovery.
//
// This package handles:
//   - Walking gatesDir one level deep to discover gate manifests
//   - Loading and validating manifest.yml files for each gate
//   - Silently ignoring missing gatesDir so boot proceeds with no gates
package gates
