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

// Events returns an immutable snapshot of the turn's events in sequence
// order. The returned slice is a copy: mutating it cannot affect session
// state.
func (t *Turn) Events() []Event {
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
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

// owns reports whether t is a turn admitted by this session.
func (s *Session) owns(t *Turn) error {
	if t == nil {
		return errors.New("session: nil turn")
	}
	for _, own := range s.turns {
		if own == t {
			return nil
		}
	}
	return fmt.Errorf("session: turn %q does not belong to session %q", t.ID, s.ID)
}

// RecordToolResult appends an actual tool result event to a turn owned by
// this session. Turns from other sessions are rejected to prevent
// cross-session corruption.
func (s *Session) RecordToolResult(t *Turn, payload string) error {
	if err := s.owns(t); err != nil {
		return err
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
	if err := s.owns(t); err != nil {
		return err
	}
	if t.done {
		return fmt.Errorf("session: turn %q already terminal", t.ID)
	}
	t.done = true
	s.append(t, k, payload)
	return nil
}

// Events returns a globally sequence-ordered snapshot of all session
// events, correctly interleaving events from concurrent turns.
func (s *Session) Events() []Event {
	var out []Event
	for _, t := range s.turns {
		out = append(out, t.events...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
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
	t.events = append(t.events, Event{Seq: s.seq, Kind: k, Payload: payload, Time: time.Now()})
}
