// Package voice — TTS half of the talk pipeline. Spoken responses
// are strictly opt-in (config voice.tts_enabled); the tool shells out
// to a local piper/espeak-ng style binary, feeding the reply text on
// stdin, and degrades to silence when the binary is absent.
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

// DefaultTTSTemplates lists the TTS binaries tried in order when
// FLOWSTATE_VOICE_TTS is unset: piper first (neural, offline),
// espeak-ng as the lightweight fallback.
var DefaultTTSTemplates = []string{
	"piper --stdin_input",
	"espeak-ng --stdin",
}

// ErrTTSUnavailable is returned when no TTS binary can be resolved
// while spoken output is enabled.
var ErrTTSUnavailable = errors.New(
	"voice: no text-to-speech binary found (tried piper, espeak-ng); " +
		"install piper or espeak-ng, or set FLOWSTATE_VOICE_TTS",
)

// TTSTool speaks reply text through a local TTS binary. The reply
// text arrives on the child's stdin; audio goes to the child's
// default output. No shell is involved.
type TTSTool struct {
	// Command is the argv template for the TTS binary.
	Command string
}

// NewTTSTool resolves the TTS command from the explicit template,
// the FLOWSTATE_VOICE_TTS env var, or the first of
// DefaultTTSTemplates whose binary exists on PATH.
//
// Expected:
//   - command is a whitespace-split command template, or empty to
//     resolve from env/defaults.
//
// Returns:
//   - A configured *TTSTool.
//   - ErrTTSUnavailable when no command resolves.
//
// Side effects:
//   - Reads the FLOWSTATE_VOICE_TTS environment variable.
//   - Probes PATH for piper/espeak-ng via exec.LookPath.
func NewTTSTool(command string) (*TTSTool, error) {
	tmpl := command
	if tmpl == "" {
		tmpl = os.Getenv("FLOWSTATE_VOICE_TTS")
	}
	if tmpl == "" {
		for _, candidate := range DefaultTTSTemplates {
			bin := strings.Fields(candidate)[0]
			if _, err := exec.LookPath(bin); err == nil {
				tmpl = candidate
				break
			}
		}
	}
	if tmpl == "" {
		return nil, ErrTTSUnavailable
	}
	return &TTSTool{Command: tmpl}, nil
}

// Speak feeds the reply text to the TTS binary on stdin so it is
// spoken aloud.
//
// Expected:
//   - ctx is non-nil; cancellation kills the child process.
//   - text is the reply to speak; empty text is a no-op.
//
// Returns:
//   - ErrTTSUnavailable when no command is configured.
//   - An error wrapping the exit status when the command fails.
//
// Side effects:
//   - Spawns the TTS binary with text on stdin; audio plays through
//     the child's default audio output.
func (t *TTSTool) Speak(ctx context.Context, text string) error {
	if t == nil || t.Command == "" {
		return ErrTTSUnavailable
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	fields := strings.Fields(t.Command)
	if len(fields) == 0 {
		return ErrTTSUnavailable
	}
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s: %w", ErrTTSUnavailable, fields[0], err)
		}
		return fmt.Errorf("voice: tts command %q failed: %w: %s", fields[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
