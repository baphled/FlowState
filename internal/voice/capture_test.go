package voice

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCaptureScript writes a shell script that emits a valid WAV at
// the {file} path and returns its path as the command template.
func fakeCaptureScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-capture")
	// Emit 44-byte header + 2 bytes of silence. The header is written
	// via printf %b escapes so the payload survives /bin/sh printf
	// implementations that treat \xNN in the format string differently
	// (dash printf emits literal "\x24" bytes, corrupting the RIFF
	// header and failing the WAV validation below on ubuntu CI).
	body := "#!/bin/sh\n# usage: fake-capture <file>\n"
	body += "printf '%b' '\\x52\\x49\\x46\\x46\\x24\\x00\\x00\\x00\\x57\\x41\\x56\\x45\\x66\\x6d\\x74\\x20\\x10\\x00\\x00\\x00\\x01\\x00\\x01\\x00\\x80\\x3e\\x00\\x00\\x80\\x3e\\x00\\x00\\x02\\x00\\x10\\x00\\x64\\x61\\x74\\x61\\x02\\x00\\x00\\x00\\x00\\x00' > \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake capture: %v", err)
	}
	return script + " {file}"
}

func TestCaptureToolRecordProducesWAVAndCleansUp(t *testing.T) {
	tool, err := NewCaptureTool(fakeCaptureScript(t))
	if err != nil {
		t.Fatalf("NewCaptureTool: %v", err)
	}
	rec, err := tool.Record(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	data, err := os.ReadFile(rec.Path)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	if len(data) < 44 {
		t.Fatalf("recording too short: %d bytes", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("not a WAV file: %q", data[0:12])
	}
	rate := binary.LittleEndian.Uint32(data[24:28])
	channels := binary.LittleEndian.Uint16(data[22:24])
	bits := binary.LittleEndian.Uint16(data[34:36])
	if rate != SampleRate || channels != Channels || bits != BitsPerSample {
		t.Fatalf("format = %dHz %dch %dbit, want 16000/1/16", rate, channels, bits)
	}
	rec.Cleanup()
	if _, err := os.Stat(rec.Path); !os.IsNotExist(err) {
		t.Fatalf("temp file not removed after Cleanup")
	}
}

func TestCaptureToolUnavailableWhenNoBinary(t *testing.T) {
	t.Setenv("FLOWSTATE_VOICE_CAPTURE", "")
	// All default binaries hidden via empty PATH.
	t.Setenv("PATH", t.TempDir())
	if _, err := NewCaptureTool(""); err == nil {
		t.Fatal("expected ErrCaptureUnavailable with empty PATH")
	} else if err.Error() == "" {
		t.Fatal("error message must be actionable")
	}
}
