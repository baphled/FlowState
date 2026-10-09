// flowstate talk — push-to-talk voice input.
//
// The talk command records microphone audio (via the voice pipeline's
// capture tool), transcribes it with a local STT binary (whisper-cli),
// optionally speaks the agent reply through a local TTS binary (piper)
// when voice.tts_enabled is on, and hands the transcript to the same
// non-interactive dispatch path `flowstate run` uses. All voice
// binaries are resolved env > config > PATH and every stage degrades
// gracefully: when the capture or STT binary is absent the command
// warns and continues in text-only mode (reading the turn from stdin)
// instead of failing; a missing TTS binary never fails the turn.
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
	"time"

	"github.com/spf13/cobra"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/orchestrator"
	"github.com/baphled/flowstate/internal/voice"
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
	// Speak opts in to speaking the reply via TTS for this turn,
	// overriding the config default.
	Speak bool
	// NoSpeak opts out of speaking the reply via TTS for this turn.
	NoSpeak bool
}

// talkLastResponse is the reply text captured from the most recent
// talk dispatch, set by streamTalkResponse so speakReply can hand it
// to the TTS tool.
var talkLastResponse string

// newTalkCmd builds the push-to-talk `flowstate talk` command.
//
// Expected:
//   - getApp returns the initialised application instance.
//
// Returns:
//   - A configured *cobra.Command.
//
// Side effects:
//   - Registers the --agent, --session, --json, --text-only, --speak
//     and --no-speak flags.
func newTalkCmd(getApp func() *app.App) *cobra.Command {
	opts := &talkOptions{Agent: "worker"}
	cmd := &cobra.Command{
		Use:   "talk",
		Short: "Talk to agents by voice (push-to-talk)",
		Long: "Record a voice turn, transcribe it locally, dispatch it through the same session " +
			"pipeline as `flowstate run`, and speak the reply via TTS when enabled. Press Enter to " +
			"start and stop recording. Falls back to text-only input when voice binaries are absent.",
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
	cmd.Flags().BoolVar(&opts.Speak, "speak", false, "Speak the agent reply via TTS for this turn")
	cmd.Flags().BoolVar(&opts.NoSpeak, "no-speak", false, "Never speak the agent reply for this turn")
	return cmd
}

// runTalk performs one push-to-talk turn, speaking the reply when
// TTS is enabled (config voice.tts_enabled, or the --speak /
// --no-speak per-turn overrides).
//
// Expected:
//   - ctx is non-nil and cancelled on SIGINT/SIGTERM.
//   - cmd and application are wired by the cobra runtime.
//   - opts carries the parsed flags.
//
// Returns:
//   - nil on a dispatched turn; an error for engine absence or a
//     failed dispatch. Missing voice binaries are NOT errors — the
//     command warns and continues in text-only mode, and a missing
//     TTS binary only skips the spoken half of the turn.
//
// Side effects:
//   - Spawns capture and STT subprocesses unless text-only.
//   - Prints the transcript and the agent response.
//   - Speaks the reply via the TTS binary when enabled.
func runTalk(ctx context.Context, cmd *cobra.Command, application *app.App, opts *talkOptions) error {
	if application.Streamer == nil {
		return errors.New("engine not configured")
	}

	var transcript string
	var err error
	switch {
	case opts.TextOnly:
		transcript, err = readTextTurn(cmd)
	default:
		sttCmd := ""
		if application != nil && application.Config != nil {
			sttCmd = application.Config.Voice.STTCmd
		}
		captureCmd := ""
		if application != nil && application.Config != nil {
			captureCmd = application.Config.Voice.CaptureCmd
		}
		transcript, err = captureVoiceTurn(ctx, cmd, captureCmd, sttCmd)
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

	sessionID := resolveSessionID(opts.Session)
	loadExistingSession(application, opts.Session)
	defer saveSession(ctx, cmd, application, sessionID)

	runOpts := &RunOptions{
		Prompt:  transcript,
		Agent:   opts.Agent,
		JSON:    opts.JSON,
		Session: opts.Session,
	}
	streamed, err := streamTalkResponse(ctx, cmd, application, runOpts)
	if err != nil {
		return err
	}
	talkLastResponse = streamed
	return speakReply(ctx, cmd, application, opts, talkLastResponse)
}

// streamTalkResponse dispatches the transcript through the shared
// run pipeline and returns the complete reply text for TTS.
//
// Expected:
//   - ctx is non-nil; cmd and application are wired; opts carries a
//     non-empty prompt.
//
// Returns:
//   - The complete agent response string and nil on success, or an
//     empty string and error on failure.
//
// Side effects:
//   - Streams the reply to the command's output writer.
func streamTalkResponse(ctx context.Context, cmd *cobra.Command, application *app.App, opts *RunOptions) (string, error) {
	consumer := NewWriterConsumer(cmd.OutOrStdout(), opts.JSON)
	orch := orchestrator.New( //nolint:contextcheck // orchestrator owns a dispatcher-lifecycle base context internally; call-level ctx is passed per operation
		application.Engine,
		application.Registry,
		application.SwarmRegistry,
		application.Streamer,
		application.Sessions,
		application.SessionManager(),
	)
	err := orch.ProcessUserInput(ctx, orchestrator.UserInput{
		Message:      opts.Prompt,
		DefaultAgent: opts.Agent,
		ScanMentions: true,
	}, consumer)
	if err != nil {
		return "", err
	}
	if consumer.Err() != nil {
		return "", fmt.Errorf("stream error: %w", consumer.Err())
	}
	return consumer.Response(), nil
}

// speakReply speaks the agent reply through the TTS tool when
// enabled, degrading silently when TTS is unavailable.
//
// Expected:
//   - ctx is non-nil; cancellation aborts the TTS child.
//   - application carries the loaded voice config.
//   - reply is the agent's response text; empty replies are skipped.
//
// Returns:
//   - Invariably nil: an unavailable TTS binary degrades to a warning,
//     never a failed turn.
//
// Side effects:
//   - Spawns the TTS child when a tool resolves and speaking is on.
func speakReply(ctx context.Context, cmd *cobra.Command, application *app.App, opts *talkOptions, reply string) error {
	if strings.TrimSpace(reply) == "" {
		return nil
	}
	enabled := application != nil && application.Config != nil && application.Config.Voice.TTSEnabled
	if opts.Speak {
		enabled = true
	}
	if opts.NoSpeak {
		enabled = false
	}
	if !enabled {
		return nil
	}
	ttsCmd := ""
	if application != nil && application.Config != nil {
		ttsCmd = application.Config.Voice.TTSCmd
	}
	tts, err := voice.NewTTSTool(ttsCmd)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tts unavailable, reply shown as text only:", err)
		return nil
	}
	if err := tts.Speak(ctx, reply); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tts failed, reply shown as text only:", err)
	}
	return nil
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
func captureVoiceTurn(ctx context.Context, cmd *cobra.Command, captureCmd, sttCmd string) (string, error) {
	fmt.Fprintln(cmd.OutOrStdout(), "press Enter to start recording, Enter again to stop")
	reader := bufio.NewReader(os.Stdin)
	if _, err := reader.ReadString('\n'); err != nil {
		return "", err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "recording…")

	capTool, err := voice.NewCaptureTool(captureCmd)
	if err != nil {
		return "", err
	}
	capTool.MaxDuration = 30 * time.Second
	rec, err := capTool.Record(ctx, capTool.MaxDuration)
	if err != nil {
		return "", err
	}
	defer rec.Cleanup()

	fmt.Fprintln(cmd.OutOrStdout(), "transcribing…")
	sttTool, err := voice.NewSTTTool(sttCmd)
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
