package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DefaultSTTCommand is the whisper.cpp CLI invocation: reads the WAV
// path as its trailing argument, prints the transcript on stdout.
const DefaultSTTCommand = "whisper-cli"

// ErrSTTUnavailable is returned when no STT binary can be resolved.
var ErrSTTUnavailable = errors.New(
	"voice: no speech-to-text binary found (whisper-cli); " +
		"install whisper.cpp's CLI, or set FLOWSTATE_VOICE_STT",
)

// ErrEmptyTranscript is returned when the STT command succeeds but
// emits no usable transcript — the user said nothing audible.
var ErrEmptyTranscript = errors.New(
	"voice: speech-to-text produced an empty transcript",
)

// STTTool wraps a local whisper-cli style command for
// speech-to-text.
type STTTool struct {
	// Command is the argv template; "{file}" is substituted with the
	// WAV path. Split on whitespace; no shell involved.
	Command string
}

// NewSTTTool resolves the STT command from the explicit template,
// the FLOWSTATE_VOICE_STT env var, or DefaultSTTCommand when
// whisper-cli is on PATH.
//
// Expected:
//   - command is a command template with "{file}", or empty to
//     resolve from env/defaults.
//
// Returns:
//   - A configured *STTTool.
//   - ErrSTTUnavailable when nothing resolves.
//
// Side effects:
//   - Reads FLOWSTATE_VOICE_STT and probes PATH via exec.LookPath.
func NewSTTTool(command string) (*STTTool, error) {
	tmpl := command
	if tmpl == "" {
		tmpl = os.Getenv("FLOWSTATE_VOICE_STT")
	}
	if tmpl == "" {
		if _, err := exec.LookPath("whisper-cli"); err == nil {
			tmpl = DefaultSTTCommand
		}
	}
	if tmpl == "" {
		return nil, ErrSTTUnavailable
	}
	return &STTTool{Command: tmpl}, nil
}

// Transcribe runs the STT command over the WAV at path and returns
// the trimmed stdout as the transcript.
//
// Expected:
//   - ctx is non-nil; cancellation kills the child process.
//   - path is an existing WAV file.
//
// Returns:
//   - The transcript string (whitespace-trimmed).
//   - ErrSTTUnavailable when no command is configured.
//   - An error wrapping the exit status when the command fails.
//   - ErrEmptyTranscript when stdout holds no usable text.
//
// Side effects:
//   - Spawns the STT binary with the WAV path as a literal argv
//     entry — the audio path never passes through shell
//     interpolation, and the transcript is sanitised to trimmed
//     printable text before use.
func (s *STTTool) Transcribe(ctx context.Context, path string) (string, error) {
	if s == nil || s.Command == "" {
		return "", ErrSTTUnavailable
	}
	args := sttArgs(s.Command, path)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s: %w", ErrSTTUnavailable, args[0], err)
		}
		return "", fmt.Errorf("voice: stt command %q failed: %w", args[0], err)
	}
	transcript := strings.TrimSpace(strings.TrimSpace(stdout.String()))
	if transcript == "" {
		return "", ErrEmptyTranscript
	}
	return transcript, nil
}

// sttArgs substitutes the {file} placeholder and splits the command
// template into argv.
//
// Expected:
//   - command is a non-empty template containing "{file}".
//   - path is the WAV file path.
//
// Returns:
//   - The argv slice for exec.Command.
//
// Side effects:
//   - None.
func sttArgs(command, path string) []string {
	return strings.Fields(strings.ReplaceAll(command, "{file}", path))
}
