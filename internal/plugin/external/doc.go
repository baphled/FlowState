// Package external provides filesystem-based plugin discovery for FlowState.
//
// This package handles:
//   - Scanning a plugin directory for subdirectories containing manifest.json files
//   - Loading and validating plugin manifests from disk
//   - Applying enabled/disabled filters from configuration
package external
