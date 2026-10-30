// Package app wires the M1 runtime graph explicitly: store, tool
// registry and per-session allowlisted tool loops. Composition is
// plain configuration with no ambient globals or package-level
// singletons: building twice from the same Dependencies yields two
// independent but identically-shaped graphs (M1-COMPOSITION).
package app

import (
	"context"
	"errors"

	internalapp "github.com/baphled/flowstate/internal/session/internalapp"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

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
