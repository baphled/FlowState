package voice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePiperFake writes an 0755 script emitting the given WAV bytes
// on stdout, optionally logging stdin and/or argv to files.
func writePiperFake(t *testing.T, wav []byte, logStdin, logArgv string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "piper")
	script := "#!/bin/sh\n"
	if logStdin != "" {
		script += "cat >> " + logStdin + "\n"
		if logArgv == "" {
			script += "printf '\\n' >> " + logStdin + "\n"
		}
	} else {
		script += "cat >/dev/null\n"
	}
	if logArgv != "" {
		script += "printf '%s\\n' \"$@\" > " + logArgv + "\n"
	}
	script += "printf '%s' " + shellQuote(string(wav)) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write piper fake: %v", err)
	}
	return bin
}

// shellQuote wraps s in single quotes for shell literal use.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TestPreprocessTTS asserts markdown stripping and symbol expansion.
func TestPreprocessTTS(t *testing.T) {
	got := PreprocessTTS("use `go test`:\n```go\nfmt.Println(1)\n```\nfoo -> bar & baz => qux")
	want := "use go test foo to bar and baz to qux"
	if got != want {
		t.Fatalf("PreprocessTTS = %q, want %q", got, want)
	}
}

// TestPreprocessTTSEmpty asserts an all-code reply leaves nothing.
func TestPreprocessTTSEmpty(t *testing.T) {
	if got := PreprocessTTS("```go\nx\n```"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

// TestSplitSentences asserts sentence boundaries.
func TestSplitSentences(t *testing.T) {
	got := splitSentences("First sentence here. Second sentence follows!")
	if len(got) != 2 || got[0] != "First sentence here." || got[1] != "Second sentence follows!" {
		t.Fatalf("splitSentences = %#v", got)
	}
}

// TestSynthesizePlainReturnsWAV asserts bytes flow through per sentence.
func TestSynthesizePlainReturnsWAV(t *testing.T) {
	wav := []byte("RIFFfakeWAVbytes")
	cmd := writePiperFake(t, wav, "", "")
	tool := &TTSTool{Command: cmd, Model: "en_GB-alan-medium"}
	got, err := tool.Synthesize(context.Background(), "hello there")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(got) != string(wav) {
		t.Fatalf("wav = %q, want %q", got, wav)
	}
}

// TestSynthesizeConcatenatesSentences asserts one child per sentence
// and byte concatenation.
func TestSynthesizeConcatenatesSentences(t *testing.T) {
	dir := t.TempDir()
	stdinLog := filepath.Join(dir, "stdin.log")
	cmd := writePiperFake(t, []byte("RIFF"), stdinLog, "")
	tool := &TTSTool{Command: cmd}
	got, err := tool.Synthesize(context.Background(), "First sentence here. Second sentence follows!")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(got) != "RIFFRIFF" {
		t.Fatalf("concat = %q", got)
	}
	logged, _ := os.ReadFile(stdinLog)
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 2 || lines[0] != "First sentence here." || lines[1] != "Second sentence follows!" {
		t.Fatalf("stdin log = %#v, want two invocations", lines)
	}
}

// TestSynthesizeUnavailableIsClear asserts a missing binary yields
// ErrPiperUnavailable, never an espeak-ng fallback.
func TestSynthesizeUnavailableIsClear(t *testing.T) {
	tool := &TTSTool{Command: filepath.Join(t.TempDir(), "no-piper-here")}
	if _, err := tool.Synthesize(context.Background(), "hello"); !errors.Is(err, ErrPiperUnavailable) {
		t.Fatalf("err = %v, want ErrPiperUnavailable", err)
	}
}

// TestSynthesizeTuningFlags asserts piper argv carries the voice
// tuning flags.
func TestSynthesizeTuningFlags(t *testing.T) {
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv.log")
	cmd := writePiperFake(t, []byte("RIFF"), "", argvLog)
	tool := &TTSTool{
		Command:         cmd,
		Model:           "en_GB-alan-medium",
		LengthScale:     1.2,
		NoiseScale:      0.6,
		SentenceSilence: 0.4,
	}
	if _, err := tool.Synthesize(context.Background(), "tuning check"); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	logged, _ := os.ReadFile(argvLog)
	argv := strings.Fields(string(logged))
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--model en_GB-alan-medium",
		"--length_scale 1.2",
		"--noise_scale 0.6",
		"--sentence_silence 0.4",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q missing %q", joined, want)
		}
	}
}

// TestSynthesizeEmptyTextIsNoop asserts empty replies yield nil bytes.
func TestSynthesizeEmptyTextIsNoop(t *testing.T) {
	tool := &TTSTool{Command: "piper"}
	got, err := tool.Synthesize(context.Background(), "   ")
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil, nil", got, err)
	}
}
