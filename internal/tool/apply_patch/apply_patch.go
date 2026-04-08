package applypatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool applies unified diffs to files.
type Tool struct{}

// New creates a new apply_patch tool instance.
//
// Returns:
//   - A Tool configured to apply unified diffs.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "apply_patch".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "apply_patch"
}

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Apply unified diffs to files"
}

// Schema returns the input schema for the tool.
//
// Returns:
//   - A schema describing the patch argument.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"patch": {
				Type:        "string",
				Description: "Inline patch text or a path to a patch file",
			},
		},
		Required: []string{"patch"},
	}
}

// Execute applies a patch.
//
// Expected:
//   - input contains a non-empty patch argument or a readable patch file path.
//
// Returns:
//   - A tool.Result with update output or an error.
//
// Side effects:
//   - Reads and writes files in the current working directory.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	patchValue, ok := input.Arguments["patch"].(string)
	if !ok || strings.TrimSpace(patchValue) == "" {
		return tool.Result{}, errors.New("patch argument is required")
	}

	patchText, err := loadPatchText(patchValue)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	result, err := applyPatchText(patchText)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	return tool.Result{Output: result}, nil
}

// loadPatchText resolves patch text from an inline value or file path.
//
// Expected:
//   - value may be an inline patch or a path to a patch file.
//
// Returns:
//   - The patch text to apply or an error.
//
// Side effects:
//   - May read a patch file from disk.
func loadPatchText(value string) (string, error) {
	if info, err := os.Stat(value); err == nil && !info.IsDir() {
		if !filepath.IsLocal(value) {
			return "", errors.New("path traversal not allowed")
		}
		root, err := os.OpenRoot(".")
		if err != nil {
			return "", fmt.Errorf("open root failed: %w", err)
		}
		defer root.Close()
		content, readErr := root.ReadFile(value)
		if readErr != nil {
			return "", fmt.Errorf("read patch failed: %w", readErr)
		}
		return string(content), nil
	}
	return value, nil
}

// applyPatchText applies a unified diff to files in the current directory.
//
// Expected:
//   - patchText contains a valid unified diff.
//
// Returns:
//   - A summary of updated files or an error.
//
// Side effects:
//   - Reads and writes files in the current directory.
func applyPatchText(patchText string) (string, error) {
	root, err := os.OpenRoot(".")
	if err != nil {
		return "", fmt.Errorf("open root failed: %w", err)
	}
	defer root.Close()

	lines := strings.Split(patchText, "\n")
	index := 0
	var output strings.Builder

	for index < len(lines) {
		line := lines[index]
		index++
		if strings.TrimSpace(line) == "" || line == "*** Begin Patch" {
			continue
		}
		if line == "*** End Patch" {
			break
		}
		if !strings.HasPrefix(line, "*** Update File: ") {
			return "", fmt.Errorf("invalid patch header: %s", line)
		}

		path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
		rawPath := strings.TrimSpace(path)
		if !filepath.IsLocal(rawPath) {
			return "", errors.New("path traversal not allowed")
		}
		content, err := root.ReadFile(rawPath)
		if err != nil {
			return "", fmt.Errorf("read target failed: %w", err)
		}

		updated, nextIndex, err := applyFileHunks(content, lines, index)
		if err != nil {
			return "", err
		}
		index = nextIndex
		if err := root.WriteFile(rawPath, updated, 0o600); err != nil {
			return "", fmt.Errorf("write target failed: %w", err)
		}
		fmt.Fprintf(&output, "updated %s\n", rawPath)
	}

	return strings.TrimSpace(output.String()), nil
}

// applyFileHunks applies all hunks for one file from the patch stream.
//
// Expected:
//   - lines contains a patch stream and start points to the first hunk.
//
// Returns:
//   - The updated file content, next index, or an error.
//
// Side effects:
//   - None.
func applyFileHunks(original []byte, lines []string, start int) ([]byte, int, error) {
	current := string(original)
	index := start
	for index < len(lines) {
		line := lines[index]
		if isPatchBoundary(line) {
			return []byte(current), index, nil
		}
		if strings.TrimSpace(line) == "" {
			index++
			continue
		}
		if !strings.HasPrefix(line, "@@") {
			return nil, 0, fmt.Errorf("invalid hunk header: %s", line)
		}

		index++
		hunk, nextIndex := collectHunk(lines, index)
		index = nextIndex

		updated, err := applyHunk(current, hunk.old, hunk.new)
		if err != nil {
			return nil, 0, err
		}
		current = updated
	}

	return []byte(current), index, nil
}

// isPatchBoundary reports whether a line starts the next patch section.
//
// Expected:
//   - line is one line from a patch stream.
//
// Returns:
//   - True when the line starts a new patch section.
//
// Side effects:
//   - None.
func isPatchBoundary(line string) bool {
	return line == "*** End Patch" || strings.HasPrefix(line, "*** Update File: ")
}

// hunk stores the old and new lines for one patch hunk.
type hunk struct {
	old []string
	new []string
}

// collectHunk gathers hunk lines until the next boundary.
//
// Expected:
//   - lines contains a patch stream and start points to the first hunk line.
//
// Returns:
//   - The parsed hunk and the next index.
//
// Side effects:
//   - None.
func collectHunk(lines []string, start int) (hunk, int) {
	var result hunk
	index := start
	for index < len(lines) {
		chunkLine := lines[index]
		if isPatchBoundary(chunkLine) || strings.HasPrefix(chunkLine, "@@") {
			return result, index
		}
		if err := appendChunkLine(chunkLine, &result.old, &result.new); err != nil {
			return hunk{}, index
		}
		index++
	}

	return result, index
}

// appendChunkLine appends a unified diff line to the hunk buffers.
//
// Expected:
//   - chunkLine is one line from a unified diff hunk.
//
// Returns:
//   - An error when the line is not a valid hunk entry.
//
// Side effects:
//   - Appends to the supplied slices.
func appendChunkLine(chunkLine string, oldChunk, newChunk *[]string) error {
	if chunkLine == "" {
		*oldChunk = append(*oldChunk, "")
		*newChunk = append(*newChunk, "")
		return nil
	}

	switch chunkLine[0] {
	case ' ':
		text := chunkLine[1:]
		*oldChunk = append(*oldChunk, text)
		*newChunk = append(*newChunk, text)
	case '-':
		*oldChunk = append(*oldChunk, chunkLine[1:])
	case '+':
		*newChunk = append(*newChunk, chunkLine[1:])
	default:
		return fmt.Errorf("invalid hunk line: %s", chunkLine)
	}

	return nil
}

// applyHunk replaces the matched text for one hunk.
//
// Expected:
//   - current contains the original file content and the hunk matches it.
//
// Returns:
//   - The updated content or an error.
//
// Side effects:
//   - None.
func applyHunk(current string, oldChunk, newChunk []string) (string, error) {
	oldText := strings.Join(oldChunk, "\n")
	newText := strings.Join(newChunk, "\n")
	if oldText != "" && !strings.Contains(current, oldText) {
		return "", fmt.Errorf("conflict applying patch: expected %q", oldText)
	}
	if oldText == "" {
		return newText, nil
	}

	return strings.Replace(current, oldText, newText, 1), nil
}
