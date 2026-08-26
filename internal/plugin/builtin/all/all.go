// Package all is the canonical registration site for FlowState's built-in
// plugins per [[ADR - App Composition Root Boundary]].
//
// The registration contract:
//
//  1. Each built-in plugin package owns a register.go (or equivalent)
//     file with an init() function that calls
//     plugin.RegisterBuiltin(plugin.Registration{...}).
//
//  2. This package blank-imports each such package so the init() runs at
//     program start, before app.New invokes plugin.LoadBuiltins.
//
//  3. The composition root (internal/app/) blank-imports builtin/all
//     itself; it never imports the individual plugin packages for their
//     init side-effects. New built-in plugins are added by registering
//     here, not by editing app.go.
//
// Adding a new built-in plugin:
//
//   - Add init() + plugin.RegisterBuiltin in the plugin's own package.
//   - Append a blank-import line below.
//   - Tests in builtin/all_test.go assert the registration appears in
//     plugin.RegisteredBuiltins() so any plugin that forgets the init
//     wiring fails CI here, not in production.
//
// Currently registered (load-order is stable; lower Order loads first):
//
//   - eventlogger (Order 100, EnabledByDefault true) — writes engine
//     events to the configured log path.
//   - failover/ratelimit (Order 200, EnabledByDefault true) — detects
//     provider rate-limit errors and triggers failover.
package all

import (
	// Import eventlogger for its init-based builtin registration.
	_ "github.com/baphled/flowstate/internal/plugin/eventlogger"

	// Import failover for its init-based ratelimit-plugin registration.
	_ "github.com/baphled/flowstate/internal/plugin/failover"
)
