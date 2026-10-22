package vaultindex

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// MarkdownFile describes a markdown file discovered under a vault root.
//
// AbsPath is the absolute path on disk used for reading content; RelPath is
// the path relative to the vault root and forms the stable identifier used
// in the sidecar and in Qdrant payloads.
type MarkdownFile struct {
	AbsPath string
	RelPath string
	Mtime   time.Time
}

// WalkVault walks vaultRoot and returns the markdown files it contains.
//
// Hidden directories (names beginning with ".") are skipped — most notably
// the Obsidian `.obsidian` config directory and the FlowState
// .flowstate-vault-state.json sidecar — to avoid indexing tooling artefacts
// alongside genuine notes.
//
// Expected:
//   - vaultRoot is an existing directory.
//
// Returns:
//   - A slice of MarkdownFile entries, one per `*.md` file found.
//   - An error when the directory cannot be walked.
//
// Side effects:
//   - Stats every entry under vaultRoot.
func WalkVault(vaultRoot string) ([]MarkdownFile, error) {
	files := make([]MarkdownFile, 0, 128)
	err := filepath.WalkDir(vaultRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path == vaultRoot {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("statting %s: %w", path, err)
		}
		rel, err := filepath.Rel(vaultRoot, path)
		if err != nil {
			return fmt.Errorf("relativising %s: %w", path, err)
		}
		files = append(files, MarkdownFile{
			AbsPath: path,
			RelPath: filepath.ToSlash(rel),
			Mtime:   info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking vault %s: %w", vaultRoot, err)
	}
	return files, nil
}
