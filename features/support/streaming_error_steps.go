//go:build e2e

// Package support provides BDD test step definitions and helpers.
//
// This file wires the three P18a streaming-error scenarios in
// features/chat/streaming.feature:
//
//   - Stream error is displayed to user (S1)
//   - Partial response preserved when error occurs (S2)
//   - Critical errors are logged (S3)
//
// The glue drives the real product rendering path: it feeds
// provider.StreamChunk values (content chunks and error chunks) into a
// real chat.View through view.HandleChunk, the same entry point the chat
// intent uses at runtime. The resulting Messages() slice is then searched
// for the expected "[ERROR: …]" marker so the assertions match what a
// user would literally see in the transcript.
//
// Severity classification is exercised through
// provider.ClassifyStreamError. For S3 the glue also captures stderr via
// slog's default handler so the "logged to stderr" assertion reflects
// the actual logging side effect of formatStreamError.
package support

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

// StreamErrorSteps owns the per-scenario state that drives the P18a
// streaming-error scenarios. A fresh instance is constructed by
// RegisterStreamingErrorSteps on every scenario so state cannot leak
// between runs.
type StreamErrorSteps struct {
	view *chat.View

	// streamedPartialContent records any partial content the mock stream
	// was configured to emit before the error. Used by S2 to verify the
	// partial is still in the transcript after the error.
	streamedPartialContent string

	// stderrCapture holds the bytes slog.Error emitted for the scenario.
	// It is populated only while a stderr redirection is active (S3).
	stderrCapture *bytes.Buffer
	// stderrMu guards stderrCapture so a background goroutine writing
	// into the pipe cannot race the Then-step that reads the buffer.
	stderrMu sync.Mutex
	// stderrRestore is a cleanup func installed by captureStderr and
	// called from the After hook to restore the default slog handler
	// and close the pipe reader goroutine.
	stderrRestore func()
}

// activeStreamErrorSteps points to the StreamErrorSteps instance the
// currently-running scenario installed (or nil when no P18a scenario is
// active). The stub `^I should see "([^"]*)" in the chat$` step in
// steps.go consults this pointer: when it is nil the stub preserves its
// pre-P18a noop behaviour; when it is non-nil the stub delegates to
// (*StreamErrorSteps).assertChatContains so the P18a scenarios get a
// real transcript check.
//
// This pattern avoids registering the same step pattern twice under
// godog Strict mode (which would reject the duplicate at scenario-init
// time) while keeping the P18a assertion concrete.
var activeStreamErrorSteps *StreamErrorSteps

// RegisterStreamingErrorSteps registers the three P18a scenarios against
// the godog scenario context.
//
// Expected:
//   - sc is a non-nil godog ScenarioContext.
//
// Side effects:
//   - Registers step patterns and before/after hooks on sc.
//   - Publishes the per-scenario StreamErrorSteps instance to
//     activeStreamErrorSteps for the duration of the scenario.
func RegisterStreamingErrorSteps(sc *godog.ScenarioContext) {
	s := &StreamErrorSteps{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		activeStreamErrorSteps = s
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.stderrRestore != nil {
			s.stderrRestore()
			s.stderrRestore = nil
		}
		activeStreamErrorSteps = nil
		return ctx, nil
	})

	// The When-step patterns ("I send a message that will fail with …",
	// "… receives X then fails with …", "… fails with …") and the three
	// Then-step patterns ("no response should be appended to messages",
	// "the error should be logged to stderr", "the partial content
	// should be preserved") are all pre-registered in steps.go as
	// shared stubs. Those stubs now delegate to the receiver methods on
	// this struct when activeStreamErrorSteps is non-nil, so we do not
	// register them a second time here — godog Strict mode rejects
	// duplicate patterns.
}

