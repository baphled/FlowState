package cli_test

// H2 — surface knowledge-extraction timeout as a warning on
// `flowstate run` exit. Before H2 the return value of
// WaitForBackgroundExtractions was discarded; on timeout operators
// got no signal at all, yet partial `memory.json.tmp` files could be
// left on disk by the killed-at-os.Exit extractor.

import (
	"bytes"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/engine"
)

// fakeWaiter is a test double that reports a scripted result from
// WaitForBackgroundExtractions, records the timeout it was called
// with, and lets specs drive both the clean-finish and timeout paths
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

var _ = Describe("WaitForBackgroundExtractions", func() {
	var (
		buf     *bytes.Buffer
		prevLog *slog.Logger
	)

	BeforeEach(func() {
		// Swap the default slog logger for one writing to a fresh
		// buffer so each spec sees a clean log surface; AfterEach
		// restores so parallel suites are not affected.
		prevLog = slog.Default()
		buf = &bytes.Buffer{}
		handler := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		slog.SetDefault(slog.New(handler))
	})

	AfterEach(func() {
		slog.SetDefault(prevLog)
	})

	// CleanFinish_NoWarn: the happy path stays silent. Operators
	// running long pipelines do not want noise on every prompt.
	It("does not warn on clean finish", func() {
		waiter := &fakeWaiter{returnErr: nil}
		cli.WaitForBackgroundExtractionsForTest(waiter, 35*time.Second)

		Expect(waiter.calls).To(Equal(1))
		Expect(waiter.gotTimeout).To(Equal(35 * time.Second))
		Expect(buf.String()).NotTo(ContainSubstring("knowledge extraction timed out"),
			"unexpected warn on clean finish; log:\n%s", buf.String())
	})

	// Timeout_EmitsWarnWithTimeout: the core H2 regression. When the
	// waiter reports a timeout, the helper must emit a WARN naming
	// the timeout in seconds so operators can correlate partial
	// session-memory state with the run.
	It("emits a WARN naming the configured timeout when extraction times out", func() {
		waiter := &fakeWaiter{returnErr: engine.ErrExtractionTimeout}
		cli.WaitForBackgroundExtractionsForTest(waiter, 35*time.Second)

		got := buf.String()
		Expect(got).To(ContainSubstring("knowledge extraction timed out"),
			"timeout warn missing; log:\n%s", got)
		Expect(got).To(ContainSubstring("session memory may be incomplete"),
			"warn text missing session-memory-incomplete clause; log:\n%s", got)
		Expect(got).To(ContainSubstring("timeout_seconds=35"),
			"warn missing timeout_seconds=35 attribute; log:\n%s", got)
		Expect(got).To(ContainSubstring("level=WARN"),
			"log level was not WARN; got:\n%s", got)
	})

	// Skip_NoWarn pins M7 — when the engine is called with a
	// non-positive timeout (the legacy fire-and-forget path), the
	// returned error is nil, not ErrExtractionTimeout. The CLI warn
	// must therefore NOT fire on skip. Pre-M7 the return was a plain
	// bool, and `false` conflated "timed out after waiting" with
	// "skipped because timeout <= 0", causing a spurious warning on
	// every run that configured the wait off.
	It("does not warn when skipping with a non-positive timeout (M7)", func() {
		// returnErr=nil represents both "completed cleanly" and
		// "skipped"; the fake is agnostic because the caller
		// communicates "skipped" by passing a non-positive timeout,
		// which the real engine short-circuits without scheduling
		// any work.
		waiter := &fakeWaiter{returnErr: nil}
		cli.WaitForBackgroundExtractionsForTest(waiter, 0)

		Expect(buf.String()).NotTo(ContainSubstring("knowledge extraction timed out"),
			"skip should not emit timeout warn; log:\n%s", buf.String())
	})

	// Timeout_IncludesConfiguredSeconds: a non-default timeout to
	// prove the helper reports the configured value, not a hardcoded
	// 35. Failsafe for compression.session_memory.wait_timeout.
	It("reports the configured timeout, not a hardcoded value", func() {
		waiter := &fakeWaiter{returnErr: engine.ErrExtractionTimeout}
		cli.WaitForBackgroundExtractionsForTest(waiter, 10*time.Second)

		Expect(buf.String()).To(ContainSubstring("timeout_seconds=10"),
			"warn did not report configured timeout; log:\n%s", buf.String())
	})
})
