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

// MaxInFlightToolResultChars caps the size of a tool result appended
// to the live in-flight messages slice during the tool loop (context
// remediation Task 2, September 2026). The in-flight cap is
// deliberately three times the persist cap: the live context
// tolerates more than the durable store, and 24576 chars keeps
// generous headroom under typical provider tool-result limits while
// still stopping multi-hundred-KB payloads from swamping the window
// mid loop. Values above the cap are truncated head-first with the
// shared deterministic marker so persist-cap then in-flight-cap
// composes without double markers.
const MaxInFlightToolResultChars = 3 * MaxPersistedToolResultChars

// truncationMarker is the deterministic suffix appended to any tool
// result content truncated by the caps below. Content already
// carrying the marker is left alone, making the caps idempotent.
const truncationMarker = "\n[truncated: tool result exceeded %d chars; %d chars dropped]"

// truncationDetector matches the marker prefix regardless of the
// formatted counts, so already-marked content is detected even though
// the counts differ between passes.
const truncationDetector = "[truncated: tool result exceeded"

// capToolResult truncates tool result content head-first to the given
// cap, appending the deterministic truncation marker (formatted with
// the cap and the dropped count) when content exceeds it. Truncation
// cuts at the last newline inside the head region so the marker never
// splits a line mid-way. Content at or under the cap — or already
// carrying the truncation marker — passes through unchanged, making
// any sequence of caps idempotent.
//
// Expected: content to cap, and the maximum number of chars to keep.
// Returns: the capped content with marker, or the content unchanged.
// Side effects: None.
func capToolResult(content string, capChars int) string {
	if len(content) <= capChars {
		return content
	}
	if strings.Contains(content, truncationDetector) {
		return content
	}
	dropped := len(content) - capChars
	marker := fmt.Sprintf(truncationMarker, capChars, dropped)
	head := content[:capChars]
	if idx := strings.LastIndex(head, "\n"); idx > capChars-len(marker)-1 {
		head = head[:idx]
	}
	return head + marker
}

// capPersistedToolResult truncates tool result content to
// MaxPersistedToolResultChars, appending a deterministic truncation
// marker when content is dropped. Content at or under the cap — or
// already carrying the marker — passes through unchanged.
//
// Expected: content is the tool result content about to be persisted.
// Returns: the content, capped and marked when it exceeds the cap.
// Side effects: None.
func capPersistedToolResult(content string) string {
	return capToolResult(content, MaxPersistedToolResultChars)
}

// capInFlightToolResult truncates tool result content to
// MaxInFlightToolResultChars before it is appended to the live
// messages slice. Content at or under the cap — or already carrying
// the truncation marker from a prior persist cap — passes through
// unchanged, so persist-cap then in-flight-cap never double-marks.
//
// Expected: content is the tool result content about to be appended
// to the in-flight messages.
// Returns: the content, capped and marked when it exceeds the cap.
// Side effects: None.
func capInFlightToolResult(content string) string {
	return capToolResult(content, MaxInFlightToolResultChars)
}
