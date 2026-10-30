package app

import (
	"github.com/baphled/flowstate/internal/store"
)

// compile-time proof that the concrete writer satisfies the port.
var _ EventWriter = (*store.EventWriter)(nil)

// compile-time proof that the concrete store adapter satisfies both
// ports without modification.
var (
	_ SessionRepo = (*store.Store)(nil)
	_ EventLog    = (*store.Store)(nil)
)
