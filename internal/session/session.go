// Package session provides the fresh-harness session and turn primitives
// for the FlowState rewrite. It is provider- and model-agnostic: admission,
// event ordering and turn lifecycle are defined here without any inference
// dependency.
package session

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// EventKind classifies ordered events recorded during a turn.
type EventKind string

const (
	EventTurnAdmitted  EventKind = "turn.admitted"
	EventToolResult    EventKind = "turn.tool_result"
	EventTurnComplete  EventKind = "turn.complete"
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

// Turn is one admitted unit of work inside a session.
type Turn struct {
	ID     string
	Events []Event
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

// New creates a fresh session with the given runtime-owned identity.
func New(id string) (*Session, error) {
	if id == "" {
		return nil, errors.New("session: id must not be empty")
	}
	return &Session{ID: id, start: time.Now()}, nil
}

// StartTurn admits a new turn into the session.
func (s *Session) StartTurn(id string) (*Turn, error) {
	if id == "" {
		return nil, errors.New("session: turn id must not be empty")
	}
	for _, t := range s.turns {
		if t.ID == id {
			return nil, fmt.Errorf("session: turn %q already exists", id)
		}
	}
	t := &Turn{ID: id}
	s.turns = append(s.turns, t)
	s.append(t, EventTurnAdmitted, "id="+id)
	return t, nil
}

// RecordToolResult appends an actual tool result event to a turn.
func (s *Session) RecordToolResult(t *Turn, payload string) error {
	if t == nil {
		return errors.New("session: nil turn")
	}
	if t.done {
		return fmt.Errorf("session: turn %q already terminal", t.ID)
	}
	s.append(t, EventToolResult, payload)
	return nil
}

// Complete marks a turn terminal and records its terminal result.
func (s *Session) Complete(t *Turn, result string) error {
	return s.finish(t, EventTurnComplete, result)
}

// Cancel marks a turn cancelled (bounded cancellation).
func (s *Session) Cancel(t *Turn) error {
	return s.finish(t, EventTurnCancelled, "")
}

func (s *Session) finish(t *Turn, k EventKind, payload string) error {
	if t == nil {
		return errors.New("session: nil turn")
	}
	if t.done {
		return fmt.Errorf("session: turn %q already terminal", t.ID)
	}
	t.done = true
	s.append(t, k, payload)
	return nil
}

// Events returns all session events in strict sequence order.
func (s *Session) Events() []Event {
	var out []Event
	for _, t := range s.turns {
		out = append(out, t.Events...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Seq < out[j].Seq
	})
	return out
}

// Turn returns the turn with the given id, if any.
func (s *Session) Turn(id string) *Turn {
	for _, t := range s.turns {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (s *Session) append(t *Turn, k EventKind, payload string) {
	s.seq++
	t.Events = append(t.Events, Event{Seq: s.seq, Kind: k, Payload: payload, Time: time.Now()})
}
