// Package app — ports.go holds the compile-time port-satisfaction proofs for the
// consumer-side persistence ports (type definitions live in
// ports_types.go, per the types-in-types.go code contract).
package app

import (
	"github.com/baphled/flowstate/internal/store"
)

// compile-time proof that the concrete store adapter satisfies both
// ports without modification.
var (
	_ SessionRepo = (*store.Store)(nil)
	_ EventLog    = (*store.Store)(nil)
)

// compile-time proof that the concrete writer satisfies the EventWriter port.
var _ EventWriter = (*store.EventWriter)(nil)
