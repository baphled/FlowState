package eventlogger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

// subscribedEventTypes lists all event type strings the logger subscribes to.
var subscribedEventTypes = []string{"session", "tool", "provider"}

// defaultMaxRotated defines the maximum number of rotated files to keep.
const defaultMaxRotated = 5

// logEntry is the JSONL wrapper written to the output file for each event.
//
// Expected: marshalled to JSON and written as a single line.
// Returns: struct for JSON marshalling.
// Side effects: none.
type logEntry struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// EventLogger subscribes to an EventBus and writes all events as JSONL lines
// to a file with size-based rotation.
//
// Expected:
//   - Created via New with a file path and maximum file size.
//   - Start must be called to begin subscribing and writing.
//   - Close must be called to release the file handle.
//
// Returns: struct for event logging.
// Side effects: writes to the filesystem.
type EventLogger struct {
	path    string
	maxSize int64
	mu      sync.Mutex
	file    *os.File
}

// New creates a new EventLogger targeting the given path with a maximum file
// size in bytes before rotation.
//
// Expected:
//   - path is a valid filesystem path for the JSONL output file.
//   - maxSize is the byte threshold that triggers rotation.
//
// Returns: pointer to a new EventLogger.
// Side effects: none.
func New(path string, maxSize int64) *EventLogger {
	return &EventLogger{
		path:    path,
		maxSize: maxSize,
	}
}

// Start subscribes the logger to all event types on the given EventBus and
// opens the output file for writing.
//
// Expected:
//   - bus is a non-nil EventBus.
//   - The parent directory for path exists or is creatable.
//
// Returns: error if the file cannot be opened.
// Side effects: opens a file handle; subscribes handlers on the bus.
func (l *EventLogger) Start(bus *eventbus.EventBus) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	l.file = f

	for _, eventType := range subscribedEventTypes {
		et := eventType
		bus.Subscribe(et, func(event any) {
			l.handleEvent(event)
		})
	}

	return nil
}

// Close releases the underlying file handle.
//
// Expected:
//   - Called after Start; safe to call multiple times.
//
// Returns: error if the file cannot be closed.
// Side effects: closes the file handle.
func (l *EventLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return nil
	}

	err := l.file.Close()
	l.file = nil

	if err != nil {
		return fmt.Errorf("closing log file: %w", err)
	}

	return nil
}

// handleEvent marshals an event as JSONL, appends it to the output file, and
// triggers rotation if the file exceeds the maximum size. Errors are
// silently discarded because the EventBus handler cannot propagate them.
//
// Expected: event implements events.Event or is any serialisable value.
// Returns: none.
// Side effects: writes to the file; may rotate the file.
func (l *EventLogger) handleEvent(event any) {
	entry := buildLogEntry(event)

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return
	}

	if _, err := l.file.Write(line); err != nil {
		return
	}

	l.rotateIfNeeded()
}

// buildLogEntry constructs a logEntry from an event, extracting type,
// timestamp, and data fields where available.
//
// Expected: event may implement events.Event or be any value.
// Returns: logEntry ready for JSON marshalling.
// Side effects: none.
func buildLogEntry(event any) logEntry {
	entry := logEntry{
		Timestamp: time.Now(),
		Data:      event,
	}

	if ev, ok := event.(events.Event); ok {
		entry.Type = ev.EventType()
		entry.Timestamp = ev.Timestamp()
		entry.Data = extractEventData(ev)
	}

	return entry
}

// extractEventData returns the typed data payload from a known event type,
// or the event itself for unknown types.
//
// Expected: ev is a non-nil events.Event.
// Returns: the event's data payload.
// Side effects: none.
func extractEventData(ev events.Event) any {
	switch e := ev.(type) {
	case *events.SessionEvent:
		return e.Data
	case *events.ToolEvent:
		return e.Data
	case *events.ProviderEvent:
		return e.Data
	default:
		return ev
	}
}

// rotateIfNeeded checks the current file size and rotates if it exceeds the
// configured maximum. Must be called while holding l.mu.
//
// Expected: l.mu is held by the caller; l.file is non-nil.
// Returns: none.
// Side effects: may rename the current file and open a fresh one.
func (l *EventLogger) rotateIfNeeded() {
	info, err := l.file.Stat()
	if err != nil {
		return
	}

	if info.Size() < l.maxSize {
		return
	}

	l.rotate()
}

// rotate closes the current file, shifts existing rotated files, renames the
// current file to .1, and opens a fresh output file. Must be called while
// holding l.mu.
//
// Expected: l.mu is held by the caller; l.file is non-nil.
// Returns: none.
// Side effects: renames files on disk; opens a new file handle.
func (l *EventLogger) rotate() {
	if err := l.file.Close(); err != nil {
		return
	}

	shiftRotatedFiles(l.path, defaultMaxRotated)

	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return
	}
	l.file = f
}

// shiftRotatedFiles moves existing rotated files up by one index, discarding
// the oldest if it exceeds maxKeep. Missing files are silently skipped.
//
// Expected: basePath is the base log file path; maxKeep >= 1.
// Returns: none.
// Side effects: renames or removes files on disk.
func shiftRotatedFiles(basePath string, maxKeep int) {
	oldest := fmt.Sprintf("%s.%d", basePath, maxKeep)
	removeIfExists(oldest)

	for i := maxKeep - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", basePath, i)
		dst := fmt.Sprintf("%s.%d", basePath, i+1)
		renameIfExists(src, dst)
	}
}

// removeIfExists removes a file, ignoring errors when the file does not exist.
//
// Expected: path is a filesystem path.
// Returns: none.
// Side effects: removes the file if it exists.
func removeIfExists(path string) {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
}

// renameIfExists renames a file, ignoring errors when the source does not exist.
//
// Expected: src and dst are filesystem paths.
// Returns: none.
// Side effects: renames the file if it exists.
func renameIfExists(src, dst string) {
	err := os.Rename(src, dst)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
}
