package session

import "time"

// EventKind classifies ordered events recorded during a turn.
type EventKind string

// Event kinds emitted over the lifetime of a session turn.
const (
	// EventTurnAdmitted marks a turn as admitted into the session.
	EventTurnAdmitted EventKind = "turn.admitted"
	// EventToolResult records a tool result within the turn.
	EventToolResult EventKind = "turn.tool_result"
	// EventTurnComplete marks the turn as completed.
	EventTurnComplete EventKind = "turn.complete"
	// EventTurnCancelled marks the turn as cancelled.
	EventTurnCancelled EventKind = "turn.cancelled"
)

// Event is an immutable, ordered record of something that happened inside a
// session. Sequence numbers are assigned by the session and strictly
// increase in append order.
type Event struct {
	Seq     uint64
	Kind    EventKind
	Payload string
	Time    time.Time
}

// Turn is one admitted unit of work inside a session. Its events are owned
// by the session; callers read them via the immutable Events snapshot.
type Turn struct {
	ID     string
	events []Event
	done   bool
}

// Session owns identity, ordered events and turn lifecycle for one
// conversation with the harness.
type Session struct {
	ID    string
	start time.Time
	seq   uint64
	turns []*Turn
}
