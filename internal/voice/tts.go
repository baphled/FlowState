// Package voice — TTS half of the talk pipeline. Spoken responses
// are strictly opt-in (config voice.tts_enabled); the tool shells out
// to a local piper binary, feeding the reply text on stdin, and
// returns a clear error when the binary is absent. Synthesis for the
// HTTP API pipes the child's stdout back as WAV bytes; Speak remains
// the host-playback path.
package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
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
// while spoken output is enabled. Synthesis never falls back beyond
// piper: a missing piper is an error, not a degradation.
var ErrTTSUnavailable = errors.New(
	"voice: no text-to-speech binary found (tried piper, espeak-ng); " +
		"install piper or espeak-ng, or set FLOWSTATE_VOICE_TTS",
)

// ErrPiperUnavailable is returned when Synthesize cannot resolve a
// piper binary. There is deliberately no espeak-ng fallback for
// synthesis: browser playback needs WAV bytes that only piper
// produces predictably.
var ErrPiperUnavailable = errors.New(
	"voice: piper is unavailable for synthesis; install piper " +
		"and configure voice.tts_cmd or FLOWSTATE_VOICE_TTS",
)

// codeFenceRe matches fenced code blocks (``` or ~~~) so synthesis
// skips source code entirely.
var codeFenceRe = regexp.MustCompile("(?s)```.*?```|(?s)~~~.*?~~~")

// colonDropRe strips colons that introduce fenced code blocks,
// after the fences themselves are removed.
var colonDropRe = regexp.MustCompile(`:\s+`)

// inlineCodeRe matches backtick-quoted inline code spans.
var inlineCodeRe = regexp.MustCompile("`([^`]*)`")

// TTSTool speaks reply text through a local TTS binary. The reply
// text arrives on the child's stdin; audio goes to the child's
// default output. No shell is involved.
type TTSTool struct {
	// Command is the argv template for the TTS binary.
	Command string
	// Model is the piper voice model name passed via --model.
	Model string
	// LengthScale is piper's --length_scale speech-rate control.
	LengthScale float64
	// NoiseScale is piper's --noise_scale prosody control.
	NoiseScale float64
	// SentenceSilence is piper's --sentence_silence gap in seconds.
	SentenceSilence float64
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

// PreprocessTTS cleans reply text for speech: fenced code blocks are
// dropped, inline code spans keep their inner text, arrow and
// ampersand symbols are expanded to spoken words, and the remainder
// is collapsed to a single space-padded sentence.
//
// Expected:
//   - text is the raw agent reply.
//
// Returns:
//   - The speech-ready text; empty when nothing speakable remains.
//
// Side effects:
//   - None.
func PreprocessTTS(text string) string {
	cleaned := codeFenceRe.ReplaceAllString(text, " ")
	cleaned = inlineCodeRe.ReplaceAllString(cleaned, "$1")
	cleaned = colonDropRe.ReplaceAllString(cleaned, " ")
	replacer := strings.NewReplacer(
		"=>", " to ",
		"->", " to ",
		"&", " and ",
	)
	cleaned = replacer.Replace(cleaned)
	fields := strings.Fields(cleaned)
	return strings.Join(fields, " ")
}

// splitSentences breaks preprocessed text into speakable sentences
// on ., !, or ? boundaries, dropping empty fragments.
//
// Expected:
//   - text is already PreprocessTTS output.
//
// Returns:
//   - The non-empty sentences in spoken order.
//
// Side effects:
//   - None.
func splitSentences(text string) []string {
	splitter := regexp.MustCompile(`([.!?])\s+`)
	punctuated := splitter.ReplaceAllString(text, "$1\n")
	parts := strings.Split(punctuated, "\n")
	sentences := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			sentences = append(sentences, trimmed)
		}
	}
	return sentences
}

// synthArgs builds the piper argv: the command template's base
// binary plus tuning flags for the configured voice.
//
// Expected:
//   - sentence is a single speakable sentence.
//
// Returns:
//   - The argv for the piper child.
//
// Side effects:
//   - None.
func (t *TTSTool) synthArgs(sentence string) []string {
	fields := strings.Fields(t.Command)
	args := make([]string, 0, len(fields)+8)
	args = append(args, fields...)
	if t.Model != "" {
		args = append(args, "--model", t.Model)
	}
	if t.LengthScale != 0 {
		args = append(args, "--length_scale", formatTTSFloat(t.LengthScale))
	}
	if t.NoiseScale != 0 {
		args = append(args, "--noise_scale", formatTTSFloat(t.NoiseScale))
	}
	if t.SentenceSilence != 0 {
		args = append(args, "--sentence_silence", formatTTSFloat(t.SentenceSilence))
	}
	args = append(args, "--stdin_input")
	return args
}

// formatTTSFloat renders a tuning value in piper's expected decimal
// form (never scientific notation).
//
// Expected:
//   - v is a non-zero tuning value.
//
// Returns:
//   - The decimal string form.
//
// Side effects:
//   - None.
func formatTTSFloat(v float64) string {
	return strconvFormatFloat(v)
}

// Synthesize converts reply text to WAV bytes by piping each
// preprocessed sentence to piper on stdin and concatenating the
// per-sentence WAV stdout. There is no host playback and no
// espeak-ng fallback; a missing piper is a clear error.
//
// Expected:
//   - ctx is non-nil; cancellation kills the child.
//   - text is the agent reply; empty text yields empty bytes.
//
// Returns:
//   - The concatenated WAV bytes.
//   - ErrPiperUnavailable when piper cannot resolve or run.
//
// Side effects:
//   - Spawns one piper child per sentence.
func (t *TTSTool) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if t == nil {
		return nil, ErrPiperUnavailable
	}
	cleaned := PreprocessTTS(text)
	if cleaned == "" {
		return nil, nil
	}
	sentences := splitSentences(cleaned)
	var out bytes.Buffer
	for _, sentence := range sentences {
		wav, err := t.synthesizeSentence(ctx, sentence)
		if err != nil {
			return nil, err
		}
		out.Write(wav)
	}
	return out.Bytes(), nil
}

// synthesizeSentence runs one piper invocation for a single
// sentence, returning its WAV stdout bytes.
//
// Expected:
//   - ctx is non-nil; sentence is non-empty.
//
// Returns:
//   - The sentence's WAV bytes.
//   - ErrPiperUnavailable when the binary cannot resolve.
//   - An error wrapping the exit status when piper fails.
//
// Side effects:
//   - Spawns the piper child with the sentence on stdin.
func (t *TTSTool) synthesizeSentence(ctx context.Context, sentence string) ([]byte, error) {
	args := t.synthArgs(sentence)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(sentence)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s: %w", ErrPiperUnavailable, args[0], err)
		}
		return nil, fmt.Errorf("voice: piper synthesis failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// strconvFormatFloat renders v in the shortest exact decimal form so
// piper flags read naturally (1.2, not 1.20).
//
// Expected:
//   - v is a non-zero tuning value.
//
// Returns:
//   - The decimal string form without scientific notation.
//
// Side effects:
//   - None.
func strconvFormatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
