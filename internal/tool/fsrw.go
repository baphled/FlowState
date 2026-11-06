package tool

// Filesystem session tools fs.read / fs.write (M2-P1): cleaned
// relative paths confined to a per-session workdir (no traversal,
// absolute paths or symlink escapes) with byte-capped transfers.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// FSReadName is the fs.read tool name.
	FSReadName = "fs.read"
	// FSWriteName is the fs.write tool name.
	FSWriteName = "fs.write"
)

// Typed fs error codes surfaced in tool.result events.
const (
	// ErrCodePathOutsideWorkdir maps ErrPathOutsideWorkdir.
	ErrCodePathOutsideWorkdir = "fs_path_outside_workdir"
	// ErrCodeNotFound maps ErrFileNotFound.
	ErrCodeNotFound = "fs_not_found"
	// ErrCodeOversize maps ErrOversize.
	ErrCodeOversize = "fs_oversize"
)

// Byte caps for reads (memory) and writes (pre-touch).
const (
	// DefaultMaxReadBytes caps fs.read at 1 MiB.
	DefaultMaxReadBytes int64 = 1 << 20
	// DefaultMaxWriteBytes caps fs.write at 1 MiB.
	DefaultMaxWriteBytes int64 = 1 << 20
)

// Typed fs tool errors, mapped to ErrCode* by errorCodeFor.
var (
	// ErrPathOutsideWorkdir reports escape from the workdir.
	ErrPathOutsideWorkdir = errors.New("tool: path outside session workdir")
	// ErrFileNotFound reports a missing read target.
	ErrFileNotFound = errors.New("tool: file not found")
	// ErrOversize reports a transfer over the cap.
	ErrOversize = errors.New("tool: content exceeds size cap")
)

// errorCodeFor maps a tool error to its tool.result error.code;
// unmapped errors keep the generic tool_error code.
func errorCodeFor(err error) string {
	switch {
	case errors.Is(err, ErrPathOutsideWorkdir):
		return ErrCodePathOutsideWorkdir
	case errors.Is(err, ErrFileNotFound):
		return ErrCodeNotFound
	case errors.Is(err, ErrOversize):
		return ErrCodeOversize
	case errors.Is(err, ErrOldTextNotUnique):
		return ErrCodeOldTextNotUnique
	case errors.Is(err, ErrOldTextNotFound):
		return ErrCodeOldTextNotFound
	case errors.Is(err, ErrExecNotEnabled):
		return ErrCodeExecNotEnabled
	case errors.Is(err, ErrExecNotAllowed):
		return ErrCodeExecNotAllowed
	case errors.Is(err, ErrExecDeniedSubcommand):
		return ErrCodeExecDeniedSubcommand
	case errors.Is(err, ErrExecTimeout):
		return ErrCodeExecTimeout
	case errors.Is(err, ErrExecOversize):
		return ErrCodeExecOversize
	default:
		return "tool_error"
	}
}

// WithMaxReadBytes / WithMaxWriteBytes override the byte caps.
func WithMaxReadBytes(n int64) FSOption { return func(o *fsOptions) { o.maxRead = n } }

// WithMaxWriteBytes overrides the write byte cap.
func WithMaxWriteBytes(n int64) FSOption { return func(o *fsOptions) { o.maxWrite = n } }

// resolveConfined cleans rel and joins it onto workdir, rejecting
// empty/absolute/traversing paths and symlink escapes.
func resolveConfined(workdir, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", ErrPathOutsideWorkdir
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideWorkdir
	}
	joined := filepath.Join(workdir, clean) // must stay under workdir
	if joined != workdir && !strings.HasPrefix(joined, workdir+string(filepath.Separator)) {
		return "", ErrPathOutsideWorkdir
	}
	root := workdir
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil {
		root = resolved
	}
	// The deepest existing ancestor of the target must evaluate back
	// under workdir; missing tails are created by fs.write but must
	// not route out via a symlink.
	probe := joined
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
				return "", ErrPathOutsideWorkdir
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break // reached filesystem root without a hit
		}
		probe = parent
	}
	return joined, nil
}

// NewFSRead returns fs.read rooted at workdir; cap defaults to
// DefaultMaxReadBytes (override: WithMaxReadBytes).
func NewFSRead(workdir string, opts ...FSOption) Tool {
	o := fsOptions{maxRead: DefaultMaxReadBytes}
	for _, opt := range opts {
		opt(&o)
	}
	return &fsReadTool{workdir: workdir, max: o.maxRead}
}

// Name implements Tool for fs.read.
func (t *fsReadTool) Name() string { return FSReadName }

// Invoke reads args {path} and returns {path,content,bytes}. Missing ->
// ErrFileNotFound, big -> ErrOversize.
func (t *fsReadTool) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tool: fs.read args: %w", err)
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
	b, err := os.ReadFile(filepath.Clean(full)) // #nosec G304 -- full is confinement-checked against the session workdir
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": a.Path, "content": string(b), "bytes": len(b)}, nil
}

// NewFSWrite returns fs.write rooted at workdir; cap defaults to
// DefaultMaxWriteBytes (override: WithMaxWriteBytes).
func NewFSWrite(workdir string, opts ...FSOption) Tool {
	o := fsOptions{maxWrite: DefaultMaxWriteBytes}
	for _, opt := range opts {
		opt(&o)
	}
	return &fsWriteTool{workdir: workdir, max: o.maxWrite}
}

// Name implements Tool for fs.write.
func (t *fsWriteTool) Name() string { return FSWriteName }

// Invoke reads args {path,content} and returns {path,bytes}. Oversize is
// rejected before any file is touched.
func (t *fsWriteTool) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tool: fs.write args: %w", err)
	}
	if int64(len(a.Content)) > t.max {
		return nil, fmt.Errorf("%w: %s is %d bytes (cap %d)", ErrOversize, a.Path, len(a.Content), t.max)
	}
	full, err := resolveConfined(t.workdir, a.Path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, []byte(a.Content), 0o600); err != nil {
		return nil, err
	}
	return map[string]any{"path": a.Path, "bytes": len(a.Content)}, nil
}
