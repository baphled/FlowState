package app

import (
	"github.com/baphled/flowstate/internal/tool"
)

// DefaultDescendantLimit is how many descendant runs of one root run (children,
// grandchildren) may execute at once (ADR-009 D14). The per-call cap of four
// multiplies across the two levels, so the tree needs its own bound.
const DefaultDescendantLimit = 8

// descendantLimiter is the root run's cap on running descendants: a counting
// semaphore created with the root's delegate tool and shared down the tree.
type descendantLimiter struct {
	sem chan struct{}
}

// runSlot is the slot one descendant run holds while it executes. The run gives
// it up while its delegate call waits for its own children (yield) and takes one
// again afterwards (reclaim), so a parent never holds a slot a child needs. It is
// used by the one goroutine that runs the child's loop and needs no lock.
type runSlot struct {
	limiter *descendantLimiter
	held    bool
}

// delegatePlan is what a validated delegation needs to run: the child's registry,
// model and resolved working directory.
type delegatePlan struct {
	registry *tool.Registry
	modelID  string
	workdir  string
}
