package voice

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// narrationMaxLen is the hard ceiling on any spoken narration
// segment. Narrations exist so listeners hear intent, not payloads;
// anything longer than this is truncated with an ellipsis.
const narrationMaxLen = 160

// NarrateToolCall produces a short spoken description of a tool
// call: what the agent is doing and, for file tools, to which path.
// Raw commands, JSON bodies and multi-line payloads are never
// spoken verbatim; bash commands are classified by their leading
// verb and unknown tools fall back to a generic phrase.
//
// Expected:
//   - name is the tool identifier from the tool_call event.
//   - input is the tool call's input as serialised JSON; empty or
//     malformed input is tolerated.
//
// Returns:
//   - One to two sentences of speakable narration.
//
// Side effects:
//   - None.
func NarrateToolCall(name string, input string) string {
	args := decodeNarrationArgs(input)
	switch name {
	case "bash":
		return narrateBash(stringArg(args, "command"))
	case "read":
		return narrateFile("Reading", stringArg(args, "filePath"))
	case "write":
		return narrateFile("Writing", stringArg(args, "filePath"))
	case "edit":
		return narrateFile("Editing", stringArg(args, "filePath"))
	case "multiedit":
		return narrateFile("Editing", stringArg(args, "filePath"))
	case "apply_patch":
		return narrateFile("Patching", stringArg(args, "filePath"))
	case "delegate":
		return narrateDelegate(stringArg(args, "subagent_type"))
	case "coordination_store":
		return narrateCoordination(stringArg(args, "operation"), stringArg(args, "key"))
	case "grep":
		return narrateSearch("Searching for", stringArg(args, "pattern"))
	case "glob":
		return narrateSearch("Looking for files matching", stringArg(args, "pattern"))
	case "ls":
		return "Listing files."
	case "web":
		return "Browsing the web."
	default:
		return truncateNarration("Using tool " + name + ".")
	}
}

// NarrateToolOutcome produces a short outcome phrase for a tool
// result or error. The result content itself is never spoken.
//
// Expected:
//   - eventKind is "tool_result" or "tool_error".
//
// Returns:
//   - "Finished." or "That failed." respectively; empty for other
//     event kinds.
//
// Side effects:
//   - None.
func NarrateToolOutcome(eventKind string) string {
	switch eventKind {
	case "tool_result":
		return "Finished."
	case "tool_error":
		return "That failed."
	default:
		return ""
	}
}

// PreprocessTTSTurn cleans a text fragment from a turn stream for
// speech, reusing PreprocessTTS so code fences and inline code are
// handled identically to full replies.
//
// Expected:
//   - text is a text delta from a turn event stream.
//
// Returns:
//   - The speech-ready fragment; empty when nothing speakable
//     remains.
//
// Side effects:
//   - None.
func PreprocessTTSTurn(text string) string {
	return PreprocessTTS(text)
}

// TurnNarrator converts a stream of turn event fragments into
// speakable narration segments. Text deltas pass through
// PreprocessTTSTurn; tool_call events become NarrateToolCall
// descriptions; tool_result and tool_error events become short
// outcome phrases — never their content.
type TurnNarrator struct {
	segments []string
}

// NewTurnNarrator returns an empty TurnNarrator ready to consume
// turn event fragments.
//
// Expected:
//   - None.
//
// Returns:
//   - A *TurnNarrator with no segments.
//
// Side effects:
//   - None.
func NewTurnNarrator() *TurnNarrator {
	return &TurnNarrator{}
}

// AddEvent feeds one turn event into the narrator. Recognised kinds
// are "text", "tool_call", "tool_result" and "tool_error"; other
// kinds are ignored.
//
// Expected:
//   - kind is the event type field from the turn event.
//   - name is the tool name (tool_call only).
//   - payload is the event's text or input field.
//
// Returns:
//   - None.
//
// Side effects:
//   - Appends a narration segment when the event is speakable.
func (n *TurnNarrator) AddEvent(kind, name, payload string) {
	switch kind {
	case "text":
		if spoken := PreprocessTTSTurn(payload); spoken != "" {
			n.segments = append(n.segments, spoken)
		}
	case "tool_call":
		n.segments = append(n.segments, NarrateToolCall(name, payload))
	case "tool_result", "tool_error":
		if phrase := NarrateToolOutcome(kind); phrase != "" {
			n.segments = append(n.segments, phrase)
		}
	}
}

