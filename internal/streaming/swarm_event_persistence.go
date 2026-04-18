package streaming

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// renameHook, when non-nil, replaces the os.Rename call in
// CompactSwarmEvents. Tests use this to inject a deterministic rename
// failure and assert that the .tmp file is removed in the error path.
// Production code leaves this as nil.
var renameHook func(from, to string) error

// tmpFileSuffix is the fixed suffix CompactSwarmEvents writes the staging
// file with. The orphan scanner matches on this exact form.
const tmpFileSuffix = ".events.jsonl.tmp"

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

// SetRenameHookForTest installs a hook that is called in place of os.Rename
// during CompactSwarmEvents. The previous hook is returned so callers can
// restore it via DeferCleanup. Passing nil restores the production default
// (direct os.Rename).
//
// Expected:
//   - hook is either nil (disable) or a function that returns an error to
//     simulate a rename failure, or nil to let the rename succeed.
//
// Returns:
//   - The previously installed hook.
//
// Side effects:
//   - Mutates package-level state; intended for tests only.
func SetRenameHookForTest(hook func(from, to string) error) func(from, to string) error {
	prev := renameHook
	renameHook = hook
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

// unknownTypeLineCount counts JSONL lines that parsed successfully but
// whose Type field does not match any of the five known SwarmEventType
// values. The event is still returned to the caller (forward compatibility
// with a future producer) but the pane hides it and operators can detect
// the mismatch via this counter. Thread-safe via atomic.
var unknownTypeLineCount atomic.Int64

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

// UnknownTypeLineCount returns the process-wide count of JSONL lines whose
// Type is not one of the known SwarmEventType constants. The counter only
// ever increases; callers should diff two reads to measure activity over a
// window.
//
// Returns:
//   - The cumulative count of unknown-type lines observed since process
//     start.
//
// Side effects:
//   - None.
func UnknownTypeLineCount() int64 {
	return unknownTypeLineCount.Load()
}

// isKnownSwarmEventType reports whether t matches one of the five canonical
// SwarmEventType constants. Unknown types are still returned to the caller
// for forward compatibility but logged and counted for observability.
//
// Expected:
//   - t is any SwarmEventType value, including the zero value.
//
// Returns:
//   - true when t equals one of EventDelegation, EventToolCall,
//     EventToolResult, EventPlan, EventReview; false otherwise.
//
// Side effects:
//   - None.
func isKnownSwarmEventType(t SwarmEventType) bool {
	switch t {
	case EventDelegation, EventToolCall, EventToolResult, EventPlan, EventReview:
		return true
	default:
		return false
	}
}

// ReadStats captures per-read diagnostic counters from ReadEventsJSONL.
// Callers use this to surface corruption or unknown-type events at load
// time — the global process-wide counters (MalformedLineCount,
// UnknownTypeLineCount, FutureSchemaLineCount) remain available but are not
// per-read.
type ReadStats struct {
	// MalformedLines is the count of JSONL lines that failed to unmarshal
	// during this read.
	MalformedLines int64
	// UnknownTypeLines is the count of lines that parsed but whose Type is
	// not in the known-constant set.
	UnknownTypeLines int64
	// FutureSchemaLines is the count of lines whose SchemaVersion exceeds
	// CurrentSchemaVersion.
	FutureSchemaLines int64
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
	events, _, err := ReadEventsJSONLWithStats(r)
	return events, err
}

// ReadEventsJSONLWithStats is identical to ReadEventsJSONL but additionally
// returns per-read counters for malformed lines, unknown types, and
// future-schema events. Callers (for example, session load) can surface
// these counts immediately rather than diff the process-wide counters.
//
// Expected:
//   - r is a non-nil io.Reader producing JSON Lines content.
//
// Returns:
//   - The successfully parsed events.
//   - A ReadStats containing the per-read counters.
//   - An error only when the underlying reader fails for a reason other
//     than EOF.
//
// Side effects:
//   - Reads from r until EOF.
//   - Increments the process-wide counters for each anomaly observed.
//   - Emits slog.Warn per malformed, unknown-type, or future-schema line.
func ReadEventsJSONLWithStats(r io.Reader) ([]SwarmEvent, ReadStats, error) {
	var events []SwarmEvent
	var stats ReadStats
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
			stats.MalformedLines++
			slog.Warn("swarm event jsonl: skipping malformed line",
				"error", err,
				"line_bytes", len(line),
			)
			continue
		}
		if ev.SchemaVersion > CurrentSchemaVersion {
			futureSchemaLineCount.Add(1)
			stats.FutureSchemaLines++
			slog.Warn("swarm event jsonl: observed future schema version",
				"event_id", ev.ID,
				"schema_version", ev.SchemaVersion,
				"current_schema_version", CurrentSchemaVersion,
			)
		}
		if !isKnownSwarmEventType(ev.Type) {
			unknownTypeLineCount.Add(1)
			stats.UnknownTypeLines++
			slog.Warn("swarm event jsonl: unknown swarm event type",
				"event_id", ev.ID,
				"type", string(ev.Type),
			)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return events, stats, err
	}
	return events, stats, nil
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
	unlock := sessionLocks.Lock(path)
	defer unlock()

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
	unlock := sessionLocks.Lock(path)
	defer unlock()

	tmpPath := path + ".tmp"
	// Defence in depth: an unhandled panic or an early return that forgets
	// to clean up the tmp file would leak it on the filesystem. The named
	// return value is unused, so we use a sentinel flag instead.
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmpPath)
		}
	}()

	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	for i := range events {
		if werr := writeOneEventJSONL(f, &events[i]); werr != nil {
			f.Close()
			return werr
		}
	}

	if serr := f.Sync(); serr != nil {
		f.Close()
		return serr
	}
	if syncHook != nil {
		syncHook()
	}

	if cerr := f.Close(); cerr != nil {
		return cerr
	}

	rename := os.Rename
	if renameHook != nil {
		rename = renameHook
	}
	if rerr := rename(tmpPath, path); rerr != nil {
		return rerr
	}
	renamed = true
	return nil
}

