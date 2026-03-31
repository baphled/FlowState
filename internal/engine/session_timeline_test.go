package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// subscribedTimelineEventTypes lists every event type the recorder subscribes to.
// These match the exact strings used by the engine's EventBus.
var subscribedTimelineEventTypes = []string{
	"session.created",
	"session.ended",
	"tool.execute.before",
	"tool.execute.after",
	"provider.error",
	"provider.rate_limited",
	"prompt.generated",
	"context.window.built",
	"tool.reasoning",
}

// TimelineEntry is one item in a SessionTimeline, representing either an
// EventBus event or a stream chunk, captured in arrival order.
type TimelineEntry struct {
	Seq       int    `json:"seq"`
	Kind      string `json:"kind"`
	EventType string `json:"event_type,omitempty"`
	Data      any    `json:"data"`
}

// SessionTimeline is an ordered slice of TimelineEntry values representing the
// full chronological sequence of events and stream chunks for a session.
type SessionTimeline []TimelineEntry

// GoldenSession is the JSON format used to save and load full session timelines.
type GoldenSession struct {
	Scenario string          `json:"scenario"`
	Entries  SessionTimeline `json:"entries"`
}

// SessionRecorder subscribes to an EventBus and drains a stream channel,
// appending entries in arrival order with a monotonic sequence counter.
// It is safe for concurrent use.
type SessionRecorder struct {
	mu      sync.Mutex
	entries SessionTimeline
	seq     atomic.Int64
}

// newSessionRecorder creates a ready-to-use SessionRecorder.
func newSessionRecorder() *SessionRecorder {
	return &SessionRecorder{}
}

// Subscribe registers the recorder as a handler for all session event types on bus.
func (r *SessionRecorder) Subscribe(bus *eventbus.EventBus) {
	for _, et := range subscribedTimelineEventTypes {
		bus.Subscribe(et, func(event any) {
			r.appendEvent(et, event)
		})
	}
}

// DrainStream consumes all chunks from ch, appending each as a timeline entry.
// It blocks until ch is closed.
func (r *SessionRecorder) DrainStream(ch <-chan provider.StreamChunk) {
	for chunk := range ch {
		r.appendChunk(chunk)
	}
}

// Timeline returns a snapshot of all recorded entries.
func (r *SessionRecorder) Timeline() SessionTimeline {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := make(SessionTimeline, len(r.entries))
	copy(snapshot, r.entries)
	return snapshot
}

// appendEvent records one EventBus event as a TimelineEntry.
func (r *SessionRecorder) appendEvent(eventType string, data any) {
	seq := int(r.seq.Add(1) - 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, TimelineEntry{
		Seq:       seq,
		Kind:      "event",
		EventType: eventType,
		Data:      data,
	})
}

// appendChunk records one stream chunk as a TimelineEntry.
func (r *SessionRecorder) appendChunk(chunk provider.StreamChunk) {
	seq := int(r.seq.Add(1) - 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, TimelineEntry{
		Seq:       seq,
		Kind:      "chunk",
		EventType: "",
		Data:      chunk,
	})
}

// goldenSessionPath returns the testdata path for the given scenario name.
func goldenSessionPath(scenario string) string {
	return filepath.Join("testdata", "session_"+scenario+".golden.json")
}

// saveGoldenSession writes a GoldenSession to disk in JSON format.
func saveGoldenSession(path string, gs GoldenSession) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(gs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// loadGoldenSession reads a GoldenSession from disk. Returns false if not found.
func loadGoldenSession(path string) (GoldenSession, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GoldenSession{}, false
	}
	var gs GoldenSession
	if err := json.Unmarshal(data, &gs); err != nil {
		return GoldenSession{}, false
	}
	return gs, true
}

// timelineManifest returns a minimal manifest suitable for timeline tests.
func timelineManifest() agent.Manifest {
	return agent.Manifest{
		ID:   "timeline-test",
		Name: "Timeline Test Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a helpful assistant.",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}
}

// mockBashTool is a minimal tool that simulates a bash execution for timeline tests.
type mockBashTool struct{}

func (t *mockBashTool) Name() string        { return "bash" }
func (t *mockBashTool) Description() string { return "Run a bash command" }
func (t *mockBashTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"command": {Type: "string", Description: "The command to run"},
		},
		Required: []string{"command"},
	}
}
func (t *mockBashTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Output: "hi"}, nil
}

