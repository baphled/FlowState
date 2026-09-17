package engine

import (
	"fmt"
	"strings"
)

// MaxPersistedToolResultChars caps the size of a tool result content
// persisted to the context store (P2, September 2026). Values above
// the cap are truncated head-first with a deterministic marker so the
// model and downstream diagnostics can tell a truncated payload from
// a complete one. The band is the 4–8K contract range; 8192 is the
// ceiling choice.
const MaxPersistedToolResultChars = 8192

// truncationMarker is the deterministic suffix appended to any tool
// result content truncated by capPersistedToolResult. Content already
// carrying the marker is left alone, making the cap idempotent.
const truncationMarker = "\n[truncated: tool result exceeded %d chars; %d chars dropped]"

// truncationDetector matches the marker prefix regardless of the
// formatted counts, so already-marked content is detected even though
// the counts differ between passes.
const truncationDetector = "[truncated: tool result exceeded"

// capPersistedToolResult truncates tool result content to
// MaxPersistedToolResultChars, appending a deterministic truncation
// marker when content is dropped. Content at or under the cap — or
// already carrying the marker — passes through unchanged.
//
// Expected: content is the tool result content about to be persisted.
// Returns: the content, capped and marked when it exceeds the cap.
// Side effects: None.
func capPersistedToolResult(content string) string {
	if len(content) <= MaxPersistedToolResultChars {
		return content
	}
	if strings.Contains(content, truncationDetector) {
		return content
	}
	dropped := len(content) - MaxPersistedToolResultChars
	marker := fmt.Sprintf(truncationMarker, MaxPersistedToolResultChars, dropped)
	head := content[:MaxPersistedToolResultChars]
	if idx := strings.LastIndex(head, "\n"); idx > MaxPersistedToolResultChars-len(marker)-1 {
		head = head[:idx]
	}
	return head + marker
}
