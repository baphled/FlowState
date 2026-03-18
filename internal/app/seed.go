// Package app provides the main application container and initialization.
package app

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// SeedAgentsDir copies all *.json files from the source filesystem to the destination directory.
// It is idempotent: if destination already exists with files, no copy is attempted.
// If a file already exists in the destination, it is not overwritten.
//
// Expected:
//   - srcFS is a valid fs.FS containing an "agents" subdirectory with .json files.
//   - destDir is a writable destination directory path (created if missing).
//
// Returns:
//   - An error if source has no agents directory or if file operations fail.
//   - nil on success.
//
// Side effects:
//   - Creates destDir if it does not exist.
//   - Copies each .json file from srcFS to destDir if not already present.
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
		if filepath.Ext(filename) != ".json" {
			continue
		}

		destPath := filepath.Join(destDir, filename)

		_, err := os.Stat(destPath)
		if err == nil {
			continue
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("checking if %s exists: %w", destPath, err)
		}

		if err := copySingleFile(agentsDir, filename, destPath); err != nil {
			return err
		}
	}

	return nil
}

// copySingleFile copies a single file from srcFS to destPath, properly managing resources.
//
// Expected:
//   - srcFS is a valid fs.FS.
//   - filename is a valid filename to open from srcFS.
//   - destPath is a writable destination file path.
//
// Returns:
//   - An error if opening source, creating destination, or copying data fails.
//   - nil on success.
//
// Side effects:
//   - Creates or overwrites the file at destPath.
func copySingleFile(srcFS fs.FS, filename, destPath string) error {
	srcFile, err := srcFS.Open(filename)
	if err != nil {
		return fmt.Errorf("opening source file %s: %w", filename, err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating destination file %s: %w", destPath, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copying %s: %w", filename, err)
	}

	return nil
}
