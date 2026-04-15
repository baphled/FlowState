package cli_test

// H2 — surface knowledge-extraction timeout as a warning on
// `flowstate run` exit. Before H2 the return value of
// WaitForBackgroundExtractions was discarded; on timeout operators
// got no signal at all, yet partial `memory.json.tmp` files could be
// left on disk by the killed-at-os.Exit extractor.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/engine"
)

// fakeWaiter is a test double that reports a scripted result from
// WaitForBackgroundExtractions, records the timeout it was called
// with, and lets tests drive both the clean-finish and timeout paths
// deterministically (no real goroutines, no sleeps).
//
// Post-M7 the real engine returns `error` rather than bool so the CLI
// exit path can distinguish "timed out after waiting" from "caller
// asked not to wait". The fake mirrors that shape.
type fakeWaiter struct {
	returnErr  error
	gotTimeout time.Duration
	calls      int
}

func (f *fakeWaiter) WaitForBackgroundExtractions(timeout time.Duration) error {
	f.calls++
	f.gotTimeout = timeout
	return f.returnErr
}

// captureSlog swaps the default slog logger for one writing to a
// returned buffer; the test's cleanup restores the previous logger so
// parallel tests aren't affected.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	slog.SetDefault(slog.New(handler))
	return &buf
}

// TestWaitForBackgroundExtractions_CleanFinish_NoWarn proves the
// happy path stays silent: a clean finish (return value true) must
// not emit the timeout warning. Operators running long pipelines do
// not want noise on every prompt.
func TestWaitForBackgroundExtractions_CleanFinish_NoWarn(t *testing.T) {
	buf := captureSlog(t)

	waiter := &fakeWaiter{returnErr: nil}
	cli.WaitForBackgroundExtractionsForTest(waiter, 35*time.Second)

	if waiter.calls != 1 {
		t.Fatalf("waiter calls = %d; want 1", waiter.calls)
	}
	if waiter.gotTimeout != 35*time.Second {
		t.Fatalf("waiter timeout = %v; want 35s", waiter.gotTimeout)
	}
	if strings.Contains(buf.String(), "knowledge extraction timed out") {
		t.Fatalf("unexpected warn on clean finish; log:\n%s", buf.String())
	}
}

// TestWaitForBackgroundExtractions_Timeout_EmitsWarnWithTimeout is the
// core H2 regression: when the waiter reports a timeout, the helper
// must emit a WARN naming the timeout in seconds so operators can
// correlate partial session-memory state with the run.
func TestWaitForBackgroundExtractions_Timeout_EmitsWarnWithTimeout(t *testing.T) {
	buf := captureSlog(t)

	waiter := &fakeWaiter{returnErr: engine.ErrExtractionTimeout}
	cli.WaitForBackgroundExtractionsForTest(waiter, 35*time.Second)

	got := buf.String()
	if !strings.Contains(got, "knowledge extraction timed out") {
		t.Fatalf("timeout warn missing; log:\n%s", got)
	}
	if !strings.Contains(got, "session memory may be incomplete") {
		t.Fatalf("warn text missing session-memory-incomplete clause; log:\n%s", got)
	}
	if !strings.Contains(got, "timeout_seconds=35") {
		t.Fatalf("warn missing timeout_seconds=35 attribute; log:\n%s", got)
	}
	if !strings.Contains(got, "level=WARN") {
		t.Fatalf("log level was not WARN; got:\n%s", got)
	}
}

// TestWaitForBackgroundExtractions_Timeout_IncludesConfiguredSeconds
// exercises a non-default timeout to prove the helper reports the
// configured value, not a hardcoded 35. This is the failsafe for the
// future configurability hook (compression.session_memory.wait_timeout).
// TestWaitForBackgroundExtractions_Skip_NoWarn pins M7 — when the
// engine is called with a non-positive timeout (the legacy
// fire-and-forget path), the returned error is nil, not
// ErrExtractionTimeout. The CLI warn must therefore NOT fire on skip.
// Pre-M7 the return was a plain bool, and `false` conflated
// "timed out after waiting" with "skipped because timeout <= 0",
// causing a spurious warning on every run that configured the wait
// off.
func TestWaitForBackgroundExtractions_Skip_NoWarn(t *testing.T) {
	buf := captureSlog(t)

	// returnErr=nil represents both "completed cleanly" and "skipped";
	// the fake is agnostic because the caller communicates "skipped"
	// by passing a non-positive timeout, which the real engine
	// short-circuits without scheduling any work.
	waiter := &fakeWaiter{returnErr: nil}
	cli.WaitForBackgroundExtractionsForTest(waiter, 0)

	if strings.Contains(buf.String(), "knowledge extraction timed out") {
		t.Fatalf("skip should not emit timeout warn; log:\n%s", buf.String())
	}
}

func TestWaitForBackgroundExtractions_Timeout_IncludesConfiguredSeconds(t *testing.T) {
	buf := captureSlog(t)

	waiter := &fakeWaiter{returnErr: engine.ErrExtractionTimeout}
	cli.WaitForBackgroundExtractionsForTest(waiter, 10*time.Second)

	if !strings.Contains(buf.String(), "timeout_seconds=10") {
		t.Fatalf("warn did not report configured timeout; log:\n%s", buf.String())
	}
}
