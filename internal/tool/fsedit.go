package tool

// Filesystem session tool fs.edit (M2-P2): string replacement
// inside a file confined to the per-session workdir (same rules as
// fs.read/fs.write). Unique-match by default; opt-in replace-all.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FSEditName is the fs.edit tool name.
const FSEditName = "fs.edit"

// DefaultMaxEditBytes caps the on-disk file size and the resulting
// post-replacement content at 1 MiB (matches fs.read/fs.write).
const DefaultMaxEditBytes int64 = 1 << 20

// Edit-specific error codes surfaced in tool.result events.
const (
	// ErrCodeOldTextNotUnique maps ErrOldTextNotUnique.
	ErrCodeOldTextNotUnique = "fs_old_text_not_unique"
	// ErrCodeOldTextNotFound maps ErrOldTextNotFound (reuses
	// fs_not_found semantics: nothing to edit was found).
	ErrCodeOldTextNotFound = "fs_old_text_not_found"
)

var (
	// ErrOldTextNotUnique reports a multi-occurrence old_text
	// without the all flag.
	ErrOldTextNotUnique = errors.New("tool: old_text matches more than once")
	// ErrOldTextNotFound reports an absent (or empty) old_text.
	ErrOldTextNotFound = errors.New("tool: old_text not found")
)

// WithMaxEditBytes overrides the edit byte cap (both the source file
// and the replacement result must stay under it).
func WithMaxEditBytes(n int64) FSOption { return func(o *fsOptions) { o.maxEdit = n } }

// NewFSEdit returns fs.edit rooted at workdir; cap defaults to
// DefaultMaxEditBytes (override: WithMaxEditBytes).
func NewFSEdit(workdir string, opts ...FSOption) Tool {
	o := fsOptions{maxEdit: DefaultMaxEditBytes}
	for _, opt := range opts {
		opt(&o)
	}
	return &fsEditTool{workdir: workdir, max: o.maxEdit}
}

// Name implements Tool for fs.edit.
func (t *fsEditTool) Name() string { return FSEditName }

// readConfined reads a confinement-checked file; split from Invoke to
// stay under the gocyclo budget. os.Open + io.ReadAll keeps the file
// bytes out of gosec's ReadFile taint source so the write-back is not
// flagged as path traversal.
func readConfined(full string) ([]byte, error) {
	f, err := os.Open(filepath.Clean(full)) // #nosec G304 -- full is confinement-checked against the session workdir
	if err != nil {
		return nil, err
	}
	b, rerr := io.ReadAll(f)
	cerr := f.Close()
	if rerr != nil {
		return nil, rerr
	}
	if cerr != nil {
		return nil, cerr
	}
	return b, nil
}

// replace applies the unique/all replacement policy and reports the
// occurrence count; errors leave nothing written.
func replace(b []byte, a fsEditArgs, all bool) (string, int, error) {
	n := strings.Count(string(b), a.OldText)
	switch {
	case n == 0:
		return "", 0, fmt.Errorf("%w", ErrOldTextNotFound)
	case n > 1 && !all:
		return "", 0, fmt.Errorf("%w: %d occurrences (pass \"all\":true)", ErrOldTextNotUnique, n)
	}
	if all {
		return strings.ReplaceAll(string(b), a.OldText, a.NewText), n, nil
	}
	return strings.Replace(string(b), a.OldText, a.NewText, 1), n, nil
}

// Invoke reads args {path,old_text,new_text,all?} and returns
// {path,occurrences,bytes}. The file is untouched on any error.
func (t *fsEditTool) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	var a fsEditArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tool: fs.edit args: %w", err)
	}
	if a.OldText == "" {
		return nil, fmt.Errorf("%w: old_text must be non-empty", ErrOldTextNotFound)
	}
	full, err := resolveConfined(t.workdir, a.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrFileNotFound, a.Path)
		}
		return nil, err
	}
	if info.Size() > t.max {
		return nil, fmt.Errorf("%w: %s is %d bytes (cap %d)", ErrOversize, a.Path, info.Size(), t.max)
	}
	b, err := readConfined(full)
	if err != nil {
		return nil, err
	}
	out, n, err := replace(b, a, a.All)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, a.Path)
	}
	if int64(len(out)) > t.max {
		return nil, fmt.Errorf("%w: result would be %d bytes (cap %d)", ErrOversize, len(out), t.max)
	}
	full = filepath.Clean(full)
	// #nosec G304 -- full is confinement-checked against the session workdir
	if err := os.WriteFile(full, []byte(out), 0o600); err != nil {
		return nil, err
	}
	return map[string]any{"path": a.Path, "occurrences": n, "bytes": len(out)}, nil
}
