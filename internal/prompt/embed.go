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

// Content holds a prompt with its parsed metadata.
type Content struct {
	Metadata *FrontmatterMetadata
	Body     string
}

// GetPromptWithMetadata returns the prompt content with parsed frontmatter metadata.
//
// Expected:
//   - agentID is a non-empty string matching a file in prompts/{agentID}.md.
//
// Returns:
//   - A Content struct containing parsed metadata (if present) and the prompt body.
//   - Error if the prompt file does not exist or YAML parsing fails.
//
// Side effects:
//   - None.
func GetPromptWithMetadata(agentID string) (*Content, error) {
	data, err := promptFS.ReadFile("prompts/" + agentID + ".md")
	if err != nil {
		return nil, fmt.Errorf("prompt not found: %s", agentID)
	}

	content := string(data)
	metadata, body, err := ParseFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("parsing prompt metadata: %w", err)
	}

	return &Content{
		Metadata: metadata,
		Body:     body,
	}, nil
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
