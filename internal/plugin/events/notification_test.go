// Package events_test covers NotificationEvent construction, JSON
// serialisation, and the notification type/severity constants.
package events_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
)

var _ = Describe("NotificationEvent", func() {
	It("implements Event interface and sets fields", func() {
		data := events.NotificationEventData{
			ID:       "notif-1",
			Type:     events.NotificationTypeTaskFailed,
			Severity: events.NotificationSeverityError,
			Message:  "provider unavailable",
			Provider: "openai",
			Model:    "gpt-4o",
		}
		ts := time.Now().Add(-time.Minute)
		evt := events.NewNotificationEvent(data, ts)
		Expect(evt.EventType()).To(Equal(events.EventNotification))
		Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
		Expect(evt.Data).To(Equal(data))
	})

	It("defaults timestamp to now when not provided", func() {
		evt := events.NewNotificationEvent(events.NotificationEventData{
			ID:       "notif-2",
			Type:     events.NotificationTypeTurnComplete,
			Severity: events.NotificationSeverityInfo,
			Message:  "turn complete",
		})
		Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
	})

	It("serialises SSE fields id, type, severity, message, provider, model", func() {
		evt := events.NewNotificationEvent(events.NotificationEventData{
			ID:       "notif-3",
			Type:     events.NotificationTypeCooldown,
			Severity: events.NotificationSeverityWarning,
			Message:  "cooldown 5m",
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		})
		raw, err := json.Marshal(evt.Data)
		Expect(err).NotTo(HaveOccurred())
		var payload map[string]any
		Expect(json.Unmarshal(raw, &payload)).To(Succeed())
		Expect(payload).To(SatisfyAll(
			HaveKeyWithValue("ID", "notif-3"),
			HaveKeyWithValue("Type", "cooldown"),
			HaveKeyWithValue("Severity", "warning"),
			HaveKeyWithValue("Message", "cooldown 5m"),
			HaveKeyWithValue("Provider", "anthropic"),
			HaveKeyWithValue("Model", "claude-sonnet-4-6"),
		))
	})

	It("defines the four notification types and three severities", func() {
		Expect(events.NotificationTypeTurnComplete).To(Equal("turn_complete"))
		Expect(events.NotificationTypeTaskFailed).To(Equal("task_failed"))
		Expect(events.NotificationTypeCooldown).To(Equal("cooldown"))
		Expect(events.NotificationTypeFailover).To(Equal("failover"))
		Expect(events.NotificationSeverityInfo).To(Equal("info"))
		Expect(events.NotificationSeverityWarning).To(Equal("warning"))
		Expect(events.NotificationSeverityError).To(Equal("error"))
	})
})
