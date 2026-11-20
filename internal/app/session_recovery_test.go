package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// mustOpenCrashStore opens an in-memory store for crash-recovery testing.
func mustOpenCrashStore() *store.Store {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	return st
}

// mustCreateCrashSession composes a test session, panicking on errors.
func mustCreateCrashSession(ctx context.Context, st *store.Store) *store.Session {
	sess, err := CreateSession(ctx, st, "probe.ai", "probe-model", DefaultHarness, "", "probe task")
	if err != nil {
		panic(err)
	}
	return sess
}

// mustAppendEvent appends an event to the session, panicking on errors.
func mustAppendEvent(ctx context.Context, st *store.Store, sessionID, actor, eventType string, payload map[string]any) {
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		panic(err)
	}
	defer w.Close()
	if _, err := w.Append(ctx, actor, eventType, payload); err != nil {
		panic(err)
	}
}

var _ = Describe("crash recovery detection", func() {
	var ctx context.Context
	BeforeEach(func() {
		ctx = context.Background()
	})

	It("detects a dangling turn.start with run activity as crashed", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		crashed, err := DetectCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(crashed).To(BeTrue())
	})

	It("does not detect a completed turn as crashed", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "turn.end", map[string]any{"status": "completed"})
		crashed, err := DetectCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(crashed).To(BeFalse())
	})

	It("does not detect a paused session as crashed", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "session.paused", map[string]any{"reason": "budget_exceeded"})
		crashed, err := DetectCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(crashed).To(BeFalse())
	})

	It("detects a delta-only dangling turn as crashed", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeAssistantDelta, map[string]any{"delta": "par"})
		crashed, err := DetectCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(crashed).To(BeTrue())
	})

	It("does not detect a delta-only completed turn as crashed", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeAssistantDelta, map[string]any{"delta": "par"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "turn.end", map[string]any{"status": "completed"})
		crashed, err := DetectCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(crashed).To(BeFalse())
	})
})

var _ = Describe("crash recovery", func() {
	var ctx context.Context
	BeforeEach(func() {
		ctx = context.Background()
	})

	It("appends session.interrupted with reason process_lost", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		err := RecoverCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		events, ierr := st.Replay(ctx, sess.ID)
		if ierr != nil {
			panic(ierr)
		}
		found := false
		for _, e := range events {
			if e.Type == "session.interrupted" {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue())
	})

	It("is idempotent — double recovery does not double-append", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		err := RecoverCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		err = RecoverCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		events, _ := st.Replay(ctx, sess.ID)
		count := 0
		for _, e := range events {
			if e.Type == "session.interrupted" {
				count++
			}
		}
		Expect(count).To(Equal(1))
	})
})

var _ = Describe("crash recovery — fresh session guard", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	It("does not detect a fresh session (no run activity) as crashed", func() {
		st := mustOpenCrashStore()
		sess := mustCreateCrashSession(ctx, st)
		crashed, err := DetectCrashedSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(crashed).To(BeFalse())
	})
})
