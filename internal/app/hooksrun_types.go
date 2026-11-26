package app

import (
	"github.com/baphled/flowstate/internal/store"
)

// RootRunHooksRequest says how BeginRootRunHooks resolves and fires the run-level hooks of one root run.
type RootRunHooksRequest struct {
	// Scope is the already-parsed --hooks scope; the zero value loads nothing and fires nothing.
	Scope HookScope
	// ProjectRoot is where the project hook file is found; empty is the current directory. It is never
	// Workdir (ADR-012 D3).
	ProjectRoot string
	// Workdir is the working directory of a hook process; empty is the process's own.
	Workdir string
	// Lease is the exec lease the caller holds; its token is the run id a hook reads.
	Lease ExecLease
	// TextOnly is true for the text-only turn path, which has no tool, turn or compaction point: a hook on
	// one is listed as inert in hooks.loaded.
	TextOnly bool
}

// RootRunHooks is the run-level hook state of one root `session run` or `serve` run, between the lease and
// the end of the run (ADR-012, Which runs fire which points). A child and a swarm coordinator never hold one.
type RootRunHooks struct {
	st      *store.Store
	session string
	set     HookSet
	input   hookInput
	started bool
}
