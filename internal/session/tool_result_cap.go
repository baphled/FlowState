package session

import (
	"fmt"
	"strings"
)

// MaxTranscriptToolResultChars caps the size of a tool result appended
// to the session transcript accumulator. It mirrors the engine's
// MaxInFlightToolResultChars (internal/engine/tool_result_cap.go): the
// session package cannot import the engine package (the engine imports
// session, so that would cycle), so the constant and its truncation
// marker are duplicated here with identical semantics and format.
const MaxTranscriptToolResultChars = 24576

// transcriptTruncationMarker is the deterministic suffix appended to
// tool result content truncated for the transcript. The format is
// identical to the engine's marker so downstream diagnostics treat
// both caps the same way.
const transcriptTruncationMarker = "\n[truncated: tool result exceeded %d chars; %d chars dropped]"

// transcriptTruncationDetector matches the marker prefix regardless of
// the formatted counts, so already-marked content is not re-marked.
const transcriptTruncationDetector = "[truncated: tool result exceeded"

// capTranscriptToolResult truncates tool result content head-first to
// MaxTranscriptToolResultChars, appending the deterministic truncation
// marker when content exceeds it. Content at or under the cap — or
// already carrying the marker — passes through unchanged, making the
// cap idempotent and composable with the engine's caps.
//
// Expected: content to cap.
// Returns: the capped content with marker, or the content unchanged.
// Side effects: None.
func capTranscriptToolResult(content string) string {
	capChars := MaxTranscriptToolResultChars
	if len(content) <= capChars {
		return content
	}
	if strings.Contains(content, transcriptTruncationDetector) {
		return content
	}
	dropped := len(content) - capChars
	marker := fmt.Sprintf(transcriptTruncationMarker, capChars, dropped)
	head := content[:capChars]
	if idx := strings.LastIndex(head, "\n"); idx > capChars-len(marker)-1 {
		head = head[:idx]
	}
	return head + marker
}
