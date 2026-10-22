// Package permissionrequest provides the in-process registry that holds
// suspended permission requests while ModeAskUser is the active permission
// mode for a session.
//
// This package handles:
//   - Holding suspended permission requests when a pathguard or runtime-gate denial fires
//   - Mirroring the turn.Registry locked-pre-check and insert pattern
//   - Blocking until a grant or context cancellation arrives
package permissionrequest
