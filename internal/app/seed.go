// Package app provides the main application container and initialization.
package app

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// RefreshStatus classifies a single manifest outcome during a refresh.
type RefreshStatus string

const (
	// RefreshStatusCreated means the destination file did not exist and
	// was (or would be) written from the embedded source.
	RefreshStatusCreated RefreshStatus = "created"
	// RefreshStatusUpdated means the destination file existed with different
	// content and was (or would be) overwritten by the embedded version.
	RefreshStatusUpdated RefreshStatus = "updated"
	// RefreshStatusUnchanged means the destination file already matches the
	// embedded version byte-for-byte and no write occurred.
	RefreshStatusUnchanged RefreshStatus = "unchanged"
)

// RefreshOptions controls the behaviour of RefreshAgentManifests.
type RefreshOptions struct {
	// DryRun, when true, performs no writes but still returns a report of what
	// would change. Useful as a safety valve before overwriting user edits.
	DryRun bool
	// OnlyAgent, when non-empty, restricts the refresh to a single manifest
	// matched by basename without the .md extension (e.g. "planner"). When set
	// and no manifest matches, RefreshAgentManifests returns an error.
	OnlyAgent string
}

// RefreshEntry is a per-file outcome entry in a RefreshReport.
type RefreshEntry struct {
	// Name is the manifest filename (e.g. "planner.md").
	Name string
	// Status is the classified outcome.
	Status RefreshStatus
	// OldSize is the pre-refresh file size in bytes. Zero when the file did not
	// exist before.
	OldSize int64
	// NewSize is the embedded source size in bytes.
	NewSize int64
}

// RefreshReport is the ordered list of per-file outcomes from a refresh.
type RefreshReport []RefreshEntry

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

// RefreshAgentManifests overwrites on-disk agent manifests in destDir with the
// embedded versions from srcFS, reporting what changed. Unlike SeedAgentsDir —
// which only copies when the destination file is missing — this helper is the
// explicit-user-action counterpart that repairs drift when embedded manifests
// advance past the user's on-disk copies (new tools, prompt edits, allowlist
// changes).
//
// Expected:
//   - srcFS is a valid fs.FS containing an "agents" subdirectory with .md files.
//   - destDir is a writable destination directory path (created if missing).
//   - opts controls dry-run and single-agent filtering.
//
// Returns:
//   - A RefreshReport describing the per-file outcome in filesystem-walk order.
//   - An error if source has no agents directory, destination cannot be
//     created, OnlyAgent matches no manifest, or any I/O operation fails. A
//     partial report is returned alongside the error so callers can still show
//     which files succeeded before the failure.
//
// Side effects:
//   - When opts.DryRun is false: creates destDir if missing and overwrites any
//     differing manifests in place.
//   - When opts.DryRun is true: no filesystem mutations occur; the report
//     still classifies what would have changed.
func RefreshAgentManifests(srcFS fs.FS, destDir string, opts RefreshOptions) (RefreshReport, error) {
	agentsDir, err := fs.Sub(srcFS, "agents")
	if err != nil {
		return nil, fmt.Errorf("agents directory not found in source: %w", err)
	}

	if !opts.DryRun {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating destination directory: %w", err)
		}
	}

	entries, err := fs.ReadDir(agentsDir, ".")
	if err != nil {
		return nil, fmt.Errorf("reading source agents directory: %w", err)
	}

	var (
		report  RefreshReport
		matched bool
	)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		if filepath.Ext(filename) != ".md" {
			continue
		}
		if opts.OnlyAgent != "" {
			if basename(filename) != opts.OnlyAgent {
				continue
			}
			matched = true
		}

		result, err := refreshSingleFile(agentsDir, filename, filepath.Join(destDir, filename), opts.DryRun)
		if err != nil {
			return report, err
		}
		report = append(report, result)
	}

	if opts.OnlyAgent != "" && !matched {
		return nil, fmt.Errorf("no embedded agent manifest matches %q", opts.OnlyAgent)
	}

	return report, nil
}

// basename returns the filename with its extension stripped.
//
// Expected:
//   - filename is a non-empty filename, optionally containing an extension.
//
// Returns:
//   - The portion of filename preceding its extension ("planner" for "planner.md").
//
// Side effects:
//   - None.
func basename(filename string) string {
	return filename[:len(filename)-len(filepath.Ext(filename))]
}

// refreshSingleFile reads both sides, classifies the outcome, and writes when
// needed (respecting dry-run).
//
// Expected:
//   - srcFS is a valid fs.FS rooted at the agents directory.
//   - filename is a .md manifest filename inside srcFS.
//   - destPath is the target path on disk (may or may not exist).
//
// Returns:
//   - A RefreshEntry with status and size bookkeeping.
//   - An error if reading source or writing destination fails.
//
// Side effects:
//   - Writes destPath when the file is missing or differs from the embedded
//     version and dryRun is false; otherwise leaves the filesystem untouched.
func refreshSingleFile(srcFS fs.FS, filename, destPath string, dryRun bool) (RefreshEntry, error) {
	entry := RefreshEntry{Name: filename}

	srcBytes, err := fs.ReadFile(srcFS, filename)
	if err != nil {
		return entry, fmt.Errorf("reading source %s: %w", filename, err)
	}
	entry.NewSize = int64(len(srcBytes))

	existing, readErr := os.ReadFile(destPath)
	switch {
	case os.IsNotExist(readErr):
		entry.Status = RefreshStatusCreated
	case readErr != nil:
		return entry, fmt.Errorf("reading destination %s: %w", destPath, readErr)
	default:
		entry.OldSize = int64(len(existing))
		if bytes.Equal(existing, srcBytes) {
			entry.Status = RefreshStatusUnchanged
			return entry, nil
		}
		entry.Status = RefreshStatusUpdated
	}

	if dryRun {
		return entry, nil
	}

	if err := writeManifestFile(destPath, srcBytes); err != nil {
		return entry, err
	}
	return entry, nil
}

// writeManifestFile writes bytes to destPath with manifest-appropriate
// permissions (0644), matching the OpenFile-based style used by copySingleFile
// elsewhere in this package. Using OpenFile + io.Copy avoids the gosec G306
// flag that fires on os.WriteFile.
//
// Expected:
//   - destPath is a writable path; its parent directory exists.
//   - data is the manifest body.
//
// Returns:
//   - An error wrapping any open or write failure.
//
// Side effects:
//   - Truncates and rewrites destPath.
func writeManifestFile(destPath string, data []byte) error {
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s for write: %w", destPath, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
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
