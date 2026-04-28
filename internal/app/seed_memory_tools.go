// Package app provides the main application container and initialization.
package app

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// MemoryToolsEmbedSubdir is the subdirectory inside the embedded memory-tools
// fs.FS that holds the payload. Exposed as a constant so callers and
// tests do not have to hard-code the literal string.
const MemoryToolsEmbedSubdir = "memory_tools"

// MemoryToolStatus classifies a single payload outcome during an install.
// Mirrors VaultToolStatus to keep the CLI report shape consistent across
// embed-and-materialise commands.
type MemoryToolStatus string

const (
	// MemoryToolStatusCreated means the destination file did not exist and was
	// (or would be) written from the embedded source.
	MemoryToolStatusCreated MemoryToolStatus = "created"
	// MemoryToolStatusUpdated means the destination file existed with different
	// content and was (or would be) overwritten because Force was set.
	MemoryToolStatusUpdated MemoryToolStatus = "updated"
	// MemoryToolStatusUnchanged means the destination file already matches the
	// embedded version byte-for-byte and no write occurred.
	MemoryToolStatusUnchanged MemoryToolStatus = "unchanged"
	// MemoryToolStatusSkipped means the destination file existed with different
	// content and Force was not set, so the embedded version was left
	// unwritten. The operator can re-run with --force to overwrite.
	MemoryToolStatusSkipped MemoryToolStatus = "skipped"
)

// MemoryToolsInstallOptions controls InstallMemoryTools.
type MemoryToolsInstallOptions struct {
	// DryRun, when true, performs no writes but still classifies what would
	// change. Useful as a safety valve before overwriting an operator's
	// already-customised payload.
	DryRun bool
	// Force, when true, overwrites destination files whose content differs
	// from the embedded version. When false, differing files are reported as
	// "skipped" and the operator's copy is preserved.
	Force bool
}

// MemoryToolsEntry is a per-file outcome entry in a MemoryToolsReport.
type MemoryToolsEntry struct {
	// Name is the payload filename (e.g. "mcp-mem0-server.js").
	Name string
	// Status is the classified outcome.
	Status MemoryToolStatus
	// OldSize is the pre-install file size in bytes. Zero when the file did
	// not exist before.
	OldSize int64
	// NewSize is the embedded source size in bytes.
	NewSize int64
}

// MemoryToolsReport is the ordered list of per-file outcomes from an install.
type MemoryToolsReport []MemoryToolsEntry

// memoryToolsExecMode is the file mode applied to materialised memory-tool
// payload entries. The bash wrapper is invoked directly from PATH, so the
// executable bit is load-bearing on at least one entry; applying it to the
// bundled `.js` is harmless because `node` does not consult the bit when
// the script is passed as an argument. Mirrors vaultToolsExecMode for
// consistency across embed-and-materialise commands — without 0o755 the
// operator would have to chmod after every install, defeating the
// bootstrap-from-binary contract.
const memoryToolsExecMode os.FileMode = 0o755

// InstallMemoryTools materialises the embedded memory-tool payload into
// destDir, preserving the executable bit so the wrapper can be invoked
// directly from PATH on a fresh machine.
//
// Default semantics: skip-on-existing for files that differ from the
// embedded version, mirroring InstallVaultTools' "do not clobber operator
// edits" contract. Pass opts.Force to opt into byte-compare-overwrite.
//
// Expected:
//   - srcFS is a valid fs.FS containing a "memory_tools" subdirectory with
//     regular files (no extension required — the embed pattern accepts
//     every entry).
//   - destDir is a writable destination directory path (created if
//     missing when not in dry-run).
//   - opts controls dry-run and force behaviour.
//
// Returns:
//   - A MemoryToolsReport describing the per-file outcome in fs walk order.
//   - An error if the source has no memory_tools directory, the destination
//     cannot be created, or any I/O operation fails. A partial report is
//     returned alongside the error so callers can still show which files
//     succeeded before the failure.
//
// Side effects:
//   - When opts.DryRun is false: creates destDir if missing, writes any
//     missing payload files, and overwrites differing files when opts.Force
//     is true. Files are written with mode 0o755.
//   - When opts.DryRun is true: no filesystem mutations occur; the report
//     still classifies what would have changed.
func InstallMemoryTools(srcFS fs.FS, destDir string, opts MemoryToolsInstallOptions) (MemoryToolsReport, error) {
	toolsDir, err := fs.Sub(srcFS, MemoryToolsEmbedSubdir)
	if err != nil {
		return nil, fmt.Errorf("memory_tools directory not found in source: %w", err)
	}

	if !opts.DryRun {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating destination directory: %w", err)
		}
	}

	entries, err := fs.ReadDir(toolsDir, ".")
	if err != nil {
		return nil, fmt.Errorf("reading source memory_tools directory: %w", err)
	}

	var report MemoryToolsReport
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		result, err := installSingleMemoryTool(toolsDir, filename, filepath.Join(destDir, filename), opts)
		if err != nil {
			return report, err
		}
		report = append(report, result)
	}

	return report, nil
}

