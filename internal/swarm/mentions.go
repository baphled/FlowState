package swarm

import "regexp"

// atMentionPattern matches @-mentions for agent or swarm names in a
// user message. The leading boundary clause (`^|[^a-zA-Z0-9_-]`) keeps
// substrings like "email@example.com" from being treated as mentions
// while allowing both message-start (`^@bug-hunt`) and
// space/punct-prefixed (`...send @bug-hunt this`) cases.
//
// Originally lived inline in the chat intent (`internal/tui/intents/
// chat/intent.go`); moved here so the SessionOrchestrator's @-mention
// scanning path can reuse the same regex without cross-importing the
// chat package — see ADR - Session Orchestrator for Surface Parity
// for the broader unification context.
var atMentionPattern = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_-])@([a-zA-Z0-9_][a-zA-Z0-9_-]*)`)

// ExtractAtMentions returns the set of @-mentioned names found in the
// given message, preserving their original casing. The leading "@" is
// stripped; callers feed each result through ResolveTarget (or the
// agent registry) to classify it.
//
// Expected:
//   - message is the raw user prompt.
//
// Returns:
//   - A slice of mention tokens without the leading "@"; empty if none.
//
// Side effects:
//   - None.
func ExtractAtMentions(message string) []string {
	matches := atMentionPattern.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	return out
}
