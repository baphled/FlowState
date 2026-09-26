package engine

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// toolMessageCompactionDetector is the deterministic marker prefix stamped
// onto tool-result content replaced by compactOldToolResults. Content
// already carrying the marker is skipped, making the pass idempotent.
const toolMessageCompactionDetector = "[tool-result compacted:"

// compactOldToolResults replaces the content of older tool-result
// messages with a deterministic placeholder once the number of tool
// messages in the slice exceeds threshold. The most recent keepRecent
// tool results are left intact. The input slice is never mutated; the
// returned slice is a fresh copy when any message changes and the
// original slice otherwise.
//
// Expected:
//   - msgs is the in-flight provider message slice.
//   - threshold is the tool-message count above which compaction fires.
//   - keepRecent is how many of the newest tool results stay intact.
//
// Returns:
//   - The (possibly new) message slice with old tool results stubbed.
//
// Side effects:
//   - None. Pure function.
func compactOldToolResults(msgs []provider.Message, threshold, keepRecent int) []provider.Message {
	toolCount := 0
	for _, m := range msgs {
		if m.Role == "tool" {
			toolCount++
		}
	}
	if toolCount <= threshold {
		return msgs
	}
	guardStopIndex := len(msgs)
	{
		seen := 0
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role != "tool" {
				continue
			}
			seen++
			if seen >= keepRecent {
				guardStopIndex = i
				break
			}
		}
	}
	out := make([]provider.Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < guardStopIndex; i++ {
		m := &out[i]
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, toolMessageCompactionDetector) {
			continue
		}
		m.Content = compactToolResultPlaceholder(m.Content)
	}
	return out
}

// compactToolResultPlaceholder returns the compacted placeholder replacing a
// tool result, preserving the first and last lines plus the original size.
//
// Returns:
//   - The placeholder content in the compaction detector format.
//
// Expected:
//   - content is the full original tool-result text.
//
// Side effects:
//   - None.
func compactToolResultPlaceholder(content string) string {
	lines := strings.Split(content, "\n")
	first := lines[0]
	last := lines[len(lines)-1]
	if last == "" && len(lines) > 1 {
		last = lines[len(lines)-2]
	}
	return fmt.Sprintf("%s first=%q last=%q original=%d bytes]\n", toolMessageCompactionDetector, first, last, len(content))
}