var _ = Describe("Session Timeline", Label("integration"), func() {
	Describe("Scenario 1 — hello (no tool calls)", func() {
		Context("when the user sends 'hello' with no tool calls", func() {
			It("records session.created before any chunks and produces non-empty text", func() {
				ensureAnthropicE2EData()
				if anthropicE2ESkipped {
					Skip("No Anthropic API key and no golden file — skipping")
				}

				replay := &replayProvider{chunks: anthropicE2ECached}
				eng := engine.New(engine.Config{
					ChatProvider: replay,
					Manifest:     timelineManifest(),
				})

				recorder := newSessionRecorder()
				recorder.Subscribe(eng.EventBus())

				eng.SetContextStore(recall.NewEmptyContextStore("test-model"))

				mgr := session.NewManager(eng)
				sess, err := mgr.CreateSession("timeline-test")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())

				recorder.DrainStream(ch)

				timeline := recorder.Timeline()
				Expect(timeline).NotTo(BeEmpty())

				path := goldenSessionPath("hello")
				if _, exists := loadGoldenSession(path); !exists {
					saveGoldenSession(path, GoldenSession{
						Scenario: "hello",
						Entries:  timeline,
					})
				}

				var sessionCreatedIdx = -1
				var firstChunkIdx = -1
				var combinedContent string

				for i, entry := range timeline {
					if entry.Kind == "event" && entry.EventType == "session.created" && sessionCreatedIdx == -1 {
						sessionCreatedIdx = i
					}
					if entry.Kind == "chunk" && firstChunkIdx == -1 {
						firstChunkIdx = i
					}
					if entry.Kind == "chunk" {
						if chunk, ok := entry.Data.(provider.StreamChunk); ok {
							combinedContent += chunk.Content
						}
					}
				}

				Expect(sessionCreatedIdx).To(BeNumerically(">=", 0), "expected a session.created event")
				Expect(firstChunkIdx).To(BeNumerically(">=", 0), "expected at least one chunk")
				Expect(sessionCreatedIdx).To(BeNumerically("<", firstChunkIdx),
					"session.created must appear before any chunk")

				for _, entry := range timeline {
					if entry.Kind == "chunk" {
						if chunk, ok := entry.Data.(provider.StreamChunk); ok {
							Expect(chunk.ToolCall).To(BeNil(), "no tool calls expected in hello scenario")
						}
					}
				}

				Expect(combinedContent).ToNot(BeEmpty(), "combined chunk content must be non-empty")
			})
		})
	})

	Describe("Scenario 2 — tool call session", func() {
		Context("when the provider returns a bash tool call followed by a result", func() {
			It("records the tool call and result chunks in order with no forbidden tools", func() {
				seqProv := &streamSequenceProvider{
					name: "tool-call-seq-provider",
					sequences: [][]provider.StreamChunk{
						{
							{
								EventType: "tool_call",
								ToolCall: &provider.ToolCall{
									ID:        "call-1",
									Name:      "bash",
									Arguments: map[string]interface{}{"command": "echo hi"},
								},
							},
						},
						{
							{Content: "Done.", Done: true},
						},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: seqProv,
					Manifest:     timelineManifest(),
					Tools:        []tool.Tool{&mockBashTool{}},
				})

				recorder := newSessionRecorder()
				recorder.Subscribe(eng.EventBus())
				eng.SetContextStore(recall.NewEmptyContextStore("test-model"))

				mgr := session.NewManager(eng)
				sess, err := mgr.CreateSession("timeline-test")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "run bash echo hi")
				Expect(err).NotTo(HaveOccurred())

				recorder.DrainStream(ch)

				timeline := recorder.Timeline()
				Expect(timeline).NotTo(BeEmpty())

				var toolCallChunkFound bool
				var toolResultChunkFound bool
				var toolCallIdx = -1
				var toolResultIdx = -1
				forbiddenTools := map[string]bool{
					"skill_load": true,
					"todowrite":  true,
				}

				for i, entry := range timeline {
					if entry.Kind == "chunk" {
						if chunk, ok := entry.Data.(provider.StreamChunk); ok {
							if chunk.ToolCall != nil {
								Expect(forbiddenTools[chunk.ToolCall.Name]).To(BeFalse(),
									"forbidden tool call: %s", chunk.ToolCall.Name)
								if chunk.ToolCall.Name == "bash" && !toolCallChunkFound {
									toolCallChunkFound = true
									toolCallIdx = i
								}
							}
							if chunk.ToolResult != nil {
								if !toolResultChunkFound {
									toolResultChunkFound = true
									toolResultIdx = i
								}
							}
						}
					}
				}

				Expect(toolCallChunkFound).To(BeTrue(), "expected a chunk with ToolCall.Name=bash")
				Expect(toolResultChunkFound).To(BeTrue(), "expected a chunk with ToolResult")
				Expect(toolCallIdx).To(BeNumerically("<", toolResultIdx),
					"tool call chunk must precede tool result chunk")

				var bashResultContent string
				for _, entry := range timeline {
					if entry.Kind == "chunk" {
						if chunk, ok := entry.Data.(provider.StreamChunk); ok {
							if chunk.ToolResult != nil {
								bashResultContent = chunk.ToolResult.Content
								break
							}
						}
					}
				}
				Expect(bashResultContent).To(Equal("hi"))

				for _, entry := range timeline {
					if entry.Kind == "event" &&
						(entry.EventType == "tool.execute.before" || entry.EventType == "tool.execute.after") {
						Expect(entry.Data).NotTo(BeNil())
					}
				}
			})
		})
	})
})
