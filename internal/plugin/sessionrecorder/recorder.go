package sessionrecorder

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
)

// subscribedEventTypes lists all event type strings the recorder subscribes to.
var subscribedEventTypes = []string{
	"session.created",
	"session.ended",
	"tool.execute.before",
	"tool.execute.after",
	"provider.error",
	"provider.rate_limited",
	"provider.request",
	"provider.response",
	"agent.switched",
	"prompt.generated",
	"context.window.built",
	"tool.reasoning",
	"background.task.started",
	"background.task.completed",
	"background.task.failed",
}

// defaultSessionID is used for events that do not carry a session identifier.
const defaultSessionID = "global"

// TimelineEntry represents a single chronological entry in a session's timeline.
//
// Expected: marshalled to JSON and written as a single JSONL line.
// Returns: struct for JSON marshalling.
// Side effects: none.
type TimelineEntry struct {
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	EventType string    `json:"event_type,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// Recorder captures full session timelines to per-session JSONL files.
//
// Expected:
//   - Created via New with a base directory path.
//   - Start must be called to begin subscribing to EventBus events.
//   - RecordChunk records individual stream chunks.
//   - Close must be called to release all file handles.
//
// Returns: struct for session recording.
// Side effects: writes to the filesystem.
type Recorder struct {
	baseDir string
	mu      sync.Mutex
	files   map[string]*os.File
	seq     sync.Map
}

// New creates a new Recorder targeting the given base directory.
//
// Expected:
//   - baseDir is a valid filesystem path for JSONL output files.
//
// Returns: pointer to a new Recorder.
// Side effects: none.
func New(baseDir string) *Recorder {
	return &Recorder{
		baseDir: baseDir,
		files:   make(map[string]*os.File),
	}
}

// Init initialises the session recorder.
//
// Returns: nil.
// Side effects: none.
func (r *Recorder) Init() error {
	return nil
}

// Name returns the plugin name.
//
// Returns: the builtin plugin name.
// Side effects: none.
func (r *Recorder) Name() string {
	return "session-recorder"
}

// Version returns the plugin version.
//
// Returns: the builtin plugin version string.
// Side effects: none.
func (r *Recorder) Version() string {
	return "v0.0.0"
}

// Start subscribes the recorder to all event types on the given EventBus
// and ensures the base directory exists.
//
// Expected:
//   - bus is a non-nil EventBus.
//
// Returns: error if the base directory cannot be created.
// Side effects: subscribes handlers on the bus; creates the base directory.
func (r *Recorder) Start(bus *eventbus.EventBus) error {
	if err := os.MkdirAll(r.baseDir, 0o750); err != nil {
		return fmt.Errorf("creating session recording directory: %w", err)
	}

	for _, eventType := range subscribedEventTypes {
		et := eventType
		bus.Subscribe(et, func(event any) {
			r.handleEvent(event)
		})
	}

	return nil
}

// RecordChunk writes a stream chunk entry to the session's timeline file.
//
// Expected:
//   - sessionID identifies the session receiving the chunk.
//   - chunk is a valid StreamChunk from the provider.
//
// Returns: none.
// Side effects: writes to the session's JSONL file.
func (r *Recorder) RecordChunk(sessionID string, chunk provider.StreamChunk) {
	entry := TimelineEntry{
		Seq:       r.nextSeq(sessionID),
		Kind:      "chunk",
		Timestamp: time.Now(),
		Data:      chunk,
	}
	r.writeEntry(sessionID, entry)
}

// Close flushes and closes all open file handles.
//
// Returns: error if any file cannot be closed.
// Side effects: closes all file handles.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for id, f := range r.files {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("closing session file %q: %w", id, err)
		}
		delete(r.files, id)
	}

	return firstErr
}

// handleEvent extracts the session ID and event type from a bus event,
// then writes a timeline entry to the appropriate session file.
//
// Expected: event is a valid bus event (implements events.Event or is untyped).
// Returns: nothing.
// Side effects: writes to the session JSONL file via writeEntry.
func (r *Recorder) handleEvent(event any) {
	sessionID, eventType, data := extractEventInfo(event)
	entry := TimelineEntry{
		Seq:       r.nextSeq(sessionID),
		Kind:      "event",
		EventType: eventType,
		Timestamp: eventTimestamp(event),
		Data:      data,
	}
	r.writeEntry(sessionID, entry)
}

// writeEntry marshals a TimelineEntry to JSON and appends it as a single
// line to the session's JSONL file.
//
// Expected: sessionID is a non-empty string; entry is fully populated.
// Returns: nothing.
// Side effects: acquires r.mu, opens or reuses a file handle, writes to disk.
func (r *Recorder) writeEntry(sessionID string, entry TimelineEntry) {
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()

	f, err := r.fileFor(sessionID)
	if err != nil {
		return
	}

	if _, err := f.Write(line); err != nil {
		slog.Warn("failed to write session entry", "err", err)
	}
}

// fileFor returns the open file handle for a session, creating it if necessary.
// Must be called while holding r.mu.
//
// Expected: r.mu is held by the caller; sessionID is non-empty.
// Returns: an open *os.File for appending, or an error if the file cannot be opened.
// Side effects: may create a new file on disk and store the handle in r.files.
func (r *Recorder) fileFor(sessionID string) (*os.File, error) {
	if f, ok := r.files[sessionID]; ok {
		return f, nil
	}

	path := filepath.Join(r.baseDir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("opening session file: %w", err)
	}

	r.files[sessionID] = f
	return f, nil
}

// nextSeq returns the next monotonic sequence number for a session.
//
// Expected: sessionID is a non-empty string identifying the session.
// Returns: the next sequence number (0-based, increments atomically).
// Side effects: none.
func (r *Recorder) nextSeq(sessionID string) int64 {
	val, _ := r.seq.LoadOrStore(sessionID, &atomic.Int64{})
	counter, ok := val.(*atomic.Int64)
	if !ok {
		return 0
	}
	return counter.Add(1) - 1
}

// extractEventInfo returns the session ID, event type string, and data
// payload for a given event. Events without a session ID use the default
// "global" identifier.
//
// Expected: event is any value; nil is handled gracefully.
// Returns: session ID (defaults to "global"), event type string, and data payload.
// Side effects: none.
func extractEventInfo(event any) (sessionID, eventType string, data any) {
	sessionID = defaultSessionID

	ev, ok := event.(events.Event)
	if !ok {
		return sessionID, "", event
	}

	eventType = ev.EventType()
	data = extractEventData(ev)

	if id := extractSessionID(ev); id != "" {
		sessionID = id
	}

	return sessionID, eventType, data
}

// extractEventData returns the typed data payload from a known event type.
//
// Expected: ev is a non-nil events.Event.
// Returns: the event's Data field, or the event itself for unrecognised types.
// Side effects: none.
func extractEventData(ev events.Event) any {
	switch e := ev.(type) {
	case *events.SessionEvent:
		return e.Data
	case *events.ToolEvent:
		return e.Data
	case *events.ProviderEvent:
		return e.Data
	case *events.ProviderRequestEvent:
		return e.Data
	case *events.ProviderResponseEvent:
		return e.Data
	case *events.ProviderErrorEvent:
		return e.Data
	case *events.AgentSwitchedEvent:
		return e.Data
	case *events.PromptEvent:
		return e.Data
	case *events.ContextWindowEvent:
		return e.Data
	case *events.ToolReasoningEvent:
		return e.Data
	default:
		return ev
	}
}

// extractSessionID returns the session ID embedded in a known event type,
// or an empty string if the event type does not carry one.
//
// Expected: ev is a non-nil events.Event.
// Returns: session ID string, or empty if not present.
// Side effects: none.
func extractSessionID(ev events.Event) string {
	switch e := ev.(type) {
	case *events.SessionEvent:
		return e.Data.SessionID
	case *events.ToolEvent:
		return e.Data.SessionID
	case *events.ProviderEvent:
		return e.Data.SessionID
	case *events.ProviderRequestEvent:
		return e.Data.SessionID
	case *events.ProviderResponseEvent:
		return e.Data.SessionID
	case *events.ProviderErrorEvent:
		return e.Data.SessionID
	case *events.AgentSwitchedEvent:
		if e.Data.SessionID != "" {
			return e.Data.SessionID
		}
		if e.Data.ToAgent != "" {
			return e.Data.ToAgent
		}
		return e.Data.FromAgent
	case *events.PromptEvent:
		return e.Data.SessionID
	case *events.ContextWindowEvent:
		return e.Data.SessionID
	case *events.ToolReasoningEvent:
		return e.Data.SessionID
	default:
		return ""
	}
}

// eventTimestamp returns the timestamp from an events.Event, falling back
// to the current time for unknown types.
//
// Expected: event is any value; non-Event types fall back to time.Now().
// Returns: the event timestamp or the current time.
// Side effects: none.
func eventTimestamp(event any) time.Time {
	if ev, ok := event.(events.Event); ok {
		return ev.Timestamp()
	}
	return time.Now()
}
