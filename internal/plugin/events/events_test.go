package events_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
)

var _ = Describe("Events", func() {
	Describe("SessionEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.SessionEventData{
				SessionID: "sess1",
				UserID:    "user1",
				Action:    "start",
				Details:   map[string]any{"foo": "bar"},
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewSessionEvent(data, ts)
			Expect(evt.EventType()).To(Equal("session"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})
	})

	Describe("ToolEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ToolEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "hi"},
				Result:   "hi",
				Error:    nil,
			}
			evt := events.NewToolEvent(data)
			Expect(evt.EventType()).To(Equal("tool"))
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
			Expect(evt.Data).To(Equal(data))
		})
	})

	Describe("ProviderEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ProviderEventData{
				ProviderName: "anthropic",
				Request:      "foo",
				Response:     "bar",
				Error:        nil,
			}
			evt := events.NewProviderEvent(data)
			Expect(evt.EventType()).To(Equal("provider"))
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
			Expect(evt.Data).To(Equal(data))
		})
	})

	Describe("BaseEvent", func() {
		It("returns correct eventType and timestamp via embedding", func() {
			ts := time.Now().Add(-time.Hour)
			data := events.SessionEventData{SessionID: "s", UserID: "u", Action: "act", Details: nil}
			evt := events.NewSessionEvent(data, ts)
			Expect(evt.EventType()).To(Equal("session"))
			Expect(evt.Timestamp()).To(Equal(ts))
		})
	})
})
