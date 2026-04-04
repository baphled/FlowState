package events_test

import (
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
)

var _ = Describe("Events", func() {
	// --- Existing tests remain here ---

	Describe("SessionResumedEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.SessionResumedEventData{
				SessionID: "sess42",
				UserID:    "user42",
				Details:   map[string]any{"foo": "bar"},
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewSessionResumedEvent(data, ts)
			Expect(evt.EventType()).To(Equal("session.resumed"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("serialises to JSON", func() {
			data := events.SessionResumedEventData{
				SessionID: "sess42",
				UserID:    "user42",
				Details:   map[string]any{"foo": "bar"},
			}
			evt := events.NewSessionResumedEvent(data, time.Now())
			raw, err := json.Marshal(evt)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["data"]).NotTo(BeNil())
		})
	})

	Describe("ToolExecuteErrorEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ToolExecuteErrorEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "fail"},
				Error:    errors.New("tool failed"),
			}
			evt := events.NewToolExecuteErrorEvent(data)
			Expect(evt.EventType()).To(Equal("tool.execute.error"))
			Expect(evt.Data.ToolName).To(Equal("echo"))
			Expect(evt.Data.Error).To(MatchError("tool failed"))
		})

		It("serialises to JSON with error as string", func() {
			data := events.ToolExecuteErrorEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "fail"},
				Error:    errors.New("tool failed"),
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["error"]).To(Equal("tool failed"))
		})
	})

	Describe("ToolExecuteResultEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ToolExecuteResultEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "ok"},
				Result:   "ok",
			}
			evt := events.NewToolExecuteResultEvent(data)
			Expect(evt.EventType()).To(Equal("tool.execute.result"))
			Expect(evt.Data.Result).To(Equal("ok"))
		})

		It("serialises to JSON", func() {
			data := events.ToolExecuteResultEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "ok"},
				Result:   "ok",
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["result"]).To(Equal("ok"))
		})
	})

	Describe("ProviderRequestRetryEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ProviderRequestRetryEventData{
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Reason:       "rate limit",
				Attempt:      2,
			}
			evt := events.NewProviderRequestRetryEvent(data)
			Expect(evt.EventType()).To(Equal("provider.request.retry"))
			Expect(evt.Data.ProviderName).To(Equal("anthropic"))
			Expect(evt.Data.Attempt).To(Equal(2))
		})

		It("serialises to JSON", func() {
			data := events.ProviderRequestRetryEventData{
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Reason:       "rate limit",
				Attempt:      2,
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["reason"]).To(Equal("rate limit"))
			Expect(parsed["attempt"]).To(Equal(float64(2)))
		})
	})

})
