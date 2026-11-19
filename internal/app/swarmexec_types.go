package app

import (
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
)

// SwarmChild pairs one delegated child session with the manifest
// packet it executes, so callers can map swarm work onto session runs.
type SwarmChild struct {
	// PacketID is the manifest packet the child executes.
	PacketID string
	// Session is the created child session: active, parent-linked and
	// opened with a session.delegated event plus its packet task.
	Session store.Session
}

// SwarmReservation records one child's budget grant drawn from the
// parent session's declared pool at swarm-execution time.
type SwarmReservation struct {
	// PacketID is the packet whose child holds the grant.
	PacketID string
	// ChildID is the child session the grant is reserved to.
	ChildID string
	// Granted is the reserved amount.
	Granted int64
}

// SwarmExecResult is the outcome of executing one configured swarm:
// the delegated children it created (in RunOrder, complete unless the
// run aborted mid-way), their budget reservations, and the durable
// admission-and-gate run's decision.
type SwarmExecResult struct {
	// Children lists the created child sessions in RunOrder; on an
	// abort it carries the children created before the failure.
	Children []SwarmChild
	// Reservations lists per-child budget grants in RunOrder; empty
	// when the swarm ran without a budget share.
	Reservations []SwarmReservation
	// Admitted lists the packet ids the durable run admitted, in
	// admission order.
	Admitted []string
	// Gates is the aggregated gate decision of the durable run; the
	// zero value marks a run that never reached its gates.
	Gates swarm.GatesDecision
}
