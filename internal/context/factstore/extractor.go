package factstore

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// FactExtractor pulls Facts from a sequence of session messages.
//
// The default Phase B implementation is regex-driven and walks only
// user/assistant text. Phase C will swap in an LLM-backed extractor
// that also reaches into compacted/cold tool-result payloads. The
// engine wire-in does not care which implementation is plugged in;
// the interface is the load-bearing piece.
type FactExtractor interface {
	Extract(ctx context.Context, sessionID string, msgs []provider.Message) ([]Fact, error)
}

// regexPattern bundles a compiled pattern with the capture-group index
// that yields the fact text. Group 0 means "use the whole match".
type regexPattern struct {
	re       *regexp.Regexp
	groupIdx int
}

// regexFactExtractor captures durable claims via a small bank of
// case-insensitive regular expressions. The patterns are conservative
// on purpose — false positives are noisier than false negatives at
// recall time, where the overlap ranker can already narrow.
type regexFactExtractor struct {
	patterns []regexPattern
}

// NewRegexFactExtractor returns the production default extractor.
//
// Returns:
//   - A FactExtractor that captures explicit always/never/remember
//     statements and naming/identifier definitions ("the X is named Y",
//     "the X id is Y"). Tool-result messages are NEVER scanned.
func NewRegexFactExtractor() FactExtractor {
	return &regexFactExtractor{
		patterns: []regexPattern{
			{re: regexp.MustCompile(`(?im)(?:^|\b)remember[:,]?\s+(.+?)(?:[\.\?!]|$)`), groupIdx: 1},
			{re: regexp.MustCompile(`(?im)(?:^|\b)note[:,]?\s+(.+?)(?:[\.\?!]|$)`), groupIdx: 1},
			{re: regexp.MustCompile(`(?im)^(always\s+.+?)(?:[\.\?!]|$)`), groupIdx: 1},
			{re: regexp.MustCompile(`(?im)^(never\s+.+?)(?:[\.\?!]|$)`), groupIdx: 1},
			{re: regexp.MustCompile(`(?im)\b(the\s+\S+\s+(?:is|are)\s+named\s+\S+[^\.\?!\n]*)`), groupIdx: 1},
			{re: regexp.MustCompile(`(?im)\b(the\s+\S+\s+(?:id|client_id|secret|key)\s+is\s+\S+[^\.\?!\n]*)`), groupIdx: 1},
		},
	}
}

// Extract walks msgs and yields one Fact per pattern match. Tool
// messages are skipped — Phase B's regex pass is too noisy for the
// shape of typical tool output, and the LLM-driven Phase C extractor
// will own that surface area.
//
// Expected:
//   - sessionID stamps every emitted Fact's SessionID; non-empty in
//     production callers.
//   - msgs is the session's full message history in chronological
//     order.
//
// Returns:
//   - The extracted facts in the order they appear in msgs.
//   - A nil error today; reserved for future implementations whose
//     extraction can fail (LLM call timeouts, etc.).
func (e *regexFactExtractor) Extract(_ context.Context, sessionID string, msgs []provider.Message) ([]Fact, error) {
	out := make([]Fact, 0, 8)
	for i, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		sourceID := messageSourceID(m, i)
		for _, p := range e.patterns {
			matches := p.re.FindAllStringSubmatch(m.Content, -1)
			for _, match := range matches {
				if p.groupIdx >= len(match) {
					continue
				}
				text := strings.TrimSpace(match[p.groupIdx])
				if text == "" {
					continue
				}
				out = append(out, Fact{
					Text:            text,
					SourceMessageID: sourceID,
					SessionID:       sessionID,
					CreatedAt:       time.Now().UTC(),
				})
			}
		}
	}
	return out, nil
}

// messageSourceID returns a stable id for msg at position i. Tool-call
// ids win when present so re-extracting a session-on-disk produces
// identical SourceMessageIDs across runs; otherwise a positional
// fallback ("msg-<i>-<role>") keeps the field non-empty for dedup.
func messageSourceID(msg provider.Message, i int) string {
	if len(msg.ToolCalls) > 0 && msg.ToolCalls[0].ID != "" {
		return msg.ToolCalls[0].ID
	}
	return fmt.Sprintf("msg-%d-%s", i, msg.Role)
}
