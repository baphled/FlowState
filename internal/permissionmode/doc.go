// Package permissionmode carries the per-session permission-mode value
// across the engine to tool dispatch boundary via context.
//
// This package handles:
//   - Propagating the five valid permission modes through context
//   - Supporting ModePlan, ModeDefault, ModeAcceptEdits, and ModeAskUser
//   - Keeping permission-mode enforcement decoupled from tool dispatch
package permissionmode
