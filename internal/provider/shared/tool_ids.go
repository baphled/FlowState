package shared

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ToolIDTarget identifies the wire-format family a tool-call ID must conform
// to when it is rendered in an outgoing provider request.
//
// Different provider APIs emit and expect tool-call IDs in incompatible
// formats: Anthropic uses "toolu_" prefixed IDs, while OpenAI-style providers
// (openai, github-copilot, zai, openzen, ollama-openai-compat) use "call_"
// prefixed IDs. When failover swaps the active provider mid-conversation,
// IDs that originated with one family must be translated to the other so that
// the intra-request matching between tool_use / tool_result (Anthropic) or
// tool_calls[].id / tool_call_id (OpenAI) succeeds.
type ToolIDTarget int

const (
	// ToolIDTargetAnthropic indicates the id will be sent to the Anthropic
	// Messages API and must use the "toolu_" prefix.
	ToolIDTargetAnthropic ToolIDTarget = iota

	// ToolIDTargetOpenAI indicates the id will be sent to an OpenAI-compatible
	// Chat Completions endpoint and must use the "call_" prefix.
	ToolIDTargetOpenAI
)

const (
	anthropicToolIDPrefix = "toolu_"
	openaiToolIDPrefix    = "call_"
	translatedSuffixLen   = 24
)

// TranslateToolCallID returns a wire-format tool-call id for the given target
// provider family, rewriting ids that do not already match the target's
// expected prefix. Ids that already match the target prefix are returned
// unchanged, which preserves the happy-path (single-provider conversations)
// exactly as before.
//
// The translation is deterministic: the same canonical id and target will
// always produce the same wire id. This is load-bearing — within a single
// request we translate the assistant "tool_use" block and the user
// "tool_result" block independently, and the two results must be identical
// for the API to accept the pairing. Determinism is achieved by hashing the
// canonical id and encoding a prefix of the digest in hex, concatenated with
// the target's expected prefix.
//
// Expected:
//   - canonical is a tool-call id as recorded in session history. It may be
//     any non-empty string. Common shapes are "toolu_..." (Anthropic-emitted),
//     "call_..." (OpenAI-style-emitted), or an opaque id from some other
//     source. An empty canonical id returns an empty string.
//   - target identifies the provider family the caller will send the id to.
//
// Returns:
//   - A wire-safe tool-call id suitable for the target provider. For an
//     empty input, the empty string is returned unchanged so callers can
//     short-circuit missing ids without special-casing.
//
// Side effects:
//   - None.
func TranslateToolCallID(canonical string, target ToolIDTarget) string {
	if canonical == "" {
		return ""
	}

	targetPrefix := prefixFor(target)
	if strings.HasPrefix(canonical, targetPrefix) {
		return canonical
	}

	sum := sha256.Sum256([]byte(canonical))
	suffix := hex.EncodeToString(sum[:])
	if len(suffix) > translatedSuffixLen {
		suffix = suffix[:translatedSuffixLen]
	}
	return targetPrefix + suffix
}

// prefixFor returns the wire prefix for the given target provider family.
//
// Expected:
//   - target is one of the defined ToolIDTarget constants. An unknown target
//     falls back to the Anthropic prefix, which is safe for the rewrite case
//     because unknown targets cannot originate unknown-prefixed native ids.
//
// Returns:
//   - The prefix string that a wire-safe id must start with.
//
// Side effects:
//   - None.
func prefixFor(target ToolIDTarget) string {
	if target == ToolIDTargetOpenAI {
		return openaiToolIDPrefix
	}
	return anthropicToolIDPrefix
}