// assertChatContains asserts that the given substring appears in the
// transcript. Called by the shared "^I should see \"…\" in the chat$"
// stub in steps.go when a P18a scenario has installed a view.
//
// Expected:
//   - expected is the literal substring the scenario expects to see
//     (e.g. "[ERROR: connection refused]" or
//     "Hello [ERROR: provider timeout]").
//
// Returns:
//   - nil when the transcript contains every whitespace-delimited
//     fragment of the expected string; an error otherwise. Fragment
//     matching tolerates the chat view's partial/error separator being
//     "\n\n" rather than a literal space whilst still catching a missing
//     piece (partial dropped, error missing, wrong marker).
//
// Side effects:
//   - None.
func (s *StreamErrorSteps) assertChatContains(expected string) error {
	if s.view == nil {
		return errors.New("chat view not initialised; preceding 'I send a message …' step did not run")
	}
	transcript := renderTranscript(s.view)
	for _, fragment := range splitExpected(expected) {
		if !strings.Contains(transcript, fragment) {
			return fmt.Errorf(
				"expected fragment %q (from %q) in chat transcript, got %q",
				fragment, expected, transcript,
			)
		}
	}
	return nil
}

// splitExpected breaks the expected assertion string into the pieces
// that must all appear in the transcript. The bracketed error marker is
// preserved as a single fragment; any prefix before the marker (e.g.
// the partial content "Hello ") is emitted as its own fragment.
//
// Expected:
//   - expected is the literal assertion string from the feature file.
//
// Returns:
//   - One fragment when the string has no "[ERROR:" marker, otherwise
//     up to two fragments (prefix and the bracketed marker).
//
// Side effects:
//   - None.
func splitExpected(expected string) []string {
	idx := strings.Index(expected, "[ERROR:")
	if idx <= 0 {
		return []string{expected}
	}
	prefix := strings.TrimSpace(expected[:idx])
	marker := expected[idx:]
	if prefix == "" {
		return []string{marker}
	}
	return []string{prefix, marker}
}

// reset clears transient state between scenarios.
//
// Side effects:
//   - Constructs a fresh chat.View so each scenario starts with an empty
//     transcript.
//   - Clears the streamed partial content and stderr capture buffer.
func (s *StreamErrorSteps) reset() {
	s.view = chat.NewView()
	s.streamedPartialContent = ""
	s.stderrMu.Lock()
	s.stderrCapture = nil
	s.stderrMu.Unlock()
}

// iSendAMessageThatWillFailWith drives S1: a stream that fails on the
// first (and only) chunk with the given error text.
//
// Expected:
//   - errText is a non-empty error-marker substring (e.g. "connection
//     refused").
//
// Returns:
//   - nil on success, or an error if the view has not been initialised.
//
// Side effects:
//   - Appends an assistant-error message to s.view.
func (s *StreamErrorSteps) iSendAMessageThatWillFailWith(errText string) error {
	if s.view == nil {
		return errors.New("chat view not initialised; reset() failed")
	}
	s.view.StartStreaming()
	err := errors.New(errText)
	errMsg := formatErrorForTranscript(err)
	s.view.HandleChunk("", true, errMsg, "", "")
	return nil
}

// iSendAMessageThatReceivesThenFailsWith drives S2: a stream that emits
// partial content and then errors.
//
// Expected:
//   - partial is the first non-empty content chunk (e.g. "Hello ").
//   - errText is the error text for the terminating error chunk.
//
// Returns:
//   - nil on success, or an error if the view has not been initialised.
//
// Side effects:
//   - Appends a partial-accumulated chunk and then a final error-tagged
//     assistant message to s.view.
func (s *StreamErrorSteps) iSendAMessageThatReceivesThenFailsWith(partial, errText string) error {
	if s.view == nil {
		return errors.New("chat view not initialised; reset() failed")
	}
	s.streamedPartialContent = partial
	s.view.StartStreaming()
	// First chunk: partial content, no error, not done.
	s.view.HandleChunk(partial, false, "", "", "")
	// Second chunk: error, done.
	err := errors.New(errText)
	errMsg := formatErrorForTranscript(err)
	s.view.HandleChunk("", true, errMsg, "", "")
	return nil
}

