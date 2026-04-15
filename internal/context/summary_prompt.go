package context

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/baphled/flowstate/internal/provider"
)

// ErrEmptySummaryInput is returned by RenderSummaryPrompt when the caller
// supplies no messages to summarise. Rendering a prompt for an empty slice
// would yield an instruction with no substance, so we reject early rather
// than ask the model to summarise nothing.
var ErrEmptySummaryInput = errors.New("context: cannot render summary prompt for empty message slice")

// summaryPromptSystemLines assembles the system prompt from short line
// fragments so each source line stays within the project's 140-character
// linter cap. The joined string is exposed via SummaryPromptSystem.
var summaryPromptSystemLines = []string{
	"You are a strict transcript summariser for an AI coding agent.",
	"",
	"You produce only valid JSON matching the CompactionSummary schema supplied in the user prompt.",
	"You never emit prose, markdown code fences, preambles, or trailing commentary.",
	"You never reproduce raw tool-call identifiers: any string starting with \"toolu_\" or \"call_\"",
	"is an identifier and MUST NOT appear anywhere in your output.",
	"Refer to tool invocations by name and purpose only.",
}

// SummaryPromptSystem is the system prompt used alongside the rendered user
// prompt by the L2 auto-compactor. It reinforces the "JSON only, no ids"
// contract separately from the user-facing instructions so that providers
// which honour system prompts strongly (for example Anthropic) receive the
// constraint twice.
//
// Downstream code (T9b) is expected to attach this value verbatim as the
// system message when calling the summariser.
var SummaryPromptSystem = strings.Join(summaryPromptSystemLines, "\n")

// summaryPromptLines is the assembled user-prompt template, built from short
// fragments so that individual source lines stay under the 140-character
// linter cap. The fragments are joined with newlines at init time into
// summaryPromptTemplate below.
//
// The template embeds:
//   - the full schema contract for CompactionSummary,
//   - the forbidding-ids directive (phrased verbatim as the regex scrubber
//     downstream expects),
//   - the {{.MessageCount}} and {{.Messages}} interpolations supplied by the
//     summaryPromptData struct.
//
// The template is resilient to message payloads containing template
// metacharacters: callers pass pre-rendered text through .Messages, and
// Go's text/template escapes actions, not payloads.
var summaryPromptLines = []string{
	"You are summarising a slice of an AI coding agent's transcript so that the",
	"original messages can be dropped from the live context window. Produce a single",
	"JSON object matching the CompactionSummary schema described below.",
	"",
	"## Output contract",
	"",
	"Output exactly one JSON object. No preamble. No trailing commentary.",
	"No markdown code fences. No explanation of your reasoning.",
	"If you cannot produce valid JSON you must still output a JSON object with empty",
	"arrays and an `intent` field describing the failure.",
	"",
	"## Forbidden content",
	"",
	"Do NOT include any tool_use_id or tool_call_id values (strings starting with",
	"`toolu_` or `call_`) anywhere in the output.",
	"Refer to tool calls by name and purpose only.",
	"Identifiers from the raw transcript carry no semantic value once the messages",
	"are compacted and will cause downstream pairing errors if re-emitted.",
	"",
	"Do NOT quote long message bodies verbatim. Summarise intent and outcome.",
	"",
	"## Schema",
	"",
	"The CompactionSummary object has these fields:",
	"",
	"- `intent` (string): one or two sentences describing what the agent was trying",
	"  to accomplish across this slice of the transcript.",
	"- `key_decisions` (array of strings): architectural or design decisions made,",
	"  each phrased as a standalone statement. Empty array if no decisions were",
	"  made.",
	"- `errors` (array of strings): errors encountered and how they were resolved",
	"  (or left open). Empty array if none.",
	"- `next_steps` (array of strings): outstanding work implied by the transcript.",
	"  Empty array if the slice concludes cleanly.",
	"- `files_to_restore` (array of strings): relative paths of files that the",
	"  agent read or modified which a future turn is likely to need re-loaded.",
	"  Use forward slashes. Relative to the repository root. Empty array if no",
	"  files were touched.",
	"- `original_token_count` (integer): leave as 0 — the caller will overwrite",
	"  this field.",
	"- `summary_token_count` (integer): leave as 0 — the caller will overwrite",
	"  this field.",
	"",
	"Emit ONLY the fields listed above. Any other field will be rejected as a",
	"parse failure. Compaction wall-clock time is stamped server-side and must",
	"not be included in your output.",
	"",
	"## File path guidance",
	"",
	"When listing `files_to_restore`, use relative paths from the repository root",
	"(for example `internal/context/window_builder.go`). Do not include line",
	"numbers. Do not include absolute paths. If a file was only mentioned (not",
	"read or written), omit it.",
	"",
	"## Transcript to summarise",
	"",
	"The following is the canonical text block covering {{.MessageCount}} message(s).",
	"Read it and produce the CompactionSummary JSON object described above.",
	"",
	"---",
	"{{.Messages}}",
	"---",
	"",
	"Produce only the JSON object.",
}