// installSingleMemoryTool reads both sides, classifies the outcome, and
// writes when needed (respecting dry-run and force).
//
// Expected:
//   - srcFS is a valid fs.FS rooted at the memory_tools directory.
//   - filename is a regular filename inside srcFS.
//   - destPath is the target path on disk (may or may not exist).
//   - opts is the resolved install options.
//
// Returns:
//   - A MemoryToolsEntry with status and size bookkeeping.
//   - An error if reading source or writing destination fails.
//
// Side effects:
//   - Writes destPath with mode 0o755 when the file is missing, or when
//     Force is set and the file differs, and DryRun is false. Otherwise
//     leaves the filesystem untouched.
func installSingleMemoryTool(srcFS fs.FS, filename, destPath string, opts MemoryToolsInstallOptions) (MemoryToolsEntry, error) {
	entry := MemoryToolsEntry{Name: filename}

	srcBytes, err := fs.ReadFile(srcFS, filename)
	if err != nil {
		return entry, fmt.Errorf("reading source %s: %w", filename, err)
	}
	entry.NewSize = int64(len(srcBytes))

	existing, readErr := os.ReadFile(destPath) //nolint:gosec // destPath is a filename joined onto a caller-provided destDir
	switch {
	case os.IsNotExist(readErr):
		entry.Status = MemoryToolStatusCreated
	case readErr != nil:
		return entry, fmt.Errorf("reading destination %s: %w", destPath, readErr)
	default:
		entry.OldSize = int64(len(existing))
		if bytes.Equal(existing, srcBytes) {
			entry.Status = MemoryToolStatusUnchanged
			return entry, nil
		}
		if !opts.Force {
			entry.Status = MemoryToolStatusSkipped
			return entry, nil
		}
		entry.Status = MemoryToolStatusUpdated
	}

	if opts.DryRun {
		return entry, nil
	}

	if err := writeMemoryToolFile(destPath, srcBytes); err != nil {
		return entry, err
	}
	return entry, nil
}

// writeMemoryToolFile writes bytes to destPath with executable permissions
// (0o755). Mirrors writeVaultToolFile's OpenFile + explicit Chmod style to
// avoid the gosec G306 flag os.WriteFile triggers, while applying the
// executable bit the materialised wrapper needs to be invokable from PATH.
//
// Expected:
//   - destPath is a writable path; its parent directory exists.
//   - data is the file body.
//
// Returns:
//   - An error wrapping any open or write failure.
//
// Side effects:
//   - Truncates and rewrites destPath with mode 0o755.
func writeMemoryToolFile(destPath string, data []byte) error {
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, memoryToolsExecMode)
	if err != nil {
		return fmt.Errorf("opening %s for write: %w", destPath, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	// OpenFile honours the umask, so the resulting mode may be 0o755 & ~umask
	// (typically 0o755 on a default 0o022 umask, 0o744 on 0o033). Force the
	// executable bit explicitly so the wrapper is always invokable
	// regardless of the operator's umask.
	if err := os.Chmod(destPath, memoryToolsExecMode); err != nil {
		return fmt.Errorf("chmod %s: %w", destPath, err)
	}
	return nil
}

// DefaultMemoryToolsDir resolves the default destination for materialised
// memory-tool payload entries. It does NOT default to
// `~/.config/opencode/plugins/lib/dist/` because that directory is the
// canonical source-of-truth in the dotopencode repo — clobbering it from
// FlowState would create a "who wrote this file?" ambiguity. Instead,
// default to a FlowState-owned directory under XDG_DATA so a fresh-machine
// bootstrap has a clean recovery path; the operator can symlink or
// PATH-prepend afterwards.
//
// Returns:
//   - The absolute path to the default memory-tools directory when the
//     home directory can be resolved.
//   - A relative fallback path when the home directory cannot be
//     resolved (e.g. exotic CI environments). Callers should treat this
//     as a sentinel and decide how to surface the error.
//
// Side effects:
//   - Reads $HOME via os.UserHomeDir.
func DefaultMemoryToolsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".local", "share", "flowstate", "memory-tools")
	}
	return filepath.Join(home, ".local", "share", "flowstate", "memory-tools")
}