// RemoveSwarmEvents deletes the events file at path together with any
// leftover .tmp sibling. Missing files are not an error so session-delete
// callers can invoke the helper unconditionally.
//
// The function acquires the per-path lock so it cannot interleave with a
// concurrent Append or Compact on the same session — a late-arriving event
// will either observe the file before the delete (and is lost as expected)
// or after (and recreates it; the caller is responsible for ordering
// delete after stream shutdown).
//
// Expected:
//   - path is the canonical events file path (the caller computes it via
//     the session persistence helpers).
//
// Returns:
//   - nil on success or when the file is already absent.
//   - The first non-"not exist" error encountered.
//
// Side effects:
//   - Removes <path> and <path>.tmp from disk if they exist.
func RemoveSwarmEvents(path string) error {
	unlock := sessionLocks.Lock(path)
	defer unlock()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(path + ".tmp"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// CleanupOrphanTmpFiles removes any *.events.jsonl.tmp files in dir. These
// only ever exist as intermediate staging files for CompactSwarmEvents; a
// surviving one after process shutdown means a compact crashed mid-rename
// and left a half-written stager on disk.
//
// Expected:
//   - dir is a directory path; a non-existent dir is treated as a no-op
//     rather than an error so startup callers do not need to pre-check.
//
// Returns:
//   - The number of .tmp files successfully removed.
//   - The first error encountered when removing a matched file. Read
//     errors on the directory itself are returned as well.
//
// Side effects:
//   - Deletes files from disk.
func CleanupOrphanTmpFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, tmpFileSuffix) {
			continue
		}
		full := filepath.Join(dir, name)
		if rerr := os.Remove(full); rerr != nil && !os.IsNotExist(rerr) {
			return removed, rerr
		}
		removed++
	}
	return removed, nil
}
