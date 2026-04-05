package events_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
)

var _ = Describe("legacy EventType divergences", func() {
	Describe("NewSessionEvent", func() {
		It("EventType() returns 'session' but is published to 'session.created' or 'session.ended'", func() {
			evt := events.NewSessionEvent(events.SessionEventData{})
			Expect(evt.EventType()).To(Equal("session"))
			Expect(evt.EventType()).NotTo(Equal("session.created"))
			Expect(evt.EventType()).NotTo(Equal("session.ended"))
		})
	})

	Describe("NewToolEvent", func() {
		It("EventType() returns 'tool' but is published to 'tool.execute.before' or 'tool.execute.after'", func() {
			evt := events.NewToolEvent(events.ToolEventData{})
			Expect(evt.EventType()).To(Equal("tool"))
			Expect(evt.EventType()).NotTo(Equal("tool.execute.before"))
			Expect(evt.EventType()).NotTo(Equal("tool.execute.after"))
		})
	})

	Describe("NewProviderEvent", func() {
		It("EventType() returns 'provider' but is published to 'provider.rate_limited'", func() {
			evt := events.NewProviderEvent(events.ProviderEventData{})
			Expect(evt.EventType()).To(Equal("provider"))
			Expect(evt.EventType()).NotTo(Equal("provider.rate_limited"))
		})
	})

	Describe("NewPromptEvent", func() {
		It("EventType() returns 'prompt' but is published to 'prompt.generated'", func() {
			evt := events.NewPromptEvent(events.PromptEventData{})
			Expect(evt.EventType()).To(Equal("prompt"))
			Expect(evt.EventType()).NotTo(Equal("prompt.generated"))
		})
	})

	Describe("NewContextWindowEvent", func() {
		It("EventType() returns 'context.window' but is published to 'context.window.built'", func() {
			evt := events.NewContextWindowEvent(events.ContextWindowEventData{})
			Expect(evt.EventType()).To(Equal("context.window"))
			Expect(evt.EventType()).NotTo(Equal("context.window.built"))
		})
	})
})
