package streaming_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("SwarmEvent JSONL Persistence", func() {
	// Fixed reference time truncated to second precision (JSON round-trips
	// lose sub-second precision when the fractional part is zero).
	refTime := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)

	sampleEvents := func() []streaming.SwarmEvent {
		return []streaming.SwarmEvent{
			{
				ID:        "abc",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: refTime,
				AgentID:   "engineer",
				Metadata:  map[string]interface{}{"source_agent": "orchestrator"},
			},
			{
				ID:        "def",
				Type:      streaming.EventToolCall,
				Status:    "completed",
				Timestamp: refTime.Add(time.Second),
				AgentID:   "engineer",
				Metadata:  map[string]interface{}{"tool_name": "read"},
			},
		}
	}

	Describe("WriteEventsJSONL", func() {
		It("produces no output for an empty slice", func() {
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{})
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.Len()).To(Equal(0))
		})

		It("produces no output for a nil slice", func() {
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.Len()).To(Equal(0))
		})

		It("writes one JSON object per line for multiple events", func() {
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, sampleEvents())
			Expect(err).NotTo(HaveOccurred())

			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			Expect(lines).To(HaveLen(2))

			// Each line must be valid JSON.
			for _, line := range lines {
				Expect(json.Valid([]byte(line))).To(BeTrue(), "expected valid JSON: %s", line)
			}
		})

		It("encodes timestamps in RFC3339 format", func() {
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, sampleEvents()[:1])
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.String()).To(ContainSubstring("2026-04-16T10:00:00Z"))
		})

		It("encodes SwarmEventType as its string value", func() {
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, sampleEvents()[:1])
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.String()).To(ContainSubstring(`"delegation"`))
		})

		It("omits metadata when nil", func() {
			ev := streaming.SwarmEvent{
				ID:        "no-meta",
				Type:      streaming.EventPlan,
				Status:    "completed",
				Timestamp: refTime,
				AgentID:   "planner",
				Metadata:  nil,
			}
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.String()).NotTo(ContainSubstring("metadata"))
		})

		It("returns an error when an event cannot be marshalled", func() {
			ev := streaming.SwarmEvent{
				ID:        "bad-marshal",
				Type:      streaming.EventPlan,
				Status:    "started",
				Timestamp: refTime,
				AgentID:   "agent",
				Metadata:  map[string]interface{}{"bad": make(chan int)},
			}
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})
			Expect(err).To(HaveOccurred())
		})

		It("returns an error when the writer fails", func() {
			ev := streaming.SwarmEvent{
				ID:        "write-fail",
				Type:      streaming.EventPlan,
				Status:    "started",
				Timestamp: refTime,
				AgentID:   "agent",
			}
			w := &failWriter{err: errors.New("disk full")}
			err := streaming.WriteEventsJSONL(w, []streaming.SwarmEvent{ev})
			Expect(err).To(MatchError("disk full"))
		})

		It("omits empty metadata map via omitempty", func() {
			ev := streaming.SwarmEvent{
				ID:        "empty-meta",
				Type:      streaming.EventReview,
				Status:    "started",
				Timestamp: refTime,
				AgentID:   "reviewer",
				Metadata:  map[string]interface{}{},
			}
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})
			Expect(err).NotTo(HaveOccurred())
			// Go 1.25 json omitempty treats empty maps as empty.
			Expect(buf.String()).NotTo(ContainSubstring("metadata"))
		})
	})

	Describe("ReadEventsJSONL", func() {
		It("returns an empty slice for empty input", func() {
			events, err := streaming.ReadEventsJSONL(strings.NewReader(""))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())
		})

		It("skips blank lines", func() {
			input := "\n\n"
			events, err := streaming.ReadEventsJSONL(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())
		})

		It("skips corrupted lines and returns partial results", func() {
			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, sampleEvents())
			Expect(err).NotTo(HaveOccurred())

			// Insert a corrupted line between the two valid lines.
			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			corrupted := lines[0] + "\n" + "NOT VALID JSON\n" + lines[1] + "\n"

			events, err := streaming.ReadEventsJSONL(strings.NewReader(corrupted))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(2))
			Expect(events[0].ID).To(Equal("abc"))
			Expect(events[1].ID).To(Equal("def"))
		})

		It("handles input with only corrupted lines", func() {
			input := "garbage\n{broken\n"
			events, err := streaming.ReadEventsJSONL(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())
		})

		It("returns an error when the reader fails", func() {
			r := &failReader{err: errors.New("io timeout")}
			events, err := streaming.ReadEventsJSONL(r)
			Expect(err).To(MatchError("io timeout"))
			Expect(events).To(BeEmpty())
		})
	})

	Describe("Round-trip", func() {
		It("preserves all fields across write then read", func() {
			original := sampleEvents()

			var buf bytes.Buffer
			err := streaming.WriteEventsJSONL(&buf, original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.ReadEventsJSONL(&buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(HaveLen(len(original)))

			for i := range original {
				Expect(restored[i].ID).To(Equal(original[i].ID))
				Expect(restored[i].Type).To(Equal(original[i].Type))
				Expect(restored[i].Status).To(Equal(original[i].Status))
				Expect(restored[i].AgentID).To(Equal(original[i].AgentID))
				Expect(restored[i].Timestamp.Equal(original[i].Timestamp)).To(BeTrue(),
					"timestamp mismatch: got %v, want %v", restored[i].Timestamp, original[i].Timestamp)
			}
		})

		It("preserves timestamp precision across round-trip", func() {
			// Use a timestamp with nanosecond precision to verify no truncation.
			precise := time.Date(2026, 4, 16, 10, 30, 45, 123456789, time.UTC)
			ev := streaming.SwarmEvent{
				ID:        "precise",
				Type:      streaming.EventToolCall,
				Status:    "started",
				Timestamp: precise,
				AgentID:   "agent",
			}

			var buf bytes.Buffer
			Expect(streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})).To(Succeed())

			restored, err := streaming.ReadEventsJSONL(&buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(HaveLen(1))
			Expect(restored[0].Timestamp.Equal(precise)).To(BeTrue(),
				"timestamp precision lost: got %v, want %v", restored[0].Timestamp, precise)
		})

		It("preserves SwarmEventType string values across round-trip", func() {
			types := []streaming.SwarmEventType{
				streaming.EventDelegation,
				streaming.EventToolCall,
				streaming.EventPlan,
				streaming.EventReview,
			}
			events := make([]streaming.SwarmEvent, len(types))
			for i, t := range types {
				events[i] = streaming.SwarmEvent{
					ID:        string(t),
					Type:      t,
					Status:    "test",
					Timestamp: refTime,
					AgentID:   "agent",
				}
			}

			var buf bytes.Buffer
			Expect(streaming.WriteEventsJSONL(&buf, events)).To(Succeed())

			restored, err := streaming.ReadEventsJSONL(&buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(HaveLen(len(types)))
			for i, t := range types {
				Expect(restored[i].Type).To(Equal(t))
			}
		})

		It("handles nil metadata in round-trip", func() {
			ev := streaming.SwarmEvent{
				ID:        "nil-meta",
				Type:      streaming.EventPlan,
				Status:    "done",
				Timestamp: refTime,
				AgentID:   "planner",
				Metadata:  nil,
			}

			var buf bytes.Buffer
			Expect(streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})).To(Succeed())

			restored, err := streaming.ReadEventsJSONL(&buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(HaveLen(1))
			Expect(restored[0].Metadata).To(BeNil())
		})

		It("handles populated metadata in round-trip", func() {
			ev := streaming.SwarmEvent{
				ID:        "rich-meta",
				Type:      streaming.EventToolCall,
				Status:    "completed",
				Timestamp: refTime,
				AgentID:   "engineer",
				Metadata: map[string]interface{}{
					"tool_name": "bash",
					"duration":  float64(1234),
					"nested":    map[string]interface{}{"key": "value"},
				},
			}

			var buf bytes.Buffer
			Expect(streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})).To(Succeed())

			restored, err := streaming.ReadEventsJSONL(&buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(HaveLen(1))
			Expect(restored[0].Metadata).To(HaveKeyWithValue("tool_name", "bash"))
			Expect(restored[0].Metadata).To(HaveKeyWithValue("duration", float64(1234)))
			nested, ok := restored[0].Metadata["nested"].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(nested).To(HaveKeyWithValue("key", "value"))
		})
	})
})

// failWriter is an io.Writer that always returns the configured error.
type failWriter struct {
	err error
}

func (w *failWriter) Write([]byte) (int, error) { return 0, w.err }

// failReader is an io.Reader that always returns the configured error.
type failReader struct {
	err error
}

func (r *failReader) Read([]byte) (int, error) { return 0, r.err }
