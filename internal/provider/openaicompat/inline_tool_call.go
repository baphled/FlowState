package openaicompat

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/provider"
)

// inline-XML tool-call recovery
//
// Some OpenAI-compat reasoning models (observed: glm-4.5, glm-4.6 via zai)
// occasionally emit a tool call as a literal `<tool_call>...</tool_call>`
// block inside the reasoning_content channel rather than populating the
// structured `tool_calls` array. The model then stops, expecting a tool
// result; the runtime, having parsed nothing, surfaces the soft-error
// affordance "The model worked through this turn but stopped before
// replying. Try sending the prompt again." That affordance is treating
// a symptom — the underlying defect is that we drop a tool call the
// model actually emitted.
//
// Reproducer: session 718b5d51-f01b-45f0-80bb-31329a9d44e7 message 9.
//
// inlineToolCallExtractor scans accumulated reasoning text for closed
// <tool_call>...</tool_call> blocks, parses them into provider.ToolCall
// values, and returns the reasoning text with the markup stripped so the
// downstream Thinking channel does not double-render with the executed
// tool call.
//
// Parser shape:
//   - Tag-literal scanning (state machine), not regex or encoding/xml:
//     values may contain unescaped `<`, `>`, JSON, and other XML-hostile
//     bytes. encoding/xml is too strict; regex over multi-pair bodies is
//     fragile. The state machine matches ONLY a fully-closed tool_call
//     block — malformed or unclosed markup stays on the soft-error path.
//   - Body shape: the first non-whitespace token after the opening
//     `<tool_call>` is the tool name (real-world variant: bare
//     `<tool_call>bash\n...`). Inside the body, paired
//     `<arg_key>X</arg_key><arg_value>Y</arg_value>` forms map to args.
//   - Cross-chunk: scanning is incremental — callers feed every fragment
//     of reasoning text into Feed() and consume any closed blocks via
//     Pending(). The buffer holds an unclosed block until the closing
//     tag arrives across a later chunk.
//
// Out of scope (per the May 2026 brief):
//   - The `<tool_call name="X">` attribute form (no real-world evidence
//     in any persisted session as of May 7 2026; only `<tool_call>` bare
//     form is observed).
//   - Truly malformed markup (unbalanced tags, etc.) — that case stays on
//     the existing placeholder/affordance path so the user sees an
//     actionable failure rather than executing garbage args.

const (
	inlineToolCallOpen     = "<tool_call>"
	inlineToolCallClose    = "</tool_call>"
	inlineArgKeyOpen       = "<arg_key>"
	inlineArgKeyClose      = "</arg_key>"
	inlineArgValueOpen     = "<arg_value>"
	inlineArgValueClose    = "</arg_value>"
	inlineToolCallIDPrefix = "call_inline_"
)

// inlineToolCallExtractor incrementally scans reasoning text for closed
// `<tool_call>...</tool_call>` blocks. Callers must feed every fragment
// (including across stream chunks) and read the safe-to-emit Thinking
// text + any newly-closed tool calls from each Feed() call.
//
// Lifecycle:
//   - Zero value is ready to use.
//   - Feed(fragment) appends to the internal buffer, then drains every
//     closed <tool_call> block AND every byte of text that cannot be the
//     prefix of a future <tool_call> open tag.
//   - Flush() returns whatever is left after the stream ends. Any
//     unclosed <tool_call> remains in the buffer and is returned as
//     plain Thinking text (preserving the soft-error path for genuinely
//     broken markup).
type inlineToolCallExtractor struct {
	// buffer holds reasoning text that has been received but not yet
	// emitted as Thinking. Bytes leave the buffer either as safe
	// Thinking output or as part of a parsed tool_call block.
	buffer strings.Builder
}

