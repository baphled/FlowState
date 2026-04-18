package streaming

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
)

// maxJSONLineBytes is the raised bufio.Scanner line limit applied by
// ReadEventsJSONL. The previous default of 64 KiB truncated events whose
// metadata carried large tool-result bodies; 1 MiB is well above the
// observed 500 KiB ceiling while bounding memory pressure on replay.
const maxJSONLineBytes = 1 * 1024 * 1024

// scannerInitialBuffer is the starting buffer size handed to
// bufio.Scanner.Buffer. The scanner grows up to maxJSONLineBytes as needed.
const scannerInitialBuffer = 64 * 1024

// syncHook is invoked by AppendSwarmEvent and CompactSwarmEvents every time
// they call *os.File.Sync. Tests use SetSyncHookForTest to assert durability
// without faking the filesystem. Production code leaves this as a no-op.
var syncHook func()

// SetSyncHookForTest installs a hook that is called every time the
// persistence layer calls Sync on an *os.File. The previous hook is
// returned so callers can restore it in DeferCleanup. Passing nil restores
// the no-op default.
//
// Expected:
//   - hook is either nil (disable) or a function that runs quickly — it
//     executes on the goroutine performing the Sync and must not block.
//
// Returns:
//   - The previously installed hook (nil when none was set).
//
// Side effects:
//   - Mutates package-level state; intended for tests only.
func SetSyncHookForTest(hook func()) func() {
	prev := syncHook
	syncHook = hook
	return prev
}

// malformedLineCount counts JSONL lines that failed to unmarshal. Surfaced
// via MalformedLineCount() so operators and tests can detect sustained
// corruption without wiring a full observability refactor. Thread-safe via
// atomic load/store.
var malformedLineCount atomic.Int64

// futureSchemaLineCount counts JSONL lines that parsed successfully but
// declared a SchemaVersion greater than the current runtime's
// CurrentSchemaVersion. Forward-compat: the event passes through unchanged
// so the user does not lose data; the count signals operator attention.
var futureSchemaLineCount atomic.Int64

// MalformedLineCount returns the process-wide count of JSONL lines that
// failed to unmarshal during ReadEventsJSONL. The counter only ever
// increases; callers should diff two reads to measure activity over a
// window.
//
// Returns:
//   - The cumulative count of malformed lines observed since process start.
//
// Side effects:
//   - None.
func MalformedLineCount() int64 {
	return malformedLineCount.Load()
}

// FutureSchemaLineCount returns the process-wide count of JSONL lines whose
// SchemaVersion exceeds the runtime's CurrentSchemaVersion. Events with a
// future schema are still loaded so nothing is lost; the counter exists so
// operators can notice when a newer writer has touched their session files.
//
// Returns:
//   - The cumulative count of future-schema lines observed since process start.
//
// Side effects:
//   - None.
func FutureSchemaLineCount() int64 {
	return futureSchemaLineCount.Load()
}

// WriteEventsJSONL serialises events to the writer as JSON Lines (one JSON
// object per line). Timestamps are encoded in RFC3339 format by the standard
// encoding/json marshaller (time.Time implements json.Marshaler). An empty
// slice produces no output and no error.
//
// Expected:
//   - w is a non-nil io.Writer.
//   - events may be nil or empty (produces no output).
//
// Returns:
//   - An error if any event cannot be marshalled or if a write fails.
//
// Side effects:
//   - Writes to w.
func WriteEventsJSONL(w io.Writer, events []SwarmEvent) error {
	for i := range events {
		if err := writeOneEventJSONL(w, &events[i]); err != nil {
			return err
		}
	}
	return nil
}

