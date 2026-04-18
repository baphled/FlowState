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
})
