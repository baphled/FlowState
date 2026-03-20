package prompt

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed prompts/*.md
var promptFS embed.FS

// GetPrompt returns the embedded system prompt content for the given agent ID.
//
// Expected:
//   - agentID is a non-empty string matching a file in prompts/{agentID}.md.
//
// Returns:
//   - The full prompt content as a string, or an error if the file does not exist.
//
// Side effects:
//   - None.
func GetPrompt(agentID string) (string, error) {
	data, err := promptFS.ReadFile("prompts/" + agentID + ".md")
	if err != nil {
		return "", fmt.Errorf("prompt not found: %s", agentID)
	}
	return string(data), nil
}

// HasPrompt reports whether an embedded prompt exists for the given agent ID.
//
// Expected:
//   - agentID is a non-empty string.
//
// Returns:
//   - true if prompts/{agentID}.md is present in the embedded filesystem.
//
// Side effects:
//   - None.
func HasPrompt(agentID string) bool {
	_, err := promptFS.ReadFile("prompts/" + agentID + ".md")
	return err == nil
}

// ListPrompts returns the agent IDs of all embedded prompt files.
//
// Returns:
//   - A slice of agent IDs derived from filenames in the prompts/ directory (without .md extension).
//
// Side effects:
//   - None.
func ListPrompts() []string {
	entries, err := promptFS.ReadDir("prompts")
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return ids
}