// Segments returns the narration segments collected so far.
//
// Expected:
//   - None.
//
// Returns:
//   - The speakable segments in turn order.
//
// Side effects:
//   - None.
func (n *TurnNarrator) Segments() []string {
	return n.segments
}

// decodeNarrationArgs parses a tool call input string into a map,
// tolerating empty or malformed JSON.
//
// Expected:
//   - input is the serialised JSON arguments of a tool call.
//
// Returns:
//   - The decoded argument map; nil when input is not a JSON
//     object.
//
// Side effects:
//   - None.
func decodeNarrationArgs(input string) map[string]any {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return nil
	}
	return args
}

// stringArg extracts a string argument, tolerating missing keys.
//
// Expected:
//   - args may be nil; key names the argument.
//
// Returns:
//   - The argument's string value, or "".
//
// Side effects:
//   - None.
func stringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

// bashVerbClasses maps leading command tokens to spoken intents so
// a command's purpose is voiced without its payload.
var bashVerbClasses = map[string]string{
	"go":     "running Go build and tests",
	"git":    "working with git",
	"make":   "running the build",
	"npm":    "running npm",
	"grep":   "searching the codebase",
	"find":   "searching the filesystem",
	"ls":     "listing files",
	"cat":    "reading a file",
	"sed":    "editing a file",
	"docker": "running a container command",
	"cargo":  "running a Rust build",
	"pytest": "running Python tests",
	"python": "running a Python script",
	"curl":   "making an HTTP request",
	"echo":   "printing text",
}

// narrateBash classifies a shell command by its leading tokens and
// returns a spoken intent; the full command is never spoken.
//
// Expected:
//   - command is the raw shell command from a bash tool call.
//
// Returns:
//   - A one-sentence description of the command's intent.
//
// Side effects:
//   - None.
func narrateBash(command string) string {
	first := strings.Fields(command)
	if len(first) == 0 {
		return "Running a shell command."
	}
	verb := strings.TrimSuffix(first[0], ";")
	if strings.HasPrefix(verb, "./") {
		return "Running a local binary."
	}
	if class, ok := bashVerbClasses[verb]; ok {
		return truncateNarration(capitalise(class) + ".")
	}
	return "Running a shell command."
}

// narrateFile speaks a file action plus its path.
//
// Expected:
//   - action is a present participle such as "Reading".
//   - path is the file path from the tool call.
//
// Returns:
//   - A short sentence naming the action and path.
//
// Side effects:
//   - None.
func narrateFile(action, path string) string {
	if path == "" {
		return action + " a file."
	}
	return truncateNarration(action + " " + path + ".")
}

// narrateDelegate speaks the delegation target without the brief.
//
// Expected:
//   - subagent is the target agent name; may be empty.
//
// Returns:
//   - A short sentence describing the delegation.
//
// Side effects:
//   - None.
func narrateDelegate(subagent string) string {
	if subagent == "" {
		return "Delegating a task."
	}
	return truncateNarration("Delegating a task to " + subagent + ".")
}

// narrateCoordination speaks a coordination store operation.
//
// Expected:
//   - operation and key come from the coordination_store call.
//
// Returns:
//   - A short sentence describing the operation.
//
// Side effects:
//   - None.
func narrateCoordination(operation, key string) string {
	if operation == "" {
		return "Using the coordination store."
	}
	return truncateNarration("Coordination store " + operation + " for " + key + ".")
}

// narrateSearch speaks a search pattern without other arguments.
//
// Expected:
//   - lead is the spoken lead-in; pattern is the search pattern.
//
// Returns:
//   - A short sentence describing the search.
//
// Side effects:
//   - None.
func narrateSearch(lead, pattern string) string {
	if pattern == "" {
		return lead + " files."
	}
	return truncateNarration(lead + " " + pattern + ".")
}

// truncateNarration caps a narration segment at narrationMaxLen
// bytes, appending an ASCII ellipsis on overflow. It never splits a
// multi-byte rune.
//
// Expected:
//   - s is a candidate narration.
//
// Returns:
//   - The possibly truncated narration.
//
// Side effects:
//   - None.
func truncateNarration(s string) string {
	if len(s) <= narrationMaxLen {
		return s
	}
	cut := s[:narrationMaxLen-3]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "..."
}

// capitalise upper-cases the first rune of a sentence.
//
// Expected:
//   - s is a lower-case-led narration fragment.
//
// Returns:
//   - The fragment with an initial capital.
//
// Side effects:
//   - None.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
