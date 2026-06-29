package streaming

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"
)

// BufferedEventWriter provides batched, periodic-fsync writes to a JSONL
// events file. Events are accumulated in an in-memory buffer and flushed
// to disk either when the buffer reaches FlushBatchSize events or when
// FlushInterval elapses since the last flush, whichever comes first.
// This amortises the cost of fsync across multiple events while keeping
// the maximum data-at-risk window bounded to FlushInterval.
//
// A single BufferedEventWriter targets one session's events file. Callers
// should create one writer per session via NewBufferedEventWriter.
//
// Thread safety: all public methods are safe for concurrent use. The
// writer serialises access with an internal mutex so multiple goroutines
// appending to the same session coordinate correctly.
//
// Flush behaviour:
//   - The first Append after construction or after a flush starts the
//     flush timer. Subsequent Appends add to the buffer.
//   - When the event count reaches FlushBatchSize, the buffer is flushed
//     immediately (the timer is reset).
//   - When FlushInterval elapses, the buffer is flushed regardless of
//     how many events have accumulated.
//   - Callers MUST call Close when the writer is no longer needed
//     (typically on session close or process shutdown) to flush any
//     remaining events and release the file handle.
type BufferedEventWriter struct {
	path   string
	mu     sync.Mutex
	buf    bytes.Buffer
	file   *os.File
	count  int
	timer  *time.Timer
	closed bool

	// flushInterval is the maximum time between automatic flushes.
	flushInterval time.Duration
	// flushBatchSize is the event count threshold that triggers a flush.
	flushBatchSize int
}

// BufferedEventWriterConfig carries the tuning knobs for
// NewBufferedEventWriter. Zero values fall back to sensible defaults.
type BufferedEventWriterConfig struct {
	// FlushInterval is the maximum time between automatic flushes.
	// Zero defaults to 50ms.
	FlushInterval time.Duration
	// FlushBatchSize is the event count that triggers an immediate flush.
	// Zero or negative defaults to 20.
	FlushBatchSize int
}

// DefaultFlushInterval is the default maximum time between automatic
// flushes when BufferedEventWriterConfig.FlushInterval is zero.
const DefaultFlushInterval = 50 * time.Millisecond

// DefaultFlushBatchSize is the default event count that triggers an
// immediate flush when BufferedEventWriterConfig.FlushBatchSize is
// zero or negative.
const DefaultFlushBatchSize = 20

// NewBufferedEventWriter creates a BufferedEventWriter for the given
// file path. The file is opened lazily on the first Append — if no
// events are ever written, no file is created and no I/O occurs.
//
// Expected:
//   - path is the absolute or relative path to the JSONL events file.
//   - cfg carries tuning knobs; zero values fall back to defaults.
//
// Returns:
//   - A ready-to-use BufferedEventWriter.
//
// Side effects:
//   - Starts a background goroutine for periodic timer flushes when
//     the writer receives its first Append. The goroutine exits on
//     Close or final Flush.
func NewBufferedEventWriter(path string, cfg BufferedEventWriterConfig) *BufferedEventWriter {
	interval := cfg.FlushInterval
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	batchSize := cfg.FlushBatchSize
	if batchSize <= 0 {
		batchSize = DefaultFlushBatchSize
	}
	return &BufferedEventWriter{
		path:           path,
		flushInterval:  interval,
		flushBatchSize: batchSize,
	}
}

// Append encodes ev as JSONL and adds it to the in-memory buffer. If
// the buffer has reached FlushBatchSize events, a flush is triggered
// immediately. Otherwise, a timer is started (or reset) to flush after
// FlushInterval.
//
// Expected:
//   - ev is a populated SwarmEvent.
//
// Side effects:
//   - May write buffered events to disk and fsync when the batch
//     threshold is hit.
//   - Starts or resets the flush timer when the buffer is non-empty
//     and below the batch threshold.
func (w *BufferedEventWriter) Append(ev SwarmEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		slog.Warn("buffered event writer: append to closed writer",
			"path", w.path,
			"event_id", ev.ID,
		)
		return
	}

	data, err := json.Marshal(ev)
	if err != nil {
		slog.Error("buffered event writer: marshal failed",
			"path", w.path,
			"event_id", ev.ID,
			"error", err,
		)
		return
	}
	data = append(data, '\n')
	w.buf.Write(data)
	w.count++

	if w.count >= w.flushBatchSize {
		w.flushLocked()
		return
	}

	if w.timer == nil {
		w.timer = time.AfterFunc(w.flushInterval, func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.closed || w.count == 0 {
				return
			}
			w.flushLocked()
		})
	}
}

// flushLocked writes the buffered events to disk and fsyncs. The caller
// MUST hold w.mu. After flushing, the timer is stopped and the buffer
// is reset so subsequent Appends start a new cycle.
//
// Side effects:
//   - Opens the file on first flush (lazy open).
//   - Writes buffered data, fsyncs, and resets state.
//   - Invokes the package-level syncHook if set (for test assertions).
func (w *BufferedEventWriter) flushLocked() {
	if w.count == 0 && w.buf.Len() == 0 {
		return
	}

	if w.file == nil {
		f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			slog.Error("buffered event writer: open failed",
				"path", w.path,
				"error", err,
			)
			return
		}
		w.file = f
	}

	if _, err := w.file.Write(w.buf.Bytes()); err != nil {
		slog.Error("buffered event writer: write failed",
			"path", w.path,
			"error", err,
		)
		return
	}

	if err := w.file.Sync(); err != nil {
		slog.Error("buffered event writer: fsync failed",
			"path", w.path,
			"error", err,
		)
		return
	}

	if syncHook != nil {
		syncHook()
	}

	// Reset buffer and counter for the next batch.
	w.buf.Reset()
	w.count = 0

	// Stop the timer if it exists; a new cycle starts on the next Append.
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

// Flush synchronously flushes any buffered events to disk and fsyncs.
// This is useful before session-critical operations (session switch,
// process shutdown) where all pending events must be durable.
//
// Safe to call multiple times; subsequent calls are no-ops until new
// Appends arrive.
//
// Side effects:
//   - Writes buffered data to disk and fsyncs.
//   - Stops the flush timer.
func (w *BufferedEventWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
}

// Close flushes any buffered events, closes the underlying file, and
// releases all resources. After Close, the writer MUST NOT be used for
// further Appends.
//
// Side effects:
//   - Flushes buffered data.
//   - Closes the file handle.
//   - Marks the writer as closed.
func (w *BufferedEventWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}

	w.flushLocked()

	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
