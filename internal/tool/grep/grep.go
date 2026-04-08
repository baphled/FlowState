package grep

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

const defaultMode = "content"

// Tool implements regex content search.
type Tool struct{}

// New creates a new grep tool instance.
//
// Returns:
//   - A Tool configured for regex search.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func New() *Tool { return &Tool{} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "grep".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string { return "grep" }

// Description returns a human-readable description of the grep tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string { return "Search file contents using regular expressions" }

// Schema returns the input schema for the grep tool.
//
// Returns:
//   - A schema describing pattern, path, include, exclude, mode, and context.
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
			"pattern": {Type: "string", Description: "Regular expression to search for"},
			"path":    {Type: "string", Description: "Base directory to search"},
			"include": {Type: "string", Description: "Optional glob filter for files"},
			"exclude": {Type: "string", Description: "Optional glob pattern to skip files"},
			"mode": {
				Type:        "string",
				Description: "Output mode: content, files_with_matches, or count",
				Enum:        []string{"content", "files_with_matches", "count"},
			},
			"context": {Type: "string", Description: "Number of surrounding lines to include"},
		},
		Required: []string{"pattern", "path"},
	}
}

// Execute performs the grep operation.
//
// Expected:
//   - input contains pattern and path arguments.
//
// Returns:
//   - A tool.Result containing formatted matches or an error.
//
// Side effects:
//   - Reads files from the filesystem.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	pattern, ok := input.Arguments["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return tool.Result{}, errors.New("pattern argument is required")
	}

	basePath, ok := input.Arguments["path"].(string)
	if !ok || strings.TrimSpace(basePath) == "" {
		return tool.Result{}, errors.New("path argument is required")
	}

	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("invalid pattern: %w", err)}, err
	}

	include := optionalString(input.Arguments, "include")
	exclude := optionalString(input.Arguments, "exclude")
	mode := defaultMode
	if rawMode, ok := input.Arguments["mode"].(string); ok && strings.TrimSpace(rawMode) != "" {
		mode = rawMode
	}
	contextLines := 0
	if rawContext, ok := input.Arguments["context"].(string); ok && strings.TrimSpace(rawContext) != "" {
		parsed, parseErr := strconv.Atoi(rawContext)
		if parseErr != nil || parsed < 0 {
			contextErr := fmt.Errorf("invalid context: %q", rawContext)
			return tool.Result{Error: contextErr}, contextErr
		}
		contextLines = parsed
	}

	cleanBase := filepath.Clean(basePath)
	info, err := os.Stat(cleanBase)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("base path error: %w", err)}, err
	}
	if !info.IsDir() {
		pathErr := fmt.Errorf("base path %q is not a directory", cleanBase)
		return tool.Result{Error: pathErr}, pathErr
	}

	results, err := searchFiles(ctx, searchOptions{
		basePath:     cleanBase,
		include:      include,
		exclude:      exclude,
		mode:         mode,
		contextLines: contextLines,
		compiled:     compiled,
	})
	if err != nil {
		return tool.Result{Error: err}, err
	}

	return tool.Result{Output: strings.Join(results, "\n")}, nil
}

// matchResult stores search output for one file.
type matchResult struct {
	path  string
	lines []string
	count int
}

// searchOptions configures grep traversal and formatting.
type searchOptions struct {
	basePath     string
	include      string
	exclude      string
	mode         string
	contextLines int
	compiled     *regexp.Regexp
}

// searchWalker collects matching files during traversal.
type searchWalker struct {
	ctx   context.Context
	opts  searchOptions
	files []matchResult
}