// summaryPromptTemplate is the joined template text rendered by
// RenderSummaryPrompt. Stored as a package-level value (not a const) because
// it is assembled from summaryPromptLines at init time.
var summaryPromptTemplate = strings.Join(summaryPromptLines, "\n")

// summaryPromptData is the template data structure rendered into
// summaryPromptTemplate by RenderSummaryPrompt. Keep field names stable —
// changing them requires a matching edit to the template text.
type summaryPromptData struct {
	MessageCount int
	Messages     string
}

// parsedSummaryPrompt is the pre-parsed template used by RenderSummaryPrompt.
// Parsing at package init time removes an uncovered error path from the
// call site (a constant template cannot fail to parse in production) and
// avoids re-parsing on every compaction firing.
var parsedSummaryPrompt = template.Must(template.New("summary_prompt").Parse(summaryPromptTemplate))

// RenderSummaryPrompt produces the user-facing prompt text for the L2
// auto-compactor. The returned string is intended to be sent as the user
// message accompanying SummaryPromptSystem in a chat request.
//
// Expected:
//   - msgs is the slice of provider.Message values to be summarised,
//     typically the "cold" half of the transcript as partitioned by the
//     HotColdSplitter. Messages are rendered verbatim into the prompt; no
//     scrubbing of tool-call ids happens here — the prompt instructs the
//     model not to echo them, and a downstream regex scrubber enforces the
//     contract defensively.
//
// Returns:
//   - The rendered prompt text on success.
//   - ErrEmptySummaryInput when msgs is empty; callers should avoid firing
//     the L2 compactor in that state rather than treat the error as fatal.
//   - A wrapped template execution error if the template itself fails to
//     render, which only happens on programmer error in this package.
//
// Side effects:
//   - None. Pure function over msgs. Does not persist anything, does not
//     call any LLM, and does not mutate the input slice.
func RenderSummaryPrompt(msgs []provider.Message) (string, error) {
	if len(msgs) == 0 {
		return "", ErrEmptySummaryInput
	}

	data := summaryPromptData{
		MessageCount: len(msgs),
		Messages:     renderMessagesForSummary(msgs),
	}

	var buf bytes.Buffer
	if err := parsedSummaryPrompt.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("context: execute summary prompt template: %w", err)
	}

	return buf.String(), nil
}

// renderMessagesForSummary turns a slice of provider.Message into a single
// canonical text block suitable for embedding in a summariser prompt. The
// format is deliberately simple and stable — each message is rendered as
// a role header followed by its content, with tool calls described by name
// only (never by id).
//
// Expected:
//   - msgs is non-empty. Callers guard the empty case before invoking.
//
// Returns:
//   - A newline-separated string of message blocks suitable for template
//     interpolation. The returned text never contains identifier strings
//     sourced from ToolCall.ID — those are dropped by design.
//
// Side effects:
//   - None.
func renderMessagesForSummary(msgs []provider.Message) string {
	var b strings.Builder

	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}

		role := m.Role
		if role == "" {
			role = "unknown"
		}
		fmt.Fprintf(&b, "[%d] %s:\n", i, role)

		if m.Thinking != "" {
			fmt.Fprintf(&b, "  (thinking) %s\n", m.Thinking)
		}

		if m.Content != "" {
			b.WriteString("  ")
			b.WriteString(m.Content)
		}

		if len(m.ToolCalls) > 0 {
			if m.Content != "" {
				b.WriteString("\n")
			}
			for _, tc := range m.ToolCalls {
				// Deliberately omit tc.ID — the prompt forbids emitting
				// identifier strings and the summariser never needs them.
				fmt.Fprintf(&b, "  (tool call) %s\n", tc.Name)
			}
		}
	}

	return b.String()
}
