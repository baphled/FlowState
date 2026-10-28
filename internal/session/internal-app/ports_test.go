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

var _ = Describe("Store wired as ports", func() {
	It("round-trips session CRUD and event append/replay through the port interfaces", func() {
		st, err := store.OpenMemory()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(st.Close)

		var repo SessionRepo = st
		var log EventLog = st
		ctx := context.Background()

		created, err := repo.CreateSession(ctx, "sid-1", "p", "m", "h", "sha")
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Status).To(Equal("active"))

		w, err := log.NewEventWriter(ctx, "sid-1")
		Expect(err).NotTo(HaveOccurred())
		var wPort EventWriter = w
		_, err = wPort.Append(ctx, "user", "turn.start", map[string]any{"task": "t"})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())

		events, err := log.Replay(ctx, "sid-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Type).To(Equal("turn.start"))

		Expect(repo.UpdateSessionStatus(ctx, "sid-1", "completed")).To(Succeed())
		got, err := repo.GetSession(ctx, "sid-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal("completed"))
		listed, err := repo.ListSessions(ctx, "completed")
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(1))
	})
})
