package glob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/tool"
)

const maxMatches = 100
const toolTimeout = 60 * time.Second

// Tool implements file pattern matching with safety limits.
type Tool struct{}

// New creates a new glob tool instance.
//
// Returns:
//   - A Tool configured for glob matching.
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
//   - The string "glob".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string { return "glob" }

// Description returns a human-readable description of the glob tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string { return "Match files using glob patterns" }

// Schema returns the input schema for the glob tool.
//
// Returns:
//   - A schema describing pattern and path arguments.
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
			"pattern": {Type: "string", Description: "Glob pattern to match"},
			"path":    {Type: "string", Description: "Base directory to search"},
		},
		Required: []string{"pattern"},
	}
}

// Execute performs the glob operation.
//
// Expected:
//   - input contains a non-empty pattern and an optional path.
//
// Returns:
//   - A tool.Result containing matching paths or an error.
//
// Side effects:
//   - Reads the filesystem and enforces a timeout.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	pattern, ok := input.Arguments["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return tool.Result{}, errors.New("pattern argument is required")
	}

	basePath := "."
	if rawPath, ok := input.Arguments["path"].(string); ok && strings.TrimSpace(rawPath) != "" {
		basePath = rawPath
	}

	cleanBase := filepath.Clean(basePath)
	info, err := os.Stat(cleanBase)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("base path error: %w", err)}, nil
	}
	if !info.IsDir() {
		return tool.Result{Error: fmt.Errorf("base path %q is not a directory", cleanBase)}, nil
	}

	matches, err := findMatches(ctx, cleanBase, pattern)
	if err != nil {
		return tool.Result{Error: err}, nil
	}
	if len(matches) > maxMatches {
		return tool.Result{Error: fmt.Errorf("too many matches: %d (limit %d)", len(matches), maxMatches)}, nil
	}

	return tool.Result{Output: strings.Join(matches, "\n")}, nil
}

// findMatches walks the base path and collects matching files.
//
// Expected:
//   - basePath exists and pattern is a valid glob pattern.
//
// Returns:
//   - Matching file paths or an error.
//
// Side effects:
//   - Reads the filesystem.
func findMatches(ctx context.Context, basePath, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	var matches []string
	if err := filepath.WalkDir(basePath, func(path string, d os.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}
		if matchPattern(pattern, filepath.ToSlash(rel)) {
			matches = append(matches, path)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}

	sort.Strings(matches)
	return matches, nil
}

// matchPattern matches a slash-separated glob pattern against a candidate path.
//
// Expected:
//   - pattern and candidate are slash-separated paths.
//
// Returns:
//   - True when the candidate matches the pattern.
//
// Side effects:
//   - None.
func matchPattern(pattern, candidate string) bool {
	patternParts := strings.Split(pattern, "/")
	candidateParts := strings.Split(candidate, "/")
	return matchParts(patternParts, candidateParts)
}

// matchParts recursively matches path segments.
//
// Expected:
//   - patternParts and candidateParts contain split path segments.
//
// Returns:
//   - True when all segments match.
//
// Side effects:
//   - None.
func matchParts(patternParts, candidateParts []string) bool {
	if len(patternParts) == 0 {
		return len(candidateParts) == 0
	}
	if patternParts[0] == "**" {
		if matchParts(patternParts[1:], candidateParts) {
			return true
		}
		if len(candidateParts) > 0 {
			return matchParts(patternParts, candidateParts[1:])
		}
		return false
	}
	if len(candidateParts) == 0 {
		return false
	}
	matched, err := filepath.Match(patternParts[0], candidateParts[0])
	if err != nil || !matched {
		return false
	}
	return matchParts(patternParts[1:], candidateParts[1:])
}