// FeedResult carries the byproducts of one Feed() call: the safe
// Thinking text to emit immediately, and any closed tool_call blocks
// that were assembled this round.
type FeedResult struct {
	// Thinking is the prefix of the new buffer state that is safe to
	// emit downstream — it is guaranteed not to contain any
	// `<tool_call>` markup and not to overlap with a partial opening
	// tag still pending in the buffer.
	Thinking string
	// ToolCalls are any tool calls assembled from closed
	// `<tool_call>...</tool_call>` blocks during this Feed() call.
	// Each carries a freshly generated synthetic id.
	ToolCalls []provider.ToolCall
}

// Feed appends a reasoning fragment to the buffer and returns whatever
// is now safe to emit downstream (Thinking text and/or closed tool
// calls).
func (e *inlineToolCallExtractor) Feed(fragment string) FeedResult {
	if fragment == "" {
		return FeedResult{}
	}
	e.buffer.WriteString(fragment)
	return e.drain()
}

// Flush is called once the upstream reasoning stream has ended. It
// returns whatever is left in the buffer as plain Thinking text. Any
// unclosed `<tool_call>` block at this point is preserved verbatim so
// the soft-error affordance can still surface the failure to the user.
func (e *inlineToolCallExtractor) Flush() string {
	remaining := e.buffer.String()
	e.buffer.Reset()
	return remaining
}

// drain pulls every closed tool_call block AND every byte of safe-to-
// emit Thinking text out of the buffer, returning what was extracted.
//
// The state machine has three states:
//
//  1. No `<tool_call>` open tag in buffer — emit everything except
//     bytes at the tail that could be the prefix of an opening tag.
//  2. `<tool_call>` open tag present with matching close — emit text
//     before the open, parse the block, drop the markup, repeat.
//  3. `<tool_call>` open without matching close — emit text before the
//     open, hold the rest pending more fragments.
func (e *inlineToolCallExtractor) drain() FeedResult {
	var out FeedResult
	for {
		buf := e.buffer.String()
		openIdx := strings.Index(buf, inlineToolCallOpen)
		if openIdx == -1 {
			// State 1: nothing to recover. Emit everything except
			// any tail that could grow into the opening tag.
			emit, hold := splitOnPartialOpenSuffix(buf)
			out.Thinking += emit
			e.buffer.Reset()
			e.buffer.WriteString(hold)
			return out
		}

		// Emit any text before the opening tag — that text cannot
		// be part of the recovered call.
		if openIdx > 0 {
			out.Thinking += buf[:openIdx]
		}

		closeIdx := strings.Index(buf[openIdx:], inlineToolCallClose)
		if closeIdx == -1 {
			// State 3: open without close. Hold from the open tag
			// onward; emit nothing more this round. Subsequent
			// Feed() calls may complete the block.
			e.buffer.Reset()
			e.buffer.WriteString(buf[openIdx:])
			return out
		}

		// State 2: closed block. Parse it, drop the markup, loop.
		bodyStart := openIdx + len(inlineToolCallOpen)
		bodyEnd := openIdx + closeIdx
		body := buf[bodyStart:bodyEnd]
		if tc, ok := parseInlineToolCallBody(body); ok {
			out.ToolCalls = append(out.ToolCalls, tc)
		}
		// Reset the buffer to whatever follows the closing tag and
		// continue the loop in case more closed blocks are present.
		rest := buf[bodyEnd+len(inlineToolCallClose):]
		e.buffer.Reset()
		e.buffer.WriteString(rest)
	}
}

// splitOnPartialOpenSuffix returns (safeToEmit, hold) where safeToEmit
// is the buffer prefix guaranteed not to be the start of a
// `<tool_call>` opening tag, and hold is whatever tail must remain in
// the buffer because it could be the prefix of one. This is what
// keeps Thinking streaming in real time while still buffering
// potential markup.
func splitOnPartialOpenSuffix(buf string) (emit string, hold string) {
	// Walk back from the end of the buffer looking for the longest
	// suffix that is also a prefix of `<tool_call>`.
	maxLen := len(inlineToolCallOpen) - 1
	if maxLen > len(buf) {
		maxLen = len(buf)
	}
	for n := maxLen; n > 0; n-- {
		tail := buf[len(buf)-n:]
		if strings.HasPrefix(inlineToolCallOpen, tail) {
			return buf[:len(buf)-n], tail
		}
	}
	return buf, ""
}

