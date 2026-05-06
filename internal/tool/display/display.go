package tooldisplay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// primaryArgKeys maps well-known tool names to their primary display argument key.
var primaryArgKeys = map[string]string{
	"bash":       "command",
	"read":       "filePath",
	"write":      "filePath",
	"edit":       "filePath",
	"glob":       "pattern",
	"grep":       "pattern",
	"skill_load": "name",
}

// preferredFallbackKeys is the priority-ordered list of argument keys consulted
// for tools outside primaryArgKeys. The first match wins. This is the "tiered
// fallback" pattern: hand-coded tools render their canonical argument; unknown
// tools (MCP, delegation, coordination) render the most semantically informative
// argument we can find without per-tool wiring.
//
// Order matters. "query" comes first because semantic-search tools are the most
// common unknown class. "subagent_type" identifies delegations. "key" identifies
// coordination_store entries. Trailing entries are common metadata fields.
var preferredFallbackKeys = []string{
	"query",
	"subagent_type",
	"name",
	"key",
	"path",
	"id",
	"url",
	"title",
	"operation",
}

// sensitiveKeySubstrings flags arg keys whose values must be redacted before
// display. Match is case-insensitive substring against the key name.
var sensitiveKeySubstrings = []string{
	"password",
	"secret",
	"token",
	"apikey",
	"api_key",
	"auth",
	"credential",
}

// redactedPlaceholder is the literal string substituted for any sensitive arg value.
const redactedPlaceholder = "[REDACTED]"

// truncateLen is the maximum length for any rendered display value before truncation.
// Applied uniformly across bash commands and the unknown-tool fallback so MCP tools
// with large JSON blobs do not blow up the card.
const truncateLen = 80

// PrimaryArgKey returns the name of the primary argument for a given tool.
//
// Expected:
//   - name is a tool identifier (e.g. "bash", "read").
//
// Returns:
//   - The argument key used as the primary display value for that tool.
//   - An empty string when name is not a recognised tool.
//
// Side effects:
//   - None.
func PrimaryArgKey(name string) string {
	return primaryArgKeys[name]
}

// PrimaryArgValue computes the display value for a tool call across all tools,
// hand-coded or not. It is the single seam used by both Summary (TUI) and the
// session accumulator (persisted ToolInput).
//
// Resolution order:
//  1. If name is "delegate", render "<subagent_type>: <message>" so the
//     persisted ToolInput preserves the parent's brief alongside the routing
//     target. Without this the brief silently vanishes from the session
//     record, leaving "delegate: <agent>" as the entire trace of intent.
//  2. If name is in the hand-coded primaryArgKeys map, use args[primaryArgKeys[name]]
//     when present and a non-empty string.
//  3. Otherwise, walk preferredFallbackKeys and return the first key whose value
//     is a non-empty string.
//  4. Otherwise, if any string-valued arg exists, return a compact JSON object
//     containing all string-coercible args (sorted by key for determinism).
//  5. Otherwise return "" — caller renders just the tool name.
//
// Sensitive values (matched against sensitiveKeySubstrings) are replaced with
// "[REDACTED]" before being returned or serialised. The final value is
// truncated to truncateLen characters with "..." appended when it would
// otherwise exceed that limit.
//
// Expected:
//   - name is a tool identifier.
//   - args is the tool call argument map (may be nil).
//
// Returns:
//   - The display value (possibly empty), and a bool indicating whether a
//     non-empty value was found.
//
// Side effects:
//   - None.
func PrimaryArgValue(name string, args map[string]any) (string, bool) {
	if name == "delegate" {
		if v, ok := delegateDisplayValue(args); ok {
			return v, true
		}
	}

	if key := primaryArgKeys[name]; key != "" {
		if v, ok := args[key].(string); ok && v != "" {
			return truncate(redactIfSensitive(key, v)), true
		}
	}

	for _, key := range preferredFallbackKeys {
		v, ok := args[key]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr || s == "" {
			continue
		}
		return truncate(redactIfSensitive(key, s)), true
	}

	if encoded, ok := compactJSONFallback(args); ok {
		return truncate(encoded), true
	}

	return "", false
}

// delegateDisplayValue renders the delegate tool's display string as
// "<subagent_type>: <message>" so the persisted ToolInput retains both the
// routing target and the parent's brief. The combined string is truncated to
// truncateLen characters with "..." appended on overflow.
//
// Returns ok=false when neither subagent_type nor message is a usable string,
// allowing the caller to fall through to the generic resolution path.
func delegateDisplayValue(args map[string]any) (string, bool) {
	subagent, _ := args["subagent_type"].(string)
	message, _ := args["message"].(string)
	switch {
	case subagent != "" && message != "":
		return truncate(subagent + ": " + message), true
	case subagent != "":
		return truncate(subagent), true
	case message != "":
		return truncate(message), true
	}
	return "", false
}

// Summary formats a tool call as "name: primaryArg" for display purposes.
//
// Expected:
//   - name is a tool identifier.
//   - args contains the tool call argument map (may be nil).
//
// Returns:
//   - A string in the form "name: value" when a primary argument is resolvable.
//   - Just the tool name when no primary argument is found.
//   - Long values (including bash commands and unknown-tool JSON fallbacks)
//     are truncated at 80 characters with "...".
//
// Side effects:
//   - None.
func Summary(name string, args map[string]any) string {
	value, ok := PrimaryArgValue(name, args)
	if !ok {
		return name
	}
	return fmt.Sprintf("%s: %s", name, value)
}

// truncate caps s at truncateLen characters, appending "..." when truncation
// occurs. Returns s unchanged when within the limit.
func truncate(s string) string {
	if len(s) <= truncateLen {
		return s
	}
	return s[:truncateLen] + "..."
}

// redactIfSensitive returns redactedPlaceholder when key matches any
// sensitiveKeySubstrings entry (case-insensitive); otherwise returns value
// unchanged.
func redactIfSensitive(key, value string) string {
	lower := strings.ToLower(key)
	for _, sub := range sensitiveKeySubstrings {
		if strings.Contains(lower, sub) {
			return redactedPlaceholder
		}
	}
	return value
}

// compactJSONFallback builds a deterministic compact JSON object from the
// string-coercible entries in args, redacting sensitive keys. Returns the
// encoded string and true when at least one entry survived; otherwise returns
// "" and false.
//
// Determinism matters because a non-deterministic fallback would render
// different values across reloads of the same session.
func compactJSONFallback(args map[string]any) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	filtered := make(map[string]string, len(keys))
	orderedKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		s, ok := args[k].(string)
		if !ok || s == "" {
			continue
		}
		filtered[k] = redactIfSensitive(k, s)
		orderedKeys = append(orderedKeys, k)
	}
	if len(filtered) == 0 {
		return "", false
	}

	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range orderedKeys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kJSON, _ := json.Marshal(k)
		vJSON, _ := json.Marshal(filtered[k])
		sb.Write(kJSON)
		sb.WriteByte(':')
		sb.Write(vJSON)
	}
	sb.WriteByte('}')
	return sb.String(), true
}
