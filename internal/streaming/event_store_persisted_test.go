package streaming_test

import (
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

// recordingAppender captures every SwarmEvent routed through it and
// optionally returns an error on Append. It is the testing double used to
// verify the persistedSwarmStore decorator invokes its disk-append hook for
// every in-memory Append while tolerating persistence failures.
type recordingAppender struct {
	mu     sync.Mutex
	events []streaming.SwarmEvent
	err    error
}

func (r *recordingAppender) Append(ev streaming.SwarmEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return r.err
}

func (r *recordingAppender) Events() []streaming.SwarmEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]streaming.SwarmEvent, len(r.events))
	copy(out, r.events)
	return out
}

var _ = Describe("persistedSwarmStore decorator (P4)", func() {
	refTime := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)

	sampleEvent := func(id string) streaming.SwarmEvent {
		return streaming.SwarmEvent{
			ID:            id,
			Type:          streaming.EventToolCall,
			Status:        "started",
			Timestamp:     refTime,
			AgentID:       "engineer",
			SchemaVersion: streaming.CurrentSchemaVersion,
		}
	}

	It("appends to the underlying store", func() {
		inner := streaming.NewMemorySwarmStore(10)
		appender := &recordingAppender{}
		store := streaming.NewPersistedSwarmStore(inner, appender.Append)

		ev := sampleEvent("evt-1")
		store.Append(ev)

		Expect(inner.All()).To(HaveLen(1))
		Expect(inner.All()[0].ID).To(Equal("evt-1"))
	})

	It("fires the disk-append hook for every in-memory Append", func() {
		inner := streaming.NewMemorySwarmStore(10)
		appender := &recordingAppender{}
		store := streaming.NewPersistedSwarmStore(inner, appender.Append)

		store.Append(sampleEvent("a"))
		store.Append(sampleEvent("b"))
		store.Append(sampleEvent("c"))

		seen := appender.Events()
		Expect(seen).To(HaveLen(3))
		Expect(seen[0].ID).To(Equal("a"))
		Expect(seen[1].ID).To(Equal("b"))
		Expect(seen[2].ID).To(Equal("c"))
	})

	It("tolerates a persistence error without panicking or blocking", func() {
		inner := streaming.NewMemorySwarmStore(10)
		appender := &recordingAppender{err: errors.New("disk full")}
		store := streaming.NewPersistedSwarmStore(inner, appender.Append)

		// Persistence failure must not prevent the in-memory append nor
		// propagate upward — the stream path must never block on disk I/O.
		Expect(func() { store.Append(sampleEvent("evt-err")) }).NotTo(Panic())
		Expect(inner.All()).To(HaveLen(1),
			"a failed disk append must not undo the in-memory append")
	})

	It("delegates Clear to the underlying store", func() {
		inner := streaming.NewMemorySwarmStore(10)
		appender := &recordingAppender{}
		store := streaming.NewPersistedSwarmStore(inner, appender.Append)

		store.Append(sampleEvent("a"))
		store.Clear()
		Expect(inner.All()).To(BeEmpty())
	})

	It("delegates All to the underlying store", func() {
		inner := streaming.NewMemorySwarmStore(10)
		appender := &recordingAppender{}
		store := streaming.NewPersistedSwarmStore(inner, appender.Append)

		store.Append(sampleEvent("a"))
		store.Append(sampleEvent("b"))

		all := store.All()
		Expect(all).To(HaveLen(2))
		Expect(all[0].ID).To(Equal("a"))
		Expect(all[1].ID).To(Equal("b"))
	})

	It("is nil-safe when constructed with a nil appender", func() {
		inner := streaming.NewMemorySwarmStore(10)
		store := streaming.NewPersistedSwarmStore(inner, nil)
		Expect(func() { store.Append(sampleEvent("a")) }).NotTo(Panic())
		Expect(inner.All()).To(HaveLen(1))
	})

	// P5/B2 — session isolation regression gate.
	//
	// On session switch the chat intent Clears the in-memory store and then
	// restores the events it just loaded from disk. Clear must not delete the
	// on-disk JSONL file (we are about to re-read those same bytes into a
	// new in-memory store) and restore must not re-fire the AppendFunc (that
	// would double every event on every session switch).
	Describe("P5 restore-mode contract", func() {
		It("Clear does not invoke the disk AppendFunc", func() {
			inner := streaming.NewMemorySwarmStore(10)
			appender := &recordingAppender{}
			store := streaming.NewPersistedSwarmStore(inner, appender.Append)

			store.Append(sampleEvent("a"))
			Expect(appender.Events()).To(HaveLen(1))

			store.Clear()

			// Clear is an in-memory operation: the AppendFunc must not be
			// invoked with some kind of "delete" sentinel and the disk file
			// must remain untouched. We observe this indirectly via the
			// appender record count.
			Expect(appender.Events()).To(HaveLen(1),
				"Clear must not fire the disk AppendFunc")
			Expect(inner.All()).To(BeEmpty())
		})

		It("RestoreEvents appends to the in-memory store without firing the AppendFunc", func() {
			inner := streaming.NewMemorySwarmStore(10)
			appender := &recordingAppender{}
			store := streaming.NewPersistedSwarmStore(inner, appender.Append)

			restored := []streaming.SwarmEvent{
				sampleEvent("r1"),
				sampleEvent("r2"),
				sampleEvent("r3"),
			}

			// RestoreEvents is the non-WAL entry point used by
			// handleSessionLoaded. It must populate the underlying memory
			// store without writing anything to disk.
			restorer, ok := store.(streaming.EventRestorer)
			Expect(ok).To(BeTrue(),
				"persistedSwarmStore must implement EventRestorer for P5")

			restorer.RestoreEvents(restored)

			Expect(inner.All()).To(HaveLen(3))
			Expect(appender.Events()).To(BeEmpty(),
				"RestoreEvents must NOT fire the disk AppendFunc — "+
					"otherwise every session switch doubles the on-disk events")
		})

		It("RestoreEvents on a plain MemorySwarmStore populates the store", func() {
			// The chat intent's SessionStore can be a plain MemorySwarmStore
			// in test scenarios or in embedded callers without persistence.
			// The restore path is shared, so MemorySwarmStore must also
			// satisfy the restore contract.
			mem := streaming.NewMemorySwarmStore(10)
			restorer, ok := streaming.SwarmEventStore(mem).(streaming.EventRestorer)
			Expect(ok).To(BeTrue(),
				"MemorySwarmStore must implement EventRestorer for P5")

			restorer.RestoreEvents([]streaming.SwarmEvent{
				sampleEvent("m1"),
				sampleEvent("m2"),
			})

			Expect(mem.All()).To(HaveLen(2))
		})

		It("RestoreEvents with an empty slice is a no-op", func() {
			inner := streaming.NewMemorySwarmStore(10)
			appender := &recordingAppender{}
			store := streaming.NewPersistedSwarmStore(inner, appender.Append)

			restorer, _ := store.(streaming.EventRestorer)
			Expect(func() { restorer.RestoreEvents(nil) }).NotTo(Panic())
			Expect(func() { restorer.RestoreEvents([]streaming.SwarmEvent{}) }).NotTo(Panic())

			Expect(inner.All()).To(BeEmpty())
			Expect(appender.Events()).To(BeEmpty())
		})
	})
})
