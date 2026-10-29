package session

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSession(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Session Suite")
}

func newTestSession() *Session {
	s, err := New("sess-1")
	if err != nil {
		panic(err)
	}
	return s
}

var _ = Describe("Session", func() {
	It("rejects an empty session id", func() {
		_, err := New("")
		Expect(err).To(HaveOccurred())
	})

	It("records ordered turn lifecycle events with ascending sequence numbers", func() {
		s := newTestSession()
		turn, err := s.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		if err := s.RecordToolResult(turn, "tool=shell output=ok"); err != nil {
			panic(err)
		}
		if err := s.Complete(turn, "result=done"); err != nil {
			panic(err)
		}

		events := s.Events()
		wantKinds := []EventKind{EventTurnAdmitted, EventToolResult, EventTurnComplete}
		gotKinds := make([]EventKind, len(events))
		ascending := true
		var last uint64
		for i, e := range events {
			gotKinds[i] = e.Kind
			if e.Seq <= last {
				ascending = false
			}
			last = e.Seq
		}
		Expect([2]any{gotKinds, ascending}).
			To(Equal([2]any{wantKinds, true}))
	})

	It("makes cancellation terminal", func() {
		s := newTestSession()
		turn, err := s.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		if err := s.Cancel(turn); err != nil {
			panic(err)
		}
		if s.RecordToolResult(turn, "late") == nil {
			panic("expected RecordToolResult after cancel to fail")
		}
		if s.Complete(turn, "late") == nil {
			panic("expected Complete after cancel to fail")
		}
		Expect(s.Events()[len(s.Events())-1].Kind).To(Equal(EventTurnCancelled))
	})

	It("rejects duplicate turn ids within a session", func() {
		s := newTestSession()
		_, err := s.StartTurn("dup")
		Expect(err).NotTo(HaveOccurred())
		_, dupErr := s.StartTurn("dup")
		Expect([2]any{dupErr != nil, dupErr != nil && strings.Contains(dupErr.Error(), "already exists")}).
			To(Equal([2]any{true, true}))
	})

	It("rejects turns owned by another session instance", func() {
		s1 := newTestSession()
		s2 := newTestSession()
		turn1, err := s1.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		foreign, err := s2.StartTurn("turn-foreign")
		Expect(err).NotTo(HaveOccurred())
		hijackErrs := []bool{
			s2.RecordToolResult(turn1, "hijack") != nil,
			s2.Complete(turn1, "hijack") != nil,
			s1.RecordToolResult(foreign, "hijack") != nil,
		}
		leaked := false
		for _, e := range append(s1.Events(), s2.Events()...) {
			if e.Payload != "" && e.Payload == "hijack" {
				leaked = true
			}
		}
		Expect([2]any{hijackErrs, leaked}).To(Equal([2]any{[]bool{true, true, true}, false}))
	})

	It("returns event snapshots that cannot mutate turn or session state", func() {
		s := newTestSession()
		turn, err := s.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		snap := turn.Events()
		snap[0].Payload = "tampered"
		turnUntampered := turn.Events()[0].Payload != "tampered"
		ssnap := s.Events()
		ssnap[0].Payload = "tampered"
		sessUntampered := s.Events()[0].Payload != "tampered"
		Expect([2]any{turnUntampered, sessUntampered}).To(Equal([2]any{true, true}))
	})

	It("sequences events from interleaved turns in global order", func() {
		s := newTestSession()
		a, err := s.StartTurn("turn-a")
		Expect(err).NotTo(HaveOccurred())
		b, err := s.StartTurn("turn-b")
		Expect(err).NotTo(HaveOccurred())
		for _, turn := range []*Turn{a, b, a, b, a} {
			if err := s.RecordToolResult(turn, "tool step"); err != nil {
				panic(err)
			}
		}
		if err := s.Complete(b, "b done"); err != nil {
			panic(err)
		}
		events := s.Events()
		ascending := true
		var last uint64
		for _, e := range events {
			if e.Seq <= last {
				ascending = false
			}
			last = e.Seq
		}
		Expect([3]any{len(events) > 0, ascending, events[len(events)-1].Kind}).
			To(Equal([3]any{true, true, EventTurnComplete}))
	})
})
