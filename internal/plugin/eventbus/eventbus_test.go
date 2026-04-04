package eventbus

import (
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EventBus", func() {
	var bus *EventBus

	BeforeEach(func() {
		bus = NewEventBus()
	})

	It("delivers published events to subscribed handlers", func() {
		var received any
		bus.Subscribe("foo", func(e any) { received = e })
		bus.Publish("foo", 42)
		Expect(received).To(Equal(42))
	})

	It("does not deliver events to unsubscribed handlers", func() {
		var received any
		h := func(e any) { received = e }
		bus.Subscribe("foo", h)
		bus.Unsubscribe("foo", h)
		bus.Publish("foo", 99)
		Expect(received).To(BeNil())
	})

	It("supports multiple handlers for the same event type", func() {
		var a, b any
		bus.Subscribe("foo", func(e any) { a = e })
		bus.Subscribe("foo", func(e any) { b = e })
		bus.Publish("foo", "bar")
		Expect(a).To(Equal("bar"))
		Expect(b).To(Equal("bar"))
	})

	It("is concurrency-safe for Subscribe/Publish", func() {
		wg := sync.WaitGroup{}
		var count int32
		h := func(e any) { wg.Done(); atomic.AddInt32(&count, 1) }
		for range 100 {
			bus.Subscribe("foo", h)
		}
		wg.Add(100)
		go bus.Publish("foo", nil)
		wg.Wait()
		Expect(atomic.LoadInt32(&count)).To(Equal(int32(100)))
	})

	It("is concurrency-safe for Subscribe/Unsubscribe", func() {
		h := func(e any) {}
		wg := sync.WaitGroup{}
		for range 100 {
			wg.Add(1)
			go func() {
				bus.Subscribe("foo", h)
				bus.Unsubscribe("foo", h)
				wg.Done()
			}()
		}
		wg.Wait()
		bus.Publish("foo", 1) // Should not panic
	})

	It("does not panic if unsubscribing a handler not present", func() {
		h := func(e any) {}
		Expect(func() { bus.Unsubscribe("foo", h) }).NotTo(Panic())
	})

	It("does not panic if publishing to an event type with no handlers", func() {
		Expect(func() { bus.Publish("bar", 123) }).NotTo(Panic())
	})

	It("delivers all published events to handlers subscribed with \"*\"", func() {
		var received []any
		bus.Subscribe("*", func(e any) { received = append(received, e) })
		bus.Publish("foo", 1)
		bus.Publish("bar", 2)
		bus.Publish("baz", 3)
		Expect(received).To(ConsistOf(1, 2, 3))
	})

	It("delivers events to a handler subscribed to both \"*\" and a specific event type (double receive)", func() {
		var received []any
		h := func(e any) { received = append(received, e) }
		bus.Subscribe("*", h)
		bus.Subscribe("foo", h)
		bus.Publish("foo", 123)
		bus.Publish("bar", 456)
		// Handler should receive "foo" event twice (once for "*" and once for "foo"), and "bar" event once (for "*")
		Expect(received).To(ConsistOf(123, 123, 456))
	})

	It("does not deliver events to handlers after unsubscribing from \"*\"", func() {
		var received []any
		h := func(e any) { received = append(received, e) }
		bus.Subscribe("*", h)
		bus.Publish("foo", 1)
		bus.Unsubscribe("*", h)
		bus.Publish("bar", 2)
		Expect(received).To(ConsistOf(1))
	})

})