// parseInlineToolCallBody parses the body of a `<tool_call>...</tool_call>`
// block into a provider.ToolCall.
//
// Expected body shape (real-world, observed in session
// 718b5d51-f01b-45f0-80bb-31329a9d44e7):
//
//	bash
//	<arg_key>command</arg_key>
//	<arg_value>find /vault -name "*.md"</arg_value>
//
// or with multiple arg pairs:
//
//	delegate
//	<arg_key>subagent_type</arg_key>
//	<arg_value>explorer</arg_value>
//	<arg_key>message</arg_key>
//	<arg_value>Search the vault</arg_value>
//
// The first non-whitespace token before the first `<arg_key>` (or the
// whole body, if there is no `<arg_key>`) is the tool name.
//
// Returns false when the body does not yield a usable tool name — the
// soft-error path takes over for that case.
func parseInlineToolCallBody(body string) (provider.ToolCall, bool) {
	name, argsRegion := splitNameAndArgs(body)
	if name == "" {
		return provider.ToolCall{}, false
	}
	args := parseInlineArgs(argsRegion)
	return provider.ToolCall{
		ID:        newInlineToolCallID(),
		Name:      name,
		Arguments: args,
	}, true
}

// splitNameAndArgs splits a tool_call body into the leading tool name
// and the trailing region that contains any `<arg_key>/<arg_value>`
// pairs.
func splitNameAndArgs(body string) (name string, argsRegion string) {
	argsStart := strings.Index(body, inlineArgKeyOpen)
	var nameRegion string
	if argsStart == -1 {
		nameRegion = body
		argsRegion = ""
	} else {
		nameRegion = body[:argsStart]
		argsRegion = body[argsStart:]
	}
	return strings.TrimSpace(nameRegion), argsRegion
}

// parseInlineArgs walks the args region and pulls out every
// `<arg_key>K</arg_key><arg_value>V</arg_value>` pair, in order. Pairs
// without a closing tag for either half are skipped — better to drop
// a single malformed arg than to emit a half-formed tool call.
func parseInlineArgs(region string) map[string]any {
	args := map[string]any{}
	for {
		keyOpen := strings.Index(region, inlineArgKeyOpen)
		if keyOpen == -1 {
			return args
		}
		keyClose := strings.Index(region[keyOpen:], inlineArgKeyClose)
		if keyClose == -1 {
			return args
		}
		keyStart := keyOpen + len(inlineArgKeyOpen)
		keyEnd := keyOpen + keyClose
		key := strings.TrimSpace(region[keyStart:keyEnd])

		// Look for the matching <arg_value>...</arg_value> AFTER the
		// </arg_key>. Any text between them (whitespace, newlines) is
		// ignored — that's the real-world shape.
		afterKey := keyEnd + len(inlineArgKeyClose)
		valOpen := strings.Index(region[afterKey:], inlineArgValueOpen)
		if valOpen == -1 {
			return args
		}
		valClose := strings.Index(region[afterKey+valOpen:], inlineArgValueClose)
		if valClose == -1 {
			return args
		}
		valStart := afterKey + valOpen + len(inlineArgValueOpen)
		valEnd := afterKey + valOpen + valClose
		value := region[valStart:valEnd]

		if key != "" {
			args[key] = value
		}
		// Continue past the closing </arg_value> for the next pair.
		region = region[valEnd+len(inlineArgValueClose):]
	}
}

// newInlineToolCallID returns a synthetic, stream-unique tool-call id
// for a recovered call. The `call_inline_` prefix mirrors OpenAI's
// `call_*` id shape so downstream id-translation paths
// (shared.TranslateToolCallID) treat it identically.
func newInlineToolCallID() string {
	return fmt.Sprintf("%s%s", inlineToolCallIDPrefix, uuid.NewString())
}
