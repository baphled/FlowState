// Package app wires the M1 runtime graph explicitly: store, tool
// registry and per-session allowlisted tool loops. Composition is
// plain configuration with no ambient globals or package-level
// singletons: building twice from the same Dependencies yields two
// independent but identically-shaped graphs (M1-COMPOSITION).
package app

import (
	"context"
	"errors"

	"github.com/baphled/flowstate/internal/session/internal-app"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// Dependencies are the explicit inputs of the composition root. Every
// field is injectable so tests can supply substitutes such as an
// in-memory store or fake tools (M1-COMPOSITION: composition verified
// by an injectable-fake test that asserts the wiring graph).
type Dependencies struct {
	// Store is the SQLite event store; required (store.OpenMemory for
	// tests).
	Store *store.Store

	// Registry supplies the tools available to sessions; required.
	Registry *tool.Registry

	// Allowlist names the tools a session may invoke; deny-by-default
	// when empty (M1-TOOLS).
	Allowlist []string
}

// validate rejects incomplete wiring instead of panicking later.
func (d *Dependencies) validate() error {
	if d.Store == nil {
		return errors.New("app: store must not be nil")
	}
	if d.Registry == nil {
		return errors.New("app: registry must not be nil")
	}
	return nil
}

// Composition is one fully wired session runtime: the store plus a
// factory for per-session tool loops. It holds no ambient state; every
// field is the explicit wiring graph.
type Composition struct {
	store    *store.Store
	registry *tool.Registry
	allow    map[string]bool
}

// Wire builds the composition graph from Dependencies. Identical
// dependencies produce identical wiring; it performs no I/O.
func Wire(deps *Dependencies) (*Composition, error) {
	if deps == nil {
		return nil, errors.New("app: dependencies must not be nil")
	}
	if err := deps.validate(); err != nil {
		return nil, err
	}
	allow := make(map[string]bool, len(deps.Allowlist))
	for _, n := range deps.Allowlist {
		allow[n] = true
	}
	return &Composition{store: deps.Store, registry: deps.Registry, allow: allow}, nil
}

// Store exposes the wired store for session lifecycle calls.
func (c *Composition) Store() *store.Store { return c.store }

// SessionRepo exposes the consumer-side session-persistence port
// (DB-abstraction ADR packet B-2). Consumers should depend on this
// interface, not on *store.Store.
func (c *Composition) SessionRepo() internalapp.SessionRepo { return c.store }

// EventLog exposes the consumer-side event-persistence port.
func (c *Composition) EventLog() internalapp.EventLog { return c.store }

// Registry exposes the wired tool registry.
func (c *Composition) Registry() *tool.Registry { return c.registry }

// Allowed reports whether name is wired into the allowlist.
func (c *Composition) Allowed(name string) bool { return c.allow[name] }

// LoopHandle owns one session's tool loop and its event-writer
// registration. Close releases the writer so a later loop may be
// wired for the same session.
type LoopHandle struct {
	// Loop is the allowlisted tool execution loop for the session.
	Loop *tool.Loop

	// close releases the event-writer registration.
	close func() error
}

// Close releases the loop's event-writer registration.
func (h *LoopHandle) Close() error { return h.close() }

// SessionLoop wires a tool loop for one session through the store's
// single serialized event writer.
func (c *Composition) SessionLoop(ctx context.Context, sessionID string) (*LoopHandle, error) {
	w, err := c.store.NewEventWriter(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	l, err := tool.NewLoop(c.registry, w, c.allowNames())
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	return &LoopHandle{Loop: l, close: w.Close}, nil
}

// allowNames renders the allowlist set as a slice; order is not
// significant to the loop, which consults it by name.
func (c *Composition) allowNames() []string {
	names := make([]string, 0, len(c.allow))
	for n := range c.allow {
		names = append(names, n)
	}
	return names
}
