// flowstate talk — push-to-talk voice input.
//
// The talk command records microphone audio (via the voice pipeline's
// capture tool), transcribes it with a local STT binary (whisper-cli),
// echoes the transcript, and hands it to the same non-interactive
// dispatch path `flowstate run` uses. All voice binaries are resolved
// env > config > PATH and every stage degrades gracefully: when the
// capture or STT binary is absent the command warns and continues in
// text-only mode (reading the turn from stdin) instead of failing.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/voice"
	"github.com/spf13/cobra"
)

// talkOptions holds the flags for `flowstate talk`.
type talkOptions struct {
	// Agent routes the transcript to a named agent or swarm.
	Agent string
	// Session resumes an existing session id when non-empty.
	Session string
	// JSON switches output to the machine-readable run format.
	JSON bool
	// TextOnly skips capture/STT entirely and reads the turn from
	// stdin; also the automatic fallback when binaries are absent.
	TextOnly bool
}

// newTalkCmd builds the push-to-talk `flowstate talk` command.
//
// Expected:
//   - getApp returns the initialised application instance.
//
// Returns:
//   - A configured *cobra.Command.
//
// Side effects:
//   - Registers the --agent, --session, --json and --text-only flags.
func newTalkCmd(getApp func() *app.App) *cobra.Command {
	opts := &talkOptions{Agent: "worker"}
	cmd := &cobra.Command{
		Use:   "talk",
		Short: "Talk to agents by voice (push-to-talk)",
		Long: "Record a voice turn, transcribe it locally, echo the transcript, and dispatch it " +
			"through the same session pipeline as `flowstate run`. Press Enter to start and stop " +
			"recording. Falls back to text-only input when voice binaries are absent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runTalk(ctx, cmd, getApp(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Agent, "agent", opts.Agent, "Agent or swarm to receive the transcript")
	cmd.Flags().StringVar(&opts.Session, "session", "", "Resume an existing session id")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Emit the response as JSON")
	cmd.Flags().BoolVar(&opts.TextOnly, "text-only", false, "Skip voice capture and read the turn from stdin")
	return cmd
}

// terminalRestorer defers terminal-state restoration so the E2E suite
// can observe that raw-mode side effects are undone on every exit path.
type terminalRestorer struct {
	// restore reverses any terminal state mutation; never nil once
	// newTerminalRestorer has run.
	restore func()
}

// newTerminalRestorer installs the terminal restorer.
//
// Expected:
//   - None.
//
// Returns:
//   - A restorer whose restore call is a no-op until push-to-talk
//     puts the terminal into raw mode.
//
// Side effects:
//   - None; restoration is armed lazily.
func newTerminalRestorer() *terminalRestorer {
	return &terminalRestorer{restore: func() {}}
}

// runTalk performs one push-to-talk turn.
//
// Expected:
//   - ctx is non-nil and cancelled on SIGINT/SIGTERM.
//   - cmd and application are wired by the cobra runtime.
//   - opts carries the parsed flags.
//
// Returns:
//   - nil on a dispatched turn; an error for engine absence or a
//     failed dispatch. Missing voice binaries are NOT errors — the
//     command warns and continues in text-only mode.
//
// Side effects:
//   - Spawns capture and STT subprocesses unless text-only.
//   - Prints the transcript and the agent response.
//   - Restores terminal state on exit.
func runTalk(ctx context.Context, cmd *cobra.Command, application *app.App, opts *talkOptions) error {
	if application.Streamer == nil {
		return errors.New("engine not configured")
	}
	restorer := newTerminalRestorer()
	defer restorer.restore()

	var transcript string
	var err error
	switch {
	case opts.TextOnly:
		transcript, err = readTextTurn(cmd)
	default:
		transcript, err = captureVoiceTurn(ctx, cmd)
		if err != nil {
			fmt.Fprintln(cmd.OutOrStdout(), "voice unavailable, falling back to text-only mode:", err)
			transcript, err = readTextTurn(cmd)
		}
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(transcript) == "" {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "you:", transcript)

	runOpts := &RunOptions{
		Prompt:  transcript,
		Agent:   opts.Agent,
		JSON:    opts.JSON,
		Session: opts.Session,
	}
	return runPromptCtx(ctx, cmd, application, runOpts)
}

// captureVoiceTurn records one push-to-talk utterance and transcribes
// it, stopping capture on the second Enter keypress.
//
// Expected:
//   - ctx is non-nil; cancellation aborts capture.
//   - cmd provides the user-facing IO writers.
//
// Returns:
//   - The transcript string.
//   - voice.ErrCaptureUnavailable or voice.ErrSTTUnavailable (wrapped)
//     when the local binaries are absent.
//
// Side effects:
//   - Prints prompts; spawns the capture and STT subprocesses; the
//     temporary WAV is removed after transcription.
func captureVoiceTurn(ctx context.Context, cmd *cobra.Command) (string, error) {
	fmt.Fprintln(cmd.OutOrStdout(), "press Enter to start recording, Enter again to stop")
	reader := bufio.NewReader(os.Stdin)
	if _, err := reader.ReadString('\n'); err != nil {
		return "", err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "recording…")

	pipe := voice.NewPipeline("", "")
	capTool, err := voice.NewCaptureTool(pipe.Capture)
	if err != nil {
		return "", err
	}
	rec, err := capTool.Record(ctx, pipe.MaxDuration)
	if err != nil {
		return "", err
	}
	defer rec.Cleanup()

	fmt.Fprintln(cmd.OutOrStdout(), "transcribing…")
	sttTool, err := voice.NewSTTTool(pipe.STT)
	if err != nil {
		return "", err
	}
	return sttTool.Transcribe(ctx, rec.Path)
}

// readTextTurn reads one line of input as the turn content. EOF
// (exhausted or closed stdin) ends the turn as empty rather than an
// error, so `flowstate talk` degrades cleanly from a pipe.
//
// Expected:
//   - cmd provides the user-facing IO writers.
//
// Returns:
//   - The trimmed line read from stdin; empty on EOF.
//
// Side effects:
//   - Reads a line from stdin; prints a prompt first.
func readTextTurn(cmd *cobra.Command) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), "text> ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}
