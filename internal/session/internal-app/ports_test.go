package internalapp

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

func TestPorts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Internal App Ports Suite")
}

// arrangeSession opens an in-memory store wired as ports, creates a session
// and appends one turn.start event; fixture failures panic.
func arrangeSession() (repo SessionRepo, log EventLog, ctx context.Context, cleanup func()) {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	repo = st
	log = st
	ctx = context.Background()
	if _, err := repo.CreateSession(ctx, "sid-1", "p", "m", "h", "sha"); err != nil {
		panic(err)
	}
	w, err := log.NewEventWriter(ctx, "sid-1")
	if err != nil {
		panic(err)
	}
	if _, err := w.Append(ctx, "user", "turn.start", map[string]any{"task": "t"}); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return repo, log, ctx, func() { _ = st.Close() }
}

var _ = Describe("Store wired as ports", func() {
	It("creates a session with active status through the repo port", func() {
		repo, _, ctx, cleanup := arrangeSession()
		DeferCleanup(cleanup)
		created, err := repo.CreateSession(ctx, "sid-2", "p", "m", "h", "sha")
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Status).To(Equal("active"))
	})

	Context("appends and replays a turn.start event through the event log port", func() {
		var observedEventCount int
		var observedEventType string
		BeforeEach(func() {
			_, log, ctx, cleanup := arrangeSession()
			DeferCleanup(cleanup)
			events, err := log.Replay(ctx, "sid-1")
			Expect(err).NotTo(HaveOccurred())
			observedEventCount = len(events)
			observedEventType = events[0].Type
		})
		It("records the expected number of events", func() { Expect(observedEventCount).To(Equal(1)) })
		It("records the expected event type", func() { Expect(observedEventType).To(Equal("turn.start")) })
	})

	It("round-trips status updates through the repo port", func() {
		repo, _, ctx, cleanup := arrangeSession()
		DeferCleanup(cleanup)
		err := repo.UpdateSessionStatus(ctx, "sid-1", "completed")
		Expect(err).NotTo(HaveOccurred())
		got, err := repo.GetSession(ctx, "sid-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal("completed"))
	})

	It("lists completed sessions through the repo port", func() {
		repo, _, ctx, cleanup := arrangeSession()
		DeferCleanup(cleanup)
		err := repo.UpdateSessionStatus(ctx, "sid-1", "completed")
		Expect(err).NotTo(HaveOccurred())
		listed, err := repo.ListSessions(ctx, "completed")
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(1))
	})
})
