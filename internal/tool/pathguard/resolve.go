package pathguard

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrPathTraversal is returned when a tool-supplied file path contains a
// parent-directory ("..") reference that would escape its starting
// directory. Every file-accessing tool surfaces the same error so an
// agent receives an identical message regardless of which tool it
// invoked.
var ErrPathTraversal = errors.New("path traversal not allowed")

// ResolvePath normalises a tool-supplied file path and enforces the one
// path policy shared by every file-accessing tool — read, write, edit,
// multiedit and apply_patch.
//
// The policy is intentionally permissive about location: both relative
// and absolute paths are permitted, so an agent can edit files outside
// the process working directory (for example manifests under
// ~/.config/flowstate). The pathguard deny-list is the gate that
// protects sensitive directories; this helper only rejects
// parent-directory ("..") traversal.
//
// The earlier edit/multiedit/apply_patch confinement to the working
// directory (filepath.IsLocal plus os.OpenRoot) produced an asymmetry
// where read and write accepted absolute paths but the mutating tools
// did not. ResolvePath is the single source of truth that keeps all
// five tools in lock-step, preventing that drift from recurring.
//
// It returns the cleaned path suitable for os.ReadFile / os.WriteFile,
// or ErrPathTraversal when the path traverses a parent directory.
func ResolvePath(raw string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(raw))
	if strings.Contains(cleaned, "..") {
		return "", ErrPathTraversal
	}
	return cleaned, nil
}
