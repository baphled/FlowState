package voice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSTTScript writes a shell script echoing fixed text on stdout
// and returns its "{file}"-template invocation.
func fakeSTTScript(t *testing.T, transcript string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-stt")
	body := "#!/bin/sh\necho " + strings.TrimSpace(transcript) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake stt: %v", err)
	}
	return script + " {file}"
}

func TestSTTToolTranscribe(t *testing.T) {
	tool, err := NewSTTTool(fakeSTTScript(t, "hello agent"))
	if err != nil {
		t.Fatalf("NewSTTTool: %v", err)
	}
	f := filepath.Join(t.TempDir(), "r.wav")
	if err := os.WriteFile(f, []byte("RIFF....WAVE"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := tool.Transcribe(context.Background(), f)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got != "hello agent" {
		t.Fatalf("transcript = %q, want %q", got, "hello agent")
	}
}

func TestSTTToolEmptyTranscript(t *testing.T) {
	tool, err := NewSTTTool(fakeSTTScript(t, ""))
	if err != nil {
		t.Fatalf("NewSTTTool: %v", err)
	}
	f := filepath.Join(t.TempDir(), "r.wav")
	_, err = tool.Transcribe(context.Background(), f)
	if !errors.Is(err, ErrEmptyTranscript) {
		t.Fatalf("err = %v, want ErrEmptyTranscript", err)
	}
}

func TestSTTToolUnavailableWithoutBinary(t *testing.T) {
	t.Setenv("FLOWSTATE_VOICE_STT", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := NewSTTTool(""); !errors.Is(err, ErrSTTUnavailable) {
		t.Fatalf("err = %v, want ErrSTTUnavailable", err)
	}
}