// searchFiles walks the filesystem and formats the search results.
//
// Expected:
//   - opts is fully populated with valid search settings.
//
// Returns:
//   - Formatted search output lines or an error.
//
// Side effects:
//   - Reads files under the configured base path.
func searchFiles(ctx context.Context, opts searchOptions) ([]string, error) {
	walker := searchWalker{ctx: ctx, opts: opts}
	err := filepath.WalkDir(opts.basePath, walker.visit)
	if err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}

	sort.Slice(walker.files, func(i, j int) bool { return walker.files[i].path < walker.files[j].path })

	output := make([]string, 0)
	for _, match := range walker.files {
		switch opts.mode {
		case "files_with_matches":
			output = append(output, match.path)
		case "count":
			output = append(output, fmt.Sprintf("%s:%d", match.path, match.count))
		default:
			output = append(output, match.lines...)
		}
	}
	return output, nil
}

// visit inspects one path during a walk.
//
// Expected:
//   - path, d, and walkErr are provided by filepath.WalkDir.
//
// Returns:
//   - An error to stop the walk or nil to continue.
//
// Side effects:
//   - Appends matches to the walker state.
func (w *searchWalker) visit(path string, d fs.DirEntry, walkErr error) error {
	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	default:
	}

	if walkErr != nil {
		return walkErr
	}
	if d.IsDir() {
		return nil
	}

	rel, err := filepath.Rel(w.opts.basePath, path)
	if err != nil {
		return err
	}
	relSlash := filepath.ToSlash(rel)
	if w.opts.include != "" && !matchesGlob(w.opts.include, relSlash) {
		return nil
	}
	if w.opts.exclude != "" && matchesGlob(w.opts.exclude, relSlash) {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	fileMatches := searchContent(filepath.Base(path), string(data), w.opts.compiled, w.opts.contextLines)
	if len(fileMatches.lines) == 0 {
		return nil
	}
	fileMatches.path = path
	w.files = append(w.files, fileMatches)
	return nil
}

// searchContent scans file content for matching lines.
//
// Expected:
//   - compiled is a valid regular expression.
//
// Returns:
//   - The matching lines and match count for one file.
//
// Side effects:
//   - None.
func searchContent(label, content string, compiled *regexp.Regexp, contextLines int) matchResult {
	lines := strings.Split(content, "\n")
	seen := make(map[int]struct{})
	result := matchResult{}
	for idx, line := range lines {
		if !compiled.MatchString(line) {
			continue
		}
		result.count++
		if contextLines == 0 {
			result.lines = append(result.lines, fmt.Sprintf("%s:%d:%s", label, idx+1, line))
			continue
		}
		start := max(0, idx-contextLines)
		end := min(len(lines)-1, idx+contextLines)
		for i := start; i <= end; i++ {
			if _, ok := seen[i]; ok {
				continue
			}
			seen[i] = struct{}{}
			result.lines = append(result.lines, fmt.Sprintf("%s:%d:%s", label, i+1, lines[i]))
		}
	}
	return result
}

// matchesGlob reports whether a candidate matches a glob pattern.
//
// Expected:
//   - pattern and candidate are slash-separated path strings.
//
// Returns:
//   - True when the candidate matches the pattern.
//
// Side effects:
//   - None.
func matchesGlob(pattern, candidate string) bool {
	matched, err := filepath.Match(pattern, candidate)
	if err == nil && matched {
		return true
	}
	if strings.Contains(pattern, "/") {
		matched, err = filepath.Match(pattern, candidate)
		return err == nil && matched
	}
	return matchedBasename(pattern, candidate)
}

// matchedBasename compares a pattern against each path segment.
//
// Expected:
//   - pattern and candidate are slash-separated path strings.
//
// Returns:
//   - True when any segment matches the pattern.
//
// Side effects:
//   - None.
func matchedBasename(pattern, candidate string) bool {
	for part := range strings.SplitSeq(candidate, "/") {
		matched, err := filepath.Match(pattern, part)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// optionalString returns a string argument when present.
//
// Expected:
//   - args may contain key as a string value.
//
// Returns:
//   - The string value or an empty string.
//
// Side effects:
//   - None.
func optionalString(args map[string]any, key string) string {
	value, ok := args[key].(string)
	if !ok {
		return ""
	}
	return value
}
