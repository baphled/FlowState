package tool

// fs.edit tool (M2-P2): plain-string, unique-match replacement in a
// file under the session workdir, reusing the fsrw confinement and
// byte-cap machinery. Ambiguity is refused so an edit can never
// silently pick the wrong occurrence.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FSEditName is the fs.edit tool name.
const FSEditName = "fs.edit"

// ErrEditAmbiguous reports more than one occurrence of find.
var ErrEditAmbiguous = errors.New("tool: find occurs more than once")

// ErrEditNotFound reports zero occurrences of find.
var ErrEditNotFound = errors.New("tool: find not present")

// NewFSEdit returns fs.edit rooted at workdir; caps default to
// DefaultMaxReadBytes / DefaultMaxWriteBytes (override: WithMaxReadBytes /
// WithMaxWriteBytes).
func NewFSEdit(workdir string, opts ...FSOption) Tool {
	o := fsOptions{maxRead: DefaultMaxReadBytes, maxWrite: DefaultMaxWriteBytes}
	for _, opt := range opts {
		opt(&o)
	}
	return &fsEditTool{workdir: workdir, maxRead: o.maxRead, maxWrite: o.maxWrite}
}

// Name implements Tool for fs.edit.
func (t *fsEditTool) Name() string { return FSEditName }

// Invoke reads args {path,find,replace} and rewrites the file with
// the single occurrence of find replaced, returning {path,bytes}.
// find must occur exactly once: !=1 occurrences are an error and the
// file is left untouched. Oversize source or result is rejected
// before any write.
func (t *fsEditTool) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Path    string `json:"path"`
		Find    string `json:"find"`
		Replace string `json:"replace"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tool: fs.edit args: %w", err)
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
	if info.Size() > t.maxRead {
		return nil, fmt.Errorf("%w: %s is %d bytes (cap %d)", ErrOversize, a.Path, info.Size(), t.maxRead)
	}
	b, err := os.ReadFile(filepath.Clean(full)) // #nosec G304 -- full is confinement-checked against the session workdir
	if err != nil {
		return nil, err
	}
	content := string(b)
	switch n := strings.Count(content, a.Find); {
	case a.Find != "" && n == 0:
		return nil, fmt.Errorf("%w: %q not present in %s", ErrEditNotFound, a.Find, a.Path)
	case a.Find != "" && n > 1:
		return nil, fmt.Errorf("%w: %q occurs %d times in %s", ErrEditAmbiguous, a.Find, n, a.Path)
	}
	out := strings.Replace(content, a.Find, a.Replace, 1)
	if int64(len(out)) > t.maxWrite {
		return nil, fmt.Errorf("%w: result is %d bytes (cap %d)", ErrOversize, len(out), t.maxWrite)
	}
	if err := os.WriteFile(full, []byte(out), 0o600); err != nil {
		return nil, err
	}
	return map[string]any{"path": a.Path, "bytes": len(out)}, nil
}
