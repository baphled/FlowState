package eventlogger_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/eventlogger"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("EventLogger", func() {
	var (
		logger  *eventlogger.EventLogger
		bus     *eventbus.EventBus
		tmpDir  string
		logPath string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "eventlogger-test-*")
		Expect(err).NotTo(HaveOccurred())
		logPath = filepath.Join(tmpDir, "events.jsonl")
		bus = eventbus.NewEventBus()
	})

	AfterEach(func() {
		if logger != nil {
			Expect(logger.Close()).To(Succeed())
		}
		os.RemoveAll(tmpDir)
	})

	Describe("writing events as JSONL", func() {
		It("writes a single event as one JSONL line to the file", func() {
			logger = eventlogger.New(logPath, 10*1024*1024)
			Expect(logger.Start(bus)).To(Succeed())

			evt := events.NewSessionEvent(events.SessionEventData{
				SessionID: "sess-1",
				UserID:    "user-1",
				Action:    "created",
			}, time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC))

			bus.Publish("session.created", evt)

			data, err := os.ReadFile(logPath)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry).To(HaveKey("type"))
			Expect(entry["type"]).To(Equal("session"))
			Expect(entry).To(HaveKey("timestamp"))
			Expect(entry).To(HaveKey("data"))
		})

		It("writes multiple events as multiple JSONL lines", func() {
			logger = eventlogger.New(logPath, 10*1024*1024)
			Expect(logger.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
			bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
				SessionID: "sess-1", Action: "created",
			}, ts))
			bus.Publish("tool.execute.before", events.NewToolEvent(events.ToolEventData{
				ToolName: "bash", Args: map[string]any{"cmd": "ls"},
			}, ts))
			bus.Publish("provider.error", events.NewProviderEvent(events.ProviderEventData{
				ProviderName: "anthropic",
			}, ts))

			data, err := os.ReadFile(logPath)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(3))
		})

		It("writes each line as valid JSON", func() {
			logger = eventlogger.New(logPath, 10*1024*1024)
			Expect(logger.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
			bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
				SessionID: "sess-1", Action: "created",
			}, ts))
			bus.Publish("tool.execute.before", events.NewToolEvent(events.ToolEventData{
				ToolName: "echo",
			}, ts))

			data, err := os.ReadFile(logPath)
			Expect(err).NotTo(HaveOccurred())

			for _, line := range nonEmptyLines(data) {
				Expect(json.Valid([]byte(line))).To(BeTrue())
			}
		})
	})

	Describe("file rotation", func() {
		It("rotates when file exceeds max size", func() {
			smallMax := int64(100)
			logger = eventlogger.New(logPath, smallMax)
			Expect(logger.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
			for range 10 {
				bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
					SessionID: "sess-long-id-to-exceed-limit",
					UserID:    "user-long-id",
					Action:    "created",
					Details:   map[string]any{"key": "value-padding"},
				}, ts))
			}

			rotatedPath := logPath + ".1"
			Expect(rotatedPath).To(BeAnExistingFile())
		})

		It("preserves old file with .1 suffix after rotation", func() {
			smallMax := int64(100)
			logger = eventlogger.New(logPath, smallMax)
			Expect(logger.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
			for range 10 {
				bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
					SessionID: "sess-abc", Action: "created",
					Details: map[string]any{"padding": "extra-data-to-fill"},
				}, ts))
			}

			rotatedPath := logPath + ".1"
			Expect(rotatedPath).To(BeAnExistingFile())

			rotatedData, err := os.ReadFile(rotatedPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(nonEmptyLines(rotatedData)).NotTo(BeEmpty())
		})
	})

	Describe("concurrency safety", func() {
		It("handles concurrent event writes without races", func() {
			logger = eventlogger.New(logPath, 10*1024*1024)
			Expect(logger.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
			wg := sync.WaitGroup{}
			for range 50 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					bus.Publish("session.created", events.NewSessionEvent(events.SessionEventData{
						SessionID: "sess",
						Action:    "created",
					}, ts))
				}()
			}
			wg.Wait()

			data, err := os.ReadFile(logPath)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(50))
			for _, line := range lines {
				Expect(json.Valid([]byte(line))).To(BeTrue())
			}
		})
	})

	Describe("provider.request event logging", func() {
		It("writes a provider.request event as a JSONL line with correct type", func() {
			logger = eventlogger.New(logPath, 10*1024*1024)
			Expect(logger.Start(bus)).To(Succeed())

			ts := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
			bus.Publish("provider.request", events.NewProviderRequestEvent(events.ProviderRequestEventData{
				AgentID:      "test-agent",
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Request: provider.ChatRequest{
					Provider: "anthropic",
					Model:    "claude-3",
					Messages: []provider.Message{{Role: "user", Content: "hello"}},
				},
			}, ts))

			data, err := os.ReadFile(logPath)
			Expect(err).NotTo(HaveOccurred())

			lines := nonEmptyLines(data)
			Expect(lines).To(HaveLen(1))

			var entry map[string]any
			Expect(json.Unmarshal([]byte(lines[0]), &entry)).To(Succeed())
			Expect(entry["type"]).To(Equal("provider.request"))
			Expect(entry).To(HaveKey("data"))
		})
	})

	Describe("Close", func() {
		It("can be called safely", func() {
			logger = eventlogger.New(logPath, 10*1024*1024)
			Expect(logger.Start(bus)).To(Succeed())
			Expect(logger.Close()).To(Succeed())
			logger = nil
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
