package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Capture formats. whisper.cpp's CLI consumes raw 16kHz mono s16le
// WAV; both parecord (--format=s16le --rate=16000 --channels=1) and
// arecord (-f S16_LE -r 16000 -c 1 -t wav) can emit it directly, so
// the capture tool never re-encodes audio itself.
const (
	SampleRate    = 16000
	Channels      = 1
	BitsPerSample = 16
)

// DefaultCaptureCommands lists the capture binaries tried in order
// when FLOWSTATE_VOICE_CAPTURE is unset: PipeWire's parecord first,
// ALSA's arecord as the fallback.
var DefaultCaptureCommands = []string{
	"parecord --raw --format=s16le --rate=16000 --channels=1 {file}",
	"arecord -q -f S16_LE -r 16000 -c 1 -t wav {file}",
}

// ErrCaptureUnavailable is returned when no capture binary can be
// resolved. Callers surface it verbatim — the message doubles as the
// user-facing guidance.
var ErrCaptureUnavailable = errors.New(
	"voice: no audio capture binary found (tried parecord, arecord); " +
		"install pipewire-utils or alsa-utils, or set FLOWSTATE_VOICE_CAPTURE",
)

// CaptureTool records microphone audio to a temporary WAV file using
// an external capture command.
type CaptureTool struct {
	// Command is the shell-free command template containing a
	// "{file}" placeholder. Split on whitespace; no shell involved.
	Command string
	// MaxDuration bounds recording length. Zero means 30s.
	MaxDuration time.Duration
}

// NewCaptureTool resolves the capture command from the explicit
// template, the FLOWSTATE_VOICE_CAPTURE env var, or the first of
// DefaultCaptureCommands whose binary exists on PATH.
//
// Expected:
//   - command is a whitespace-split command template with a "{file}"
//     placeholder, or empty to resolve from env/defaults.
//
// Returns:
//   - A configured *CaptureTool.
//   - ErrCaptureUnavailable when no command resolves.
//
// Side effects:
//   - Reads the FLOWSTATE_VOICE_CAPTURE environment variable.
//   - Probes PATH for parecord/arecord via exec.LookPath.
func NewCaptureTool(command string) (*CaptureTool, error) {
	tmpl := command
	if tmpl == "" {
		tmpl = os.Getenv("FLOWSTATE_VOICE_CAPTURE")
	}
	if tmpl == "" {
		for _, candidate := range DefaultCaptureCommands {
			bin := strings.Fields(candidate)[0]
			if _, err := exec.LookPath(bin); err == nil {
				tmpl = candidate
				break
			}
		}
	}
	if tmpl == "" {
		return nil, ErrCaptureUnavailable
	}
	return &CaptureTool{Command: tmpl, MaxDuration: 30 * time.Second}, nil
}

// Recording is the result of a successful capture.
type Recording struct {
	// Path is the temporary WAV file path. The caller MUST call
	// Cleanup when done.
	Path string
	// Duration is how long the capture ran.
	Duration time.Duration
}

// Cleanup removes the temporary WAV file. Safe to call twice.
//
// Side effects:
//   - Deletes the file at Path and clears the field.
func (r *Recording) Cleanup() {
	if r == nil || r.Path == "" {
		return
	}
	_ = os.Remove(r.Path)
	r.Path = ""
}

// Record captures audio for up to duration (bounded by
// MaxDuration) into a new temporary WAV file.
//
// Expected:
//   - ctx is non-nil; cancellation kills the child process.
//   - duration is the intended recording window; clamped to
//     MaxDuration when out of range.
//
// Returns:
//   - A *Recording whose Path holds the captured WAV.
//   - An error when no command is configured, temp file creation
//     fails, or the capture command exits non-zero.
//
// Side effects:
//   - Creates a 0600 temporary file and spawns the capture binary.
//   - Removes the partial file on cancellation or failure so a
//     SIGINT mid-capture leaves no stray WAV.
func (c *CaptureTool) Record(ctx context.Context, duration time.Duration) (*Recording, error) {
	if c == nil || c.Command == "" {
		return nil, ErrCaptureUnavailable
	}
	if c.MaxDuration > 0 && (duration <= 0 || duration > c.MaxDuration) {
		duration = c.MaxDuration
	}

	f, err := os.CreateTemp("", "flowstate-voice-*.wav")
	if err != nil {
		return nil, fmt.Errorf("voice: create temp wav: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("voice: close temp wav: %w", err)
	}

	rec := &Recording{Path: path, Duration: duration}
	args, err := buildCaptureArgs(c.Command, path)
	if err != nil {
		rec.Cleanup()
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, duration+2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if err := cmd.Run(); err != nil {
		rec.Cleanup()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("voice: capture cancelled: %w", ctx.Err())
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s: %w", ErrCaptureUnavailable, args[0], err)
		}
		return nil, fmt.Errorf("voice: capture command %q failed: %w", args[0], err)
	}
	return rec, nil
}

// buildCaptureArgs splits the command template on whitespace and
// substitutes the {file} placeholder. No shell is invoked; audio
// paths never flow through shell interpolation (Q2 contract).
//
// Expected:
//   - template is a non-empty whitespace-split command with "{file}".
//   - file is the recording path to substitute.
//
// Returns:
//   - The argv slice for exec.Command.
//   - An error when the template is empty after splitting.
//
// Side effects:
//   - None.
func buildCaptureArgs(template, file string) ([]string, error) {
	fields := strings.Fields(strings.ReplaceAll(template, "{file}", file))
	if len(fields) == 0 {
		return nil, errors.New("voice: empty capture command")
	}
	return fields, nil
}
