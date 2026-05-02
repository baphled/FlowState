// Package pathguard provides path-based access control for file-accessing tools.
//
// This package enforces deny-list restrictions so agents cannot read or write
// sensitive directories (vaults, config) through file tools or bash commands.
// MCP tools bypass these restrictions by design — they are the intended access
// path for protected data.
package pathguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Guard checks filesystem paths against a deny list.
type Guard struct {
	denied []string
}

// New creates a Guard that blocks access to any path under the given denied
// directories. Each entry must be an absolute path; relative paths are
// silently ignored. Nil or empty denied is a no-op (all paths allowed).
func New(denied []string) *Guard {
	abs := make([]string, 0, len(denied))
	for _, d := range denied {
		if d == "" {
			continue
		}
		a, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		abs = append(abs, a)
	}
	return &Guard{denied: abs}
}

// Check returns an error when path resolves inside any denied directory.
// If the current working directory is itself inside a denied directory,
// the check passes (the user chose to work inside that directory).
func (g *Guard) Check(path string) error {
	if len(g.denied) == 0 {
		return nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	cwd, _ := os.Getwd()
	for _, d := range g.denied {
		if cwd != "" && strings.HasPrefix(cwd, d+string(filepath.Separator)) {
			continue
		}
		if strings.HasPrefix(abs, d+string(filepath.Separator)) || abs == d {
			return fmt.Errorf("access denied: %s is a protected path (use the appropriate MCP tool)", path)
		}
	}
	return nil
}

// CheckCommand scans a bash command string for references to denied paths.
// This is a best-effort heuristic — it catches obvious cases like
// "cat ~/vaults/..." or "vim /path/to/vault" but is not a security sandbox.
func (g *Guard) CheckCommand(command string) error {
	if len(g.denied) == 0 {
		return nil
	}

	home, _ := os.UserHomeDir()
	expanded := command
	if home != "" {
		expanded = strings.ReplaceAll(expanded, "~", home)
		expanded = strings.ReplaceAll(expanded, "$HOME", home)
	}

	for _, d := range g.denied {
		if strings.Contains(expanded, d) {
			cwd, _ := os.Getwd()
			if cwd != "" && strings.HasPrefix(cwd, d+string(filepath.Separator)) {
				continue
			}
			return fmt.Errorf("access denied: command references protected path %s (use the appropriate MCP tool)", d)
		}
	}
	return nil
}
