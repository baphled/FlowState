package tooldisplay

import "fmt"

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

// bashTruncateLen is the maximum length for bash command display before truncation.
const bashTruncateLen = 80

// Summary formats a tool call as "name: primaryArg" for display purposes.
//
// Expected:
//   - name is a tool identifier.
//   - args contains the tool call argument map (may be nil).
//
// Returns:
//   - A string in the form "name: value" when a primary argument is present and non-empty.
//   - Just the tool name when no primary argument is found.
//   - For bash commands exceeding 80 characters, the command is truncated with "...".
//
// Side effects:
//   - None.
func Summary(name string, args map[string]any) string {
	key := PrimaryArgKey(name)
	if key == "" {
		return name
	}

	arg, ok := args[key].(string)
	if !ok || arg == "" {
		return name
	}

	if name == "bash" && len(arg) > bashTruncateLen {
		arg = arg[:bashTruncateLen] + "..."
	}

	return fmt.Sprintf("%s: %s", name, arg)
}
