// Package auth provides HTTP middleware and session management for the
// FlowState API authentication track.
//
// This package handles:
//   - Session record attachment to request contexts via RequireSession
//   - CSRF token generation and validation for state-changing requests
//   - Origin header enforcement to prevent cross-site request forgery
//   - Login and session-store wiring across deployment modes
package auth
