// Package swarm owns configured-swarm admission control: packets
// admitted against pinned source hashes, satisfied prerequisites and
// an acyclic scope-bound parent/child graph. Runtime orchestration
// (leases, budgets, child sessions) composes on top.
package swarm

// Packet is one admission-control packet: the unit a configured
// swarm admits before any delegation runs. The source digest pins
// the exact bytes the packet was scoped from; prerequisites must be
// admitted first; ParentID forms the scope-bound parent/child graph.
type Packet struct {
	// ID uniquely identifies the packet within the swarm.
	ID string
	// Scope is the dot-separated capability scope ("m2.runtime").
	Scope string
	// Task is the human-readable work description.
	Task string
	// SourceSHA256 is the declared SHA-256 of the source body.
	SourceSHA256 string
	// ParentID names the admitting parent packet; empty at the root.
	ParentID string
	// Prerequisites lists packet ids that must be admitted first.
	Prerequisites []string
}

// AdmissionLog is the in-memory record of admitted packets backing
// admission control. Production persistence composes it with the
// store; it owns admission decisions only.
type AdmissionLog struct {
	admitted map[string]Packet
}
