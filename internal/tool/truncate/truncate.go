package truncate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Default budgets. Match OpenCode's tool/truncation.ts.
const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 50 * 1024
)

// Direction selects which slice of an oversized payload survives.
type Direction string

const (
	// Head keeps the first N lines/bytes (default).
	Head Direction = "head"
	// Tail keeps the last N lines/bytes.
	Tail Direction = "tail"
)

// Options carries per-call overrides. Zero values fall back to defaults.
type Options struct {
	// MaxLines caps the number of newline-separated records returned to
	// the model. Zero falls back to DefaultMaxLines.
	MaxLines int
	// MaxBytes caps the byte length of the returned payload. Zero falls
	// back to DefaultMaxBytes.
	MaxBytes int
	// Direction selects head (first slice) or tail (last slice). Empty
	// falls back to Head.
	Direction Direction
	// SessionID scopes the overflow file under the session's tool-output
	// directory. Empty falls back to a global "_unscoped" bucket so
	// truncation still works in non-session contexts (CLI, tests).
	SessionID string
	// ToolName is embedded in the overflow filename for triage. Empty
	// falls back to "tool".
	ToolName string
	// Dir overrides the on-disk root for overflow files. Empty falls
	// back to UserCacheDir/flowstate/tool-output. Tests use this to
	// pin the path without polluting the real cache.
	Dir string
}

// Result is what Apply returns. When Truncated=false, Content is the
// original input verbatim and OutputPath is empty. When Truncated=true,
// Content carries the budget-fitting slice plus a recovery hint and
// OutputPath points at the spill file containing the full original.
type Result struct {
	Content    string
	Truncated  bool
	OutputPath string
}

// Apply caps text at the configured byte/line budget. Under-cap input
// passes through unchanged. Over-cap input is sliced (head or tail), a
// recovery hint is appended pointing at a session-scoped overflow file,
// and the spill file is written with the full original content.
//
// Errors writing the overflow file are swallowed: the agent always sees
// the truncated content even when spill IO fails, so the model can keep
// progressing on the visible slice. The OutputPath in the returned
// Result is empty when the spill failed; the hint omits the path in
// that case.
func Apply(text string, opts Options) Result {
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	direction := opts.Direction
	if direction == "" {
		direction = Head
	}

	totalBytes := len(text)
	lines := splitLines(text)

	if len(lines) <= maxLines && totalBytes <= maxBytes {
		return Result{Content: text, Truncated: false}
	}

	preview, hitBytes, removed, unit := slice(lines, maxLines, maxBytes, direction)

	outputPath := writeOverflow(text, opts)
	hint := buildHint(removed, unit, hitBytes, outputPath)

	var content string
	if direction == Head {
		content = preview + "\n\n" + hint
	} else {
		content = hint + "\n\n" + preview
	}

	return Result{Content: content, Truncated: true, OutputPath: outputPath}
}

// splitLines splits text on newlines without dropping the trailing
// empty line that strings.Split would lose for "a\n".
func splitLines(text string) []string {
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

// slice returns the head or tail slice that fits both the line budget
// and the byte budget, plus removal metadata for the hint.
func slice(lines []string, maxLines, maxBytes int, direction Direction) (preview string, hitBytes bool, removed int, unit string) {
	totalBytes := bytesForLines(lines)
	out := make([]string, 0, maxLines)
	bytesAccum := 0

	if direction == Head {
		for i := 0; i < len(lines) && len(out) < maxLines; i++ {
			size := len(lines[i])
			if i > 0 {
				size++ // newline separator
			}
			if bytesAccum+size > maxBytes {
				hitBytes = true
				break
			}
			out = append(out, lines[i])
			bytesAccum += size
		}
	} else {
		// Tail: walk from the end, prepend to maintain order.
		for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
			size := len(lines[i])
			if len(out) > 0 {
				size++
			}
			if bytesAccum+size > maxBytes {
				hitBytes = true
				break
			}
			out = append([]string{lines[i]}, out...)
			bytesAccum += size
		}
	}

	if hitBytes {
		removed = totalBytes - bytesAccum
		unit = "bytes"
	} else {
		removed = len(lines) - len(out)
		unit = "lines"
	}
	preview = strings.Join(out, "\n")
	return preview, hitBytes, removed, unit
}

// bytesForLines reconstructs the byte length of a join-on-newline.
func bytesForLines(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	total := len(lines) - 1 // separators
	for _, l := range lines {
		total += len(l)
	}
	return total
}

// buildHint formats the recovery message embedded in truncated output.
// The hint is the user-facing contract for this package and must
// reference both the read tool's offset/limit fields and grep so the
// model can recover specific ranges without reloading the full spill.
func buildHint(removed int, unit string, hitBytes bool, outputPath string) string {
	var head string
	if hitBytes {
		head = fmt.Sprintf("...%d %s truncated (50KB cap)...", removed, unit)
	} else {
		head = fmt.Sprintf("...%d %s truncated (2000-line cap)...", removed, unit)
	}
	if outputPath == "" {
		return head + "\n" +
			"Output truncated. Use grep to filter, or read with offset/limit to view a specific range."
	}
	return head + "\n" +
		"Full output saved to: " + outputPath + "\n" +
		"Use grep to filter the spill file, or call read with offset and limit to view a specific line range."
}

// writeOverflow spills the full original content to a session-scoped
// file and returns the absolute path. On any IO error the function
// returns "" so Apply can still succeed for the model.
func writeOverflow(text string, opts Options) string {
	dir, err := overflowDir(opts)
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	name := overflowFilename(opts)
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(text), 0o600); err != nil {
		return ""
	}
	return full
}

// overflowDir returns the session-scoped on-disk root for spill files.
// Layout: <root>/<session>/ where root defaults to
// UserCacheDir/flowstate/tool-output and session defaults to
// "_unscoped" when no session ID was supplied.
func overflowDir(opts Options) (string, error) {
	root := opts.Dir
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(cache, "flowstate", "tool-output")
	}
	sess := opts.SessionID
	if sess == "" {
		sess = "_unscoped"
	}
	return filepath.Join(root, sanitiseSegment(sess)), nil
}

// overflowFilename returns a deterministic-ish name combining the tool
// label, a millisecond timestamp, and a short random tag so concurrent
// truncations don't collide.
func overflowFilename(opts Options) string {
	tool := opts.ToolName
	if tool == "" {
		tool = "tool"
	}
	ts := time.Now().UnixMilli()
	tag := randomHex(4)
	return fmt.Sprintf("%s-%d-%s.txt", sanitiseSegment(tool), ts, tag)
}

// randomHex returns a hex-encoded random tag of n bytes. Falls back to
// a static label on rand failure (a session-load with no entropy is
// already broken; the spill is best-effort).
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "deadbeef"
	}
	return hex.EncodeToString(buf)
}

// sanitiseSegment strips path separators and other shell metacharacters
// from a directory or file segment so callers can pass session IDs and
// tool names verbatim.
func sanitiseSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	mapper := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}
	return strings.Map(mapper, s)
}

// errOverflowFailed signals a spill-write failure. Currently unused at
// the public API surface — Apply swallows IO errors so truncation
// remains a non-fatal envelope. Exported so future callers that want
// to surface spill failures can do so.
var errOverflowFailed = errors.New("truncate: failed to write overflow spill")

// OverflowError returns the sentinel for spill-write failures.
func OverflowError() error { return errOverflowFailed }
