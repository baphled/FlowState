//go:build e2e

package session

// CapTranscriptToolResultForTest exposes capTranscriptToolResult so e2e
// transcript-cap specs can pin the truncation contract.
func CapTranscriptToolResultForTest(content string) string {
	return capTranscriptToolResult(content)
}

// TranscriptTruncationMarkerForTest exposes the deterministic transcript
// truncation-marker prefix so e2e specs can assert marker presence.
func TranscriptTruncationMarkerForTest() string {
	return transcriptTruncationDetector
}