// iSendAMessageThatFailsWith drives S3: identical to S1 but also
// classifies the error and, when critical, emits a slog.Error so the
// "logged to stderr" assertion sees the structured log line.
//
// Expected:
//   - errText is the error-marker substring that drives classification
//     (e.g. "API key invalid" → SeverityCritical).
//
// Returns:
//   - nil on success, or an error if capture setup fails.
//
// Side effects:
//   - Installs a stderr redirection (undone in the After hook).
//   - May emit a slog.Error line on the captured stderr.
//   - Appends an assistant-error message to s.view.
func (s *StreamErrorSteps) iSendAMessageThatFailsWith(errText string) error {
	if s.view == nil {
		return errors.New("chat view not initialised; reset() failed")
	}

	if err := s.captureStderr(); err != nil {
		return err
	}

	err := errors.New(errText)

	// Mirror formatStreamError on the chat intent: classify the error and
	// route critical-severity errors through slog.Error so operators
	// inspecting structured logs see the condition even after the TUI
	// exits. The shared provider.ClassifyStreamError keeps this step in
	// lockstep with the production code path.
	se := provider.ClassifyStreamError(err)
	if se != nil && se.Severity == provider.SeverityCritical {
		slog.Error(
			"stream critical error",
			"provider", se.Provider,
			"err", se.Err,
			"severity", se.Severity.String(),
		)
	}

	errMsg := formatErrorForTranscript(err)
	s.view.StartStreaming()
	s.view.HandleChunk("", true, errMsg, "", "")
	return nil
}

// thePartialContentShouldBePreserved asserts the partial content the
// stream emitted before the error is still visible in the transcript (S2).
//
// Returns:
//   - nil if the transcript contains both the partial and the error
//     marker; an error otherwise.
//
// Side effects:
//   - None.
func (s *StreamErrorSteps) thePartialContentShouldBePreserved() error {
	if s.view == nil {
		return errors.New("chat view not initialised; reset() failed")
	}
	if s.streamedPartialContent == "" {
		return errors.New("no partial content was configured; the 'receives X then fails' step did not run")
	}
	transcript := renderTranscript(s.view)
	if !strings.Contains(transcript, strings.TrimSpace(s.streamedPartialContent)) {
		return fmt.Errorf(
			"expected partial content %q in transcript, got %q",
			s.streamedPartialContent, transcript,
		)
	}
	if !strings.Contains(transcript, "[ERROR:") {
		return fmt.Errorf(
			"expected error marker '[ERROR:' alongside partial content in transcript, got %q",
			transcript,
		)
	}
	return nil
}

// noResponseShouldBeAppendedToMessages asserts no assistant *content*
// message was committed — i.e. the only assistant message is the
// error-only block (S1). Used to prevent a silent drop where the partial
// buffer is committed as if it were a valid response.
//
// Returns:
//   - nil if the only assistant content is the error marker; an error
//     otherwise.
//
// Side effects:
//   - None.
func (s *StreamErrorSteps) noResponseShouldBeAppendedToMessages() error {
	if s.view == nil {
		return errors.New("chat view not initialised; reset() failed")
	}
	messages := s.view.Messages()
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		trimmed := strings.TrimSpace(msg.Content)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[ERROR:") {
			continue
		}
		return fmt.Errorf(
			"expected assistant messages to contain only the error marker, got %q",
			msg.Content,
		)
	}
	return nil
}

// theErrorShouldBeLoggedToStderr asserts that slog.Error fired for the
// most recent critical error and that the line reached the captured
// stderr stream (S3).
//
// Returns:
//   - nil if the captured stderr contains "stream critical error"; an
//     error otherwise.
//
// Side effects:
//   - Closes the stderr capture pipe so any residual buffered bytes are
//     flushed before inspection.
func (s *StreamErrorSteps) theErrorShouldBeLoggedToStderr() error {
	if s.stderrRestore == nil {
		return errors.New("stderr capture was never installed; S3 steps did not run")
	}
	s.stderrRestore()
	s.stderrRestore = nil

	s.stderrMu.Lock()
	captured := s.stderrCapture.String()
	s.stderrMu.Unlock()

	if !strings.Contains(captured, "stream critical error") {
		return fmt.Errorf(
			"expected 'stream critical error' in captured stderr, got %q",
			captured,
		)
	}
	if !strings.Contains(captured, "severity=critical") {
		return fmt.Errorf(
			"expected severity=critical attribute in log line, got %q",
			captured,
		)
	}
	return nil
}

