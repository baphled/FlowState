// Package identity defines the per-mode identity source interface used by the
// FlowState auth track.
//
// This package handles:
//   - Selecting exactly one Source implementation at boot via cfg.Auth.Mode
//   - Handing off credential validation without coupling to the active mode
//   - Supporting shared-secret, per-deployment-login, and multi-user modes
package identity
