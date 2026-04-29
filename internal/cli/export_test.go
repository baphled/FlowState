package cli

import (
	"context"
	"io"
	"time"

	"github.com/baphled/flowstate/internal/app"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/spf13/cobra"
)

// WaitForBackgroundExtractionsForTest exposes the unexported helper so
// its timeout-warn behaviour can be exercised without standing up a
// full engine+goroutine.
func WaitForBackgroundExtractionsForTest(waiter BackgroundExtractionWaiterForTest, timeout time.Duration) {
	waitForBackgroundExtractions(waiter, timeout)
}

// BackgroundExtractionWaiterForTest mirrors the unexported package
// interface so external test packages can satisfy it.
type BackgroundExtractionWaiterForTest interface {
	WaitForBackgroundExtractions(timeout time.Duration) error
}

// ResolveBackgroundExtractionWaitForTest exposes the unexported config
// resolver so Item 7's propagation test can verify that a custom
// compression.session_memory.wait_timeout reaches the CLI exit path.
func ResolveBackgroundExtractionWaitForTest(application *app.App) time.Duration {
	return resolveBackgroundExtractionWait(application)
}

// DefaultBackgroundExtractionWaitForTest exposes the fallback constant
// so tests can assert both the default-path and overridden-path values
// without duplicating the 35-second literal.
const DefaultBackgroundExtractionWaitForTest = defaultBackgroundExtractionWait

// HTTPShutdownerForTest mirrors the unexported httpShutdowner interface
// so Item 6's regression test can inject a fake server.
type HTTPShutdownerForTest = httpShutdowner

// EngineShutdownerForTest mirrors the unexported engineShutdowner
// interface. Item 6 uses it to assert that serve shutdown always
// invokes Engine.Shutdown; a future refactor of runServe that skips
// the call must fail this test.
type EngineShutdownerForTest = engineShutdowner

// PerformServeShutdownForTest exposes the unexported helper so the
// regression test can drive it without binding a port or wiring up a
// signal loop.
func PerformServeShutdownForTest(server HTTPShutdownerForTest, eng EngineShutdownerForTest, out, errOut io.Writer) error {
	return performServeShutdown(server, eng, out, errOut)
}

// WriteCompressionStatsForTest exposes the unexported helper so Item 2's
// --stats test can assert the one-line format without standing up a
// full run.
func WriteCompressionStatsForTest(out io.Writer, metrics ctxstore.CompressionMetrics) {
	writeCompressionStats(out, metrics)
}

// NewTaskCmdForTest, NewTaskListCmdForTest, NewTaskOutputCmdForTest, and
// NewTaskCancelCmdForTest expose the unexported task command
// constructors so the external cli_test package can drive their
// wiring without having to live inside the cli package itself
// (which would collide with the cli_test Ginkgo suite bootstrap).
func NewTaskCmdForTest(getApp func() *app.App) *cobra.Command {
	return newTaskCmd(getApp)
}

func NewTaskListCmdForTest(getApp func() *app.App) *cobra.Command {
	return newTaskListCmd(getApp)
}

func NewTaskOutputCmdForTest(getApp func() *app.App) *cobra.Command {
	return newTaskOutputCmd(getApp)
}

func NewTaskCancelCmdForTest(getApp func() *app.App) *cobra.Command {
	return newTaskCancelCmd(getApp)
}

// SetOllamaProbeForTest swaps the package-level Ollama HTTP probe so external
// tests can drive the `flowstate auth ollama` subcommand without making real
// network calls. Returns a restore function that the test must invoke in its
// teardown.
func SetOllamaProbeForTest(probe func(string) error) func() {
	original := ollamaProbe
	ollamaProbe = probe
	return func() { ollamaProbe = original }
}

// RunPromptCtxForTest exposes the context-aware run entry point so the
// signal-driven persist-on-cancel regression test can drive a
// cancellation in-process without sending real signals to the test
// runner. Outside tests runPrompt always provides a signal.NotifyContext
// linked to cmd.Context(); tests pass a plain context.WithCancel so
// they can cancel mid-stream and assert the defer-save flushed the
// session.
func RunPromptCtxForTest(ctx context.Context, cmd *cobra.Command, application *app.App, opts *RunOptions) error {
	return runPromptCtx(ctx, cmd, application, opts)
}

// NewToolsCmdForTest exposes the unexported tools command constructor so
// external test packages can drive its wiring without living inside the cli
// package.
func NewToolsCmdForTest(getApp func() *app.App) *cobra.Command {
	return newToolsCmd(getApp)
}

// NewToolsListCmdForTest exposes the unexported tools list subcommand so
// external test packages can drive flag wiring and output assertions without
// living inside the cli package.
func NewToolsListCmdForTest(getApp func() *app.App) *cobra.Command {
	return newToolsListCmd(getApp)
}