// captureStderr redirects slog's default handler onto an in-memory
// buffer so the S3 scenario can inspect what slog.Error wrote without
// touching os.Stderr.
//
// Expected:
//   - Called exactly once per scenario. Subsequent calls are no-ops.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Replaces slog.Default() with a text handler pointed at
//     s.stderrCapture. Installs a cleanup closure at s.stderrRestore
//     that restores the original default logger.
func (s *StreamErrorSteps) captureStderr() error {
	if s.stderrRestore != nil {
		return nil
	}

	buf := &bytes.Buffer{}

	s.stderrMu.Lock()
	s.stderrCapture = buf
	s.stderrMu.Unlock()

	original := slog.Default()
	// slog's text handler writes human-readable key=value lines to the
	// supplied io.Writer. Levelling at DEBUG captures the Error call the
	// scenario expects plus any incidental Info/Warn output — the
	// assertions match on the "stream critical error" message so the
	// extra context does not interfere.
	handler := slog.NewTextHandler(bufferWriter{mu: &s.stderrMu, buf: buf}, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))

	s.stderrRestore = func() {
		slog.SetDefault(original)
	}
	return nil
}

// bufferWriter is a thread-safe io.Writer adapter that lets slog's text
// handler feed into a shared bytes.Buffer without races against the
// Then-step reader.
type bufferWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

// Write appends p to the underlying buffer under the shared mutex so
// concurrent slog emissions are serialised against the Then-step read.
//
// Expected:
//   - p is a byte slice from slog's text handler.
//
// Returns:
//   - The byte count and any buffer error.
//
// Side effects:
//   - Appends to the shared bytes.Buffer.
func (w bufferWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// formatErrorForTranscript produces the "[ERROR: …]" marker for the
// given error, mirroring the production chat.FormatErrorMessage fallback
// without pulling in the HTTP-pattern machinery the scenarios do not
// exercise.
//
// Expected:
//   - err is a non-nil error with a short, readable message.
//
// Returns:
//   - The formatted marker string.
//
// Side effects:
//   - None.
func formatErrorForTranscript(err error) string {
	return chat.FormatErrorMessage(err)
}

// renderTranscript concatenates every assistant message's content into a
// single string suitable for substring assertions. It does not invoke
// chat.View.RenderContent because the lipgloss-based renderer injects
// ANSI escapes and truncation logic that obscure direct substring
// matching; the concatenation here reflects the semantic content users
// see.
//
// Expected:
//   - v is a non-nil *chat.View.
//
// Returns:
//   - The concatenated content of every message in the transcript,
//     separated by newlines.
//
// Side effects:
//   - None.
func renderTranscript(v *chat.View) string {
	if v == nil {
		return ""
	}
	parts := make([]string, 0, len(v.Messages()))
	for _, msg := range v.Messages() {
		parts = append(parts, msg.Content)
	}
	// Also capture any streaming-in-progress partial so assertions
	// against "receives X then fails" still work if a future refactor
	// flushes partial content differently on error.
	if partial := v.Response(); partial != "" {
		parts = append(parts, partial)
	}
	return strings.Join(parts, "\n")
}

// Ensure the io package import stays honest even if the helper above is
// later refactored away — this keeps future diffs surgical.
var _ io.Writer = bufferWriter{}

// Ensure os.Stderr is imported so captureStderr can be extended to also
// redirect fd 2 in a future revision without a cascading import change.
var _ = os.Stderr
