package session

import (
	"strings"
	"testing"
)

func TestCapTranscriptToolResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		wantCap bool
	}{
		{name: "short content passes through unchanged", content: "ok", wantCap: false},
		{name: "content at exactly the cap passes through", content: strings.Repeat("a", MaxTranscriptToolResultChars), wantCap: false},
		{name: "content above the cap is truncated with marker", content: strings.Repeat("a", MaxTranscriptToolResultChars+1000), wantCap: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := capTranscriptToolResult(tt.content)
			if !tt.wantCap {
				if got != tt.content {
					t.Fatalf("content under cap was modified: got %d chars, want %d", len(got), len(tt.content))
				}
				return
			}
			if !strings.Contains(got, transcriptTruncationDetector) {
				t.Fatalf("truncated content missing marker: %q", got[:200])
			}
			if len(got) >= len(tt.content) {
				t.Fatalf("truncated content not smaller: got %d, original %d", len(got), len(tt.content))
			}
		})
	}
}

func TestCapTranscriptToolResultIdempotent(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("b", MaxTranscriptToolResultChars+5000)
	once := capTranscriptToolResult(big)
	twice := capTranscriptToolResult(once)
	if once != twice {
		t.Fatal("cap is not idempotent: second pass re-truncated already-marked content")
	}
}
