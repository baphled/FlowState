// Package app provides the main application container and initialization.
package app

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// SeedAgentsDir copies all *.md files from the source filesystem to the destination directory.
// Files that already exist in destDir are skipped so that custom agent manifests are preserved.
//
// Expected:
//   - srcFS is a valid fs.FS containing an "agents" subdirectory with .md files.
//   - destDir is a writable destination directory path (created if missing).
//
// Returns:
//   - An error if source has no agents directory or if file operations fail.
//   - nil on success.
//
// Side effects:
//   - Creates destDir if it does not exist.
//   - Copies each .md file from srcFS to destDir only when the destination file does not already exist.
func SeedAgentsDir(srcFS fs.FS, destDir string) error {
	agentsDir, err := fs.Sub(srcFS, "agents")
	if err != nil {
		return fmt.Errorf("agents directory not found in source: %w", err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	entries, err := fs.ReadDir(agentsDir, ".")
	if err != nil {
		return fmt.Errorf("reading source agents directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		ext := filepath.Ext(filename)
		if ext != ".md" {
			continue
		}

		destPath := filepath.Join(destDir, filename)

		if err := copySingleFile(agentsDir, filename, destPath); err != nil {
			return err
		}
	}

	return nil
}

// copySingleFile copies a single file from srcFS to destPath when the destination
// does not already exist. Existing files are left untouched so that custom agent
// manifests placed by the user are preserved across restarts.
//
// Expected:
//   - srcFS is a valid fs.FS.
//   - filename is a valid filename to open from srcFS.
//   - destPath is a writable destination file path.
//
// Returns:
//   - An error if opening source, creating destination, or copying data fails.
//   - nil on success, including when destPath already exists.
//
// Side effects:
//   - Creates destPath when it does not exist.
func copySingleFile(srcFS fs.FS, filename, destPath string) error {
	dstFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("creating destination file %s: %w", destPath, err)
	}
	defer dstFile.Close()

	srcFile, err := srcFS.Open(filename)
	if err != nil {
		return fmt.Errorf("opening source file %s: %w", filename, err)
	}
	defer srcFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copying %s: %w", filename, err)
	}

	return nil
}