// writeOneEventJSONL encodes ev as a single JSON object followed by a
// newline and writes it to w. Factored out of WriteEventsJSONL and shared
// with AppendSwarmEvent so both paths produce byte-identical output.
//
// Expected:
//   - w is a non-nil io.Writer.
//   - ev is a non-nil SwarmEvent pointer.
//
// Returns:
//   - An error if marshalling or the write fails.
//
// Side effects:
//   - Writes one line to w.
func writeOneEventJSONL(w io.Writer, ev *SwarmEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// ReadEventsJSONL reads JSON Lines from the reader and returns the parsed
// SwarmEvent entries. Lines that fail to unmarshal are silently skipped at
// the return-value level but bump a package-level counter
// (MalformedLineCount) and log a slog.Warn so operators can detect
// sustained corruption without losing the rest of the timeline.
//
// The scanner buffer is raised to 1 MiB so a single event line carrying a
// large tool-result body does not truncate the stream — the default 64 KiB
// limit was observed to drop events in production and is the P4 B3 blocker.
//
// Expected:
//   - r is a non-nil io.Reader producing JSON Lines content (one JSON object
//     per line).
//
// Returns:
//   - A slice of successfully parsed SwarmEvent entries (may be empty).
//   - An error only if the underlying reader fails for a reason other than
//     EOF.
//
// Side effects:
//   - Reads from r until EOF.
//   - Increments MalformedLineCount per corrupt line.
//   - Increments FutureSchemaLineCount per event with SchemaVersion > current.
//   - Emits slog.Warn per corrupt or future-schema line.
func ReadEventsJSONL(r io.Reader) ([]SwarmEvent, error) {
	var events []SwarmEvent
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, scannerInitialBuffer), maxJSONLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev SwarmEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Skip corrupted lines gracefully but make their presence visible.
			malformedLineCount.Add(1)
			slog.Warn("swarm event jsonl: skipping malformed line",
				"error", err,
				"line_bytes", len(line),
			)
			continue
		}
		if ev.SchemaVersion > CurrentSchemaVersion {
			futureSchemaLineCount.Add(1)
			slog.Warn("swarm event jsonl: observed future schema version",
				"event_id", ev.ID,
				"schema_version", ev.SchemaVersion,
				"current_schema_version", CurrentSchemaVersion,
			)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

// AppendSwarmEvent appends a single JSONL-encoded event to the file at path
// and fsyncs the file to disk before returning. The file is opened in
// O_APPEND|O_CREATE|O_WRONLY mode so concurrent appends from the same
// process preserve order within a single Write call (kernel atomicity for
// pipe-sized writes) — cross-process serialisation is P6 scope.
//
// Durability contract: on successful return the event is on stable storage.
// Callers are responsible for ensuring the parent directory exists.
//
// Expected:
//   - path is an absolute or relative file path whose parent directory
//     already exists.
//   - ev is a populated SwarmEvent.
//
// Returns:
//   - nil on success.
//   - An error when the file cannot be opened, the marshal fails, the write
//     fails, the sync fails, or the close fails.
//
// Side effects:
//   - Creates the file if it does not exist; otherwise appends one line.
//   - Fsyncs the file before closing.
func AppendSwarmEvent(path string, ev SwarmEvent) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := writeOneEventJSONL(f, &ev); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if syncHook != nil {
		syncHook()
	}
	return f.Close()
}

// CompactSwarmEvents rewrites the entire JSONL file at path from the
// supplied in-memory snapshot using a fsync-and-rename dance: a temporary
// sibling file is written, synced, closed, and then atomically renamed over
// the destination.
//
// When events is nil or empty, compact rewrites the destination as a
// zero-byte file (the authoritative in-memory view is "nothing"). This
// differs from the pre-P4 PersistSwarmEvents contract that treated empty as
// no-op — compaction is the close-time authority.
//
// Expected:
//   - path is an absolute or relative file path whose parent directory
//     already exists.
//   - events may be nil or empty.
//
// Returns:
//   - nil on success.
//   - An error when any of create, write, sync, close, or rename fail. On
//     failure the temporary file is best-effort removed.
//
// Side effects:
//   - Writes to <path>.tmp, fsyncs it, then renames it over <path>.
//   - Invokes the sync hook when installed (testing only).
func CompactSwarmEvents(path string, events []SwarmEvent) error {
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	for i := range events {
		if werr := writeOneEventJSONL(f, &events[i]); werr != nil {
			f.Close()
			os.Remove(tmpPath)
			return werr
		}
	}

	if serr := f.Sync(); serr != nil {
		f.Close()
		os.Remove(tmpPath)
		return serr
	}
	if syncHook != nil {
		syncHook()
	}

	if cerr := f.Close(); cerr != nil {
		os.Remove(tmpPath)
		return cerr
	}

	if rerr := os.Rename(tmpPath, path); rerr != nil {
		os.Remove(tmpPath)
		return rerr
	}
	return nil
}
