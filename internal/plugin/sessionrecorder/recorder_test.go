package sessionrecorder_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/sessionrecorder"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Recorder", func() {
	var (
		recorder *sessionrecorder.Recorder
		bus      *eventbus.EventBus
		tmpDir   string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "sessionrecorder-test-*")
		Expect(err).NotTo(HaveOccurred())
		bus = eventbus.NewEventBus()
		recorder = sessionrecorder.New(tmpDir)
	})

	AfterEach(func() {
		if recorder != nil {
			Expect(recorder.Close()).To(Succeed())
		}
		os.RemoveAll(tmpDir)
	})

	Describe("plugin interface", func() {
		It("returns the correct name", func() {
			Expect(recorder.Name()).To(Equal("session-recorder"))
		})

		It("returns the correct version", func() {
			Expect(recorder.Version()).To(Equal("v0.0.0"))
		})

		It("initialises without error", func() {
			Expect(recorder.Init()).To(Succeed())
		})
	})

	Describe("writing events as JSONL", func() {
		It("writes a session event to the correct per-session file", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
				SessionID: "sess-abc",
				UserID:    "user-1",
				Action:    "created",
			}, ts))

			sessionFile := filepath.Join(tmpDir, "sess-abc.jsonl")
			Expect(sessionFile).To(BeAnExistingFile())

			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal("session"))
			Expect(entry).To(HaveKey("seq"))
			Expect(entry).To(HaveKey("timestamp"))
			Expect(entry).To(HaveKey("data"))
		})

		It("writes tool events to the correct per-session file", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("tool.execute.before", events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-xyz",
				ToolName:  "bash",
				Args:      map[string]any{"cmd": "ls"},
			}, ts))

			sessionFile := filepath.Join(tmpDir, "sess-xyz.jsonl")
			Expect(sessionFile).To(BeAnExistingFile())

			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal("tool"))
		})

		It("writes events without sessionID to the global file", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("prompt.generated", events.NewPromptEvent(events.PromptEventData{
				AgentID:    "test-agent",
				FullPrompt: "Hello world",
				TokenCount: 10,
			}, ts))

			globalFile := filepath.Join(tmpDir, "global.jsonl")
			Expect(globalFile).To(BeAnExistingFile())

			data, err := os.ReadFile(globalFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))
		})

		It("writes provider error events to the correct per-session file", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("provider.error", events.NewProviderErrorEvent(events.ProviderErrorEventData{
				SessionID:    "sess-pe",
				ProviderName: "anthropic",
			}, ts))

			sessionFile := filepath.Join(tmpDir, "sess-pe.jsonl")
			Expect(sessionFile).To(BeAnExistingFile())

			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal("provider.error"))
		})

		It("writes provider response events to the correct per-session file", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("provider.response", events.NewProviderResponseEvent(events.ProviderResponseEventData{
				SessionID:       "sess-pr",
				AgentID:         "test-agent",
				ProviderName:    "anthropic",
				ModelName:       "claude-3",
				ResponseContent: "Hello",
				ToolCalls:       0,
				DurationMS:      150,
			}, ts))

			sessionFile := filepath.Join(tmpDir, "sess-pr.jsonl")
			Expect(sessionFile).To(BeAnExistingFile())

			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal("provider.response"))
		})

		It("writes provider error events with typed event to the correct per-session file", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("provider.error", events.NewProviderErrorEvent(events.ProviderErrorEventData{
				SessionID:    "sess-perr",
				AgentID:      "test-agent",
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Error:        errors.New("rate limited"),
				Phase:        "failover",
			}, ts))

			sessionFile := filepath.Join(tmpDir, "sess-perr.jsonl")
			Expect(sessionFile).To(BeAnExistingFile())

			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal("provider.error"))
		})
	})

	Describe("RecordChunk", func() {
		It("writes chunk entries with correct format", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			recorder.RecordChunk("sess-chunk", provider.StreamChunk{
				Content: "Hello",
			})

			sessionFile := filepath.Join(tmpDir, "sess-chunk.jsonl")
			Expect(sessionFile).To(BeAnExistingFile())

			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("chunk"))
			Expect(entry["seq"]).To(BeNumerically("==", 0))
			Expect(entry).To(HaveKey("timestamp"))
			Expect(entry).To(HaveKey("data"))
			Expect(entry).NotTo(HaveKey("event_type"))
		})

		It("increments sequence numbers per session", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			recorder.RecordChunk("sess-seq", provider.StreamChunk{Content: "a"})
			recorder.RecordChunk("sess-seq", provider.StreamChunk{Content: "b"})
			recorder.RecordChunk("sess-seq", provider.StreamChunk{Content: "c"})

			sessionFile := filepath.Join(tmpDir, "sess-seq.jsonl")
			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(3))

			for i, line := range lines {
				var entry map[string]any
				Expect(json.Unmarshal([]byte(line), &entry)).To(Succeed())
				Expect(entry["seq"]).To(BeNumerically("==", i))
			}
		})
	})

	Describe("per-session file isolation", func() {
		It("writes events from different sessions to different files", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
				SessionID: "sess-one",
				Action:    "created",
			}, ts))
			bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
				SessionID: "sess-two",
				Action:    "created",
			}, ts))

			recorder.RecordChunk("sess-one", provider.StreamChunk{Content: "chunk-one"})
			recorder.RecordChunk("sess-two", provider.StreamChunk{Content: "chunk-two-a"})
			recorder.RecordChunk("sess-two", provider.StreamChunk{Content: "chunk-two-b"})

			fileOne := filepath.Join(tmpDir, "sess-one.jsonl")
			fileTwo := filepath.Join(tmpDir, "sess-two.jsonl")

			dataOne, err := os.ReadFile(fileOne)
			Expect(err).NotTo(HaveOccurred())
			linesOne := nonEmptyLines(dataOne)
			Expect(linesOne).To(HaveLen(2))

			dataTwo, err := os.ReadFile(fileTwo)
			Expect(err).NotTo(HaveOccurred())
			linesTwo := nonEmptyLines(dataTwo)
			Expect(linesTwo).To(HaveLen(3))
		})
	})

	Describe("Close", func() {
		It("flushes and closes all open file handles", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			recorder.RecordChunk("sess-close", provider.StreamChunk{Content: "data"})

			Expect(recorder.Close()).To(Succeed())
			recorder = nil

			sessionFile := filepath.Join(tmpDir, "sess-close.jsonl")
			Expect(sessionFile).To(BeAnExistingFile())

			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())
			Expect(nonEmptyLines(data)).To(HaveLen(1))
		})

		It("can be called multiple times safely", func() {
			Expect(recorder.Start(bus)).To(Succeed())
			Expect(recorder.Close()).To(Succeed())
			Expect(recorder.Close()).To(Succeed())
			recorder = nil
		})
	})

	Describe("BackgroundTask event data extraction", func() {
		It("writes BackgroundTaskStartedEvent data field, not the raw event", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish(events.EventBackgroundTaskStarted, events.NewBackgroundTaskStartedEvent(events.BackgroundTaskEventData{
				TaskID: "task-started-1",
				Name:   "my-background-task",
				Status: "running",
			}, ts))

			globalFile := filepath.Join(tmpDir, "global.jsonl")
			Expect(globalFile).To(BeAnExistingFile())

			data, err := os.ReadFile(globalFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal(events.EventBackgroundTaskStarted))
			payload, ok := entry["data"].(map[string]any)
			Expect(ok).To(BeTrue(), "data should be a JSON object (BackgroundTaskEventData), not a raw event")
			Expect(payload["TaskID"]).To(Equal("task-started-1"))
		})

		It("writes BackgroundTaskCompletedEvent data field, not the raw event", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish(events.EventBackgroundTaskCompleted, events.NewBackgroundTaskCompletedEvent(events.BackgroundTaskEventData{
				TaskID: "task-completed-1",
				Name:   "my-background-task",
				Status: "completed",
			}, ts))

			globalFile := filepath.Join(tmpDir, "global.jsonl")
			Expect(globalFile).To(BeAnExistingFile())

			data, err := os.ReadFile(globalFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal(events.EventBackgroundTaskCompleted))
			payload, ok := entry["data"].(map[string]any)
			Expect(ok).To(BeTrue(), "data should be a JSON object (BackgroundTaskEventData), not a raw event")
			Expect(payload["TaskID"]).To(Equal("task-completed-1"))
		})

		It("writes BackgroundTaskFailedEvent data field, not the raw event", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish(events.EventBackgroundTaskFailed, events.NewBackgroundTaskFailedEvent(events.BackgroundTaskEventData{
				TaskID: "task-failed-1",
				Name:   "my-background-task",
				Status: "failed",
				Error:  "context deadline exceeded",
			}, ts))

			globalFile := filepath.Join(tmpDir, "global.jsonl")
			Expect(globalFile).To(BeAnExistingFile())

			data, err := os.ReadFile(globalFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["kind"]).To(Equal("event"))
			Expect(entry["event_type"]).To(Equal(events.EventBackgroundTaskFailed))
			payload, ok := entry["data"].(map[string]any)
			Expect(ok).To(BeTrue(), "data should be a JSON object (BackgroundTaskEventData), not a raw event")
			Expect(payload["TaskID"]).To(Equal("task-failed-1"))
			Expect(payload["Error"]).To(Equal("context deadline exceeded"))
		})
	})

	Describe("sequence numbers across events and chunks", func() {
		It("maintains a monotonic sequence across mixed events and chunks", func() {
			Expect(recorder.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
				SessionID: "sess-mixed",
				Action:    "created",
			}, ts))

			recorder.RecordChunk("sess-mixed", provider.StreamChunk{Content: "a"})
			recorder.RecordChunk("sess-mixed", provider.StreamChunk{Content: "b"})

			sessionFile := filepath.Join(tmpDir, "sess-mixed.jsonl")
			data, err := os.ReadFile(sessionFile)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(3))

			for i, line := range lines {
				var entry map[string]any
				Expect(json.Unmarshal([]byte(line), &entry)).To(Succeed())
				Expect(entry["seq"]).To(BeNumerically("==", i))
			}
		})
	})
})

func nonEmptyLines(data []byte) []string {
	raw := strings.Split(strings.TrimSpace(string(data)), "\n")
	var result []string
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}
