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
	// Emit 44-byte header + 2 bytes of silence. The payload uses
	// POSIX-mandated OCTAL \NNN escapes under printf '%b' so every
	// /bin/sh (dash on ubuntu CI, bash elsewhere) expands it to the
	// same bytes; dash does not honour \xNN escapes in %b, which
	// corrupted the RIFF header and failed WAV validation in CI.
	body := "#!/bin/sh\n# usage: fake-capture <file>\n"
	body += "printf '%b' '\\122\\111\\106\\106\\044\\000\\000\\000\\127\\101\\126\\105\\146\\155\\164\\040\\020\\000\\000\\000\\001\\000\\001\\000\\200\\076\\000\\000\\200\\076\\000\\000\\002\\000\\020\\000\\144\\141\\164\\141\\002\\000\\000\\000\\000\\000' > \"$1\"\n"
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
