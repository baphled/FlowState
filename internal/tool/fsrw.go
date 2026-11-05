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

// Built-in filesystem tool names.
const (
	FSRead  = "fs.read"
	FSWrite = "fs.write"
)

// Typed fs error codes surfaced in tool.result error events; the
// single source for the loop's code mapping.
const (
	// ErrCodePathOutsideWorkdir: traversal/absolute/symlink escape.
	ErrCodePathOutsideWorkdir = "fs_path_outside_workdir"
	// ErrCodeNotFound: read target missing.
	ErrCodeNotFound = "fs_not_found"
	// ErrCodeOversize: size cap exceeded.
	ErrCodeOversize = "fs_oversize"
)

// Default byte caps for reads (memory) and writes (pre-touch).
const (
	DefaultMaxReadBytes  int64 = 1 << 20 // 1 MiB
	DefaultMaxWriteBytes int64 = 1 << 20 // 1 MiB
)

// Typed fs tool errors. The Loop maps each to its ErrCode* constant
// in tool.result error events (see errorCodeFor).
var (
	// ErrPathOutsideWorkdir: path resolves outside the workdir.
	ErrPathOutsideWorkdir = errors.New("tool: path outside session workdir")
	// ErrFileNotFound: read target does not exist.
	ErrFileNotFound = errors.New("tool: file not found")
	// ErrOversize: transfer exceeds the configured cap.
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
	default:
		return "tool_error"
	}
}

// fsOptions carries the overridable size caps.
type fsOptions struct {
	maxRead, maxWrite int64
}

// FSOption customises an fs tool constructor.
type FSOption func(*fsOptions)

// WithMaxReadBytes / WithMaxWriteBytes override the byte caps.
func WithMaxReadBytes(n int64) FSOption  { return func(o *fsOptions) { o.maxRead = n } }
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
	// Defence in depth: the joined path must stay under workdir.
	joined := filepath.Join(workdir, clean)
	if joined != workdir && !strings.HasPrefix(joined, workdir+string(filepath.Separator)) {
		return "", ErrPathOutsideWorkdir
	}
	// Resolve a possibly-symlinked workdir once for the check below.
	root := workdir
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil {
		root = resolved
	}
	// Symlink confinement: the deepest existing ancestor of the
	// target must evaluate back under workdir; missing tails are
	// created by fs.write but must not route out via a symlink.
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

// fsReadTool reads bounded file contents under the workdir.
type fsReadTool struct {
	workdir string
	max     int64
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

func (t *fsReadTool) Name() string { return FSRead }

// Invoke: args {path}; result {path,content,bytes}. Missing ->
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
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": a.Path, "content": string(b), "bytes": len(b)}, nil
}

// fsWriteTool writes bounded content under the workdir.
type fsWriteTool struct {
	workdir string
	max     int64
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

func (t *fsWriteTool) Name() string { return FSWrite }

// Invoke: args {path,content}; result {path,bytes}. Oversize is
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
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"path": a.Path, "bytes": len(a.Content)}, nil
}
