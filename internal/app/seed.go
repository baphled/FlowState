// Package app provides the main application container and initialization.
package app

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// MigrateAgentsResult classifies the outcome of a single
// MigrateAgentsToConfigDir call so callers (and tests) can reason about
// which branch fired without re-stating the on-disk state.
type MigrateAgentsResult string

const (
	// MigrateAgentsResultMigrated means at least one .md manifest was
	// copied from the legacy XDG_DATA location into the new XDG_CONFIG
	// location. The legacy directory is left intact for the user to
	// review and remove.
	MigrateAgentsResultMigrated MigrateAgentsResult = "migrated"
	// MigrateAgentsResultSkippedNewExists means the new XDG_CONFIG
	// agent directory already exists, so the migration is a no-op —
	// XDG_CONFIG always wins when both locations exist.
	MigrateAgentsResultSkippedNewExists MigrateAgentsResult = "skipped-new-exists"
	// MigrateAgentsResultSkippedNoLegacy means there was nothing to
	// migrate: the legacy XDG_DATA directory is missing or empty.
	MigrateAgentsResultSkippedNoLegacy MigrateAgentsResult = "skipped-no-legacy"
)

// MigrateAgentsToConfigDir copies agent manifests from the legacy XDG_DATA
// location (oldDir) into the canonical XDG_CONFIG location (newDir) the
// first time FlowState starts after the AgentDir default flipped from
// `~/.local/share/flowstate/agents/` to `~/.config/flowstate/agents/`.
//
// The migration is intentionally a *copy*, not a *move*: the legacy
// directory is left in place so a user with multiple FlowState versions
// or external tooling pointed at the old path is not surprised by a
// silent disappearance. A single WARN is emitted instructing the user to
// delete the legacy directory at their convenience.
//
// Resolution rules:
//   - If newDir already exists (any content), the migration is a no-op
//     and returns MigrateAgentsResultSkippedNewExists. XDG_CONFIG wins
//     when both locations are populated — this matches the "config
//     overrides cache" precedent that runs through the rest of the
//     codebase (see DefaultConfig()'s AgentDir godoc).
//   - If newDir does not exist and oldDir is missing or contains no
//     .md files, return MigrateAgentsResultSkippedNoLegacy.
//   - Otherwise create newDir, copy every .md from oldDir into it, and
//     return MigrateAgentsResultMigrated.
//
// Expected:
//   - oldDir is the legacy XDG_DATA agents directory path (may not exist).
//   - newDir is the new XDG_CONFIG agents directory path (may not exist).
//
// Returns:
//   - The classified outcome (see MigrateAgentsResult).
//   - An error if a real I/O failure prevented an otherwise-valid
//     migration. A missing oldDir or an empty legacy directory is NOT
//     an error — they collapse to MigrateAgentsResultSkippedNoLegacy.
//
// Side effects:
//   - Creates newDir and copies manifests when a migration is required.
//   - Emits a single slog.Warn message on a successful migration so the
//     user knows the legacy directory can be removed.
//   - Reads oldDir and stat()s newDir.
func MigrateAgentsToConfigDir(oldDir, newDir string) (MigrateAgentsResult, error) {
	if _, err := os.Stat(newDir); err == nil {
		return MigrateAgentsResultSkippedNewExists, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat new agents dir %q: %w", newDir, err)
	}

	entries, err := os.ReadDir(oldDir)
	if err != nil {
		if os.IsNotExist(err) {
			return MigrateAgentsResultSkippedNoLegacy, nil
		}
		return "", fmt.Errorf("reading legacy agents dir %q: %w", oldDir, err)
	}

	manifests := filterManifestEntries(entries)
	if len(manifests) == 0 {
		return MigrateAgentsResultSkippedNoLegacy, nil
	}

	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return "", fmt.Errorf("creating new agents dir %q: %w", newDir, err)
	}

	for _, name := range manifests {
		src := filepath.Join(oldDir, name)
		dst := filepath.Join(newDir, name)
		if err := copyManifestFromDisk(src, dst); err != nil {
			return "", err
		}
	}

	slog.Warn(
		"migrated agent manifests from XDG_DATA to XDG_CONFIG; "+
			"you can safely delete the old directory",
		"old_dir", oldDir,
		"new_dir", newDir,
		"count", len(manifests),
	)
	return MigrateAgentsResultMigrated, nil
}

// filterManifestEntries returns the .md filenames inside entries, skipping
// directories and non-manifest files. The order matches the input order
// so callers see a stable copy sequence.
//
// Expected:
//   - entries is the result of os.ReadDir on a manifests directory.
//
// Returns:
//   - A slice of filenames ending in .md.
//
// Side effects:
//   - None.
func filterManifestEntries(entries []os.DirEntry) []string {
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		out = append(out, entry.Name())
	}
	return out
}

// copyManifestFromDisk copies a single manifest file from srcPath to
// destPath, creating destPath with manifest-appropriate permissions
// (0644). Unlike copySingleFile (which reads from an fs.FS and refuses to
// overwrite), this helper reads from the real filesystem and overwrites
// destPath if it exists — the caller (MigrateAgentsToConfigDir) has
// already verified the destination directory is empty before invoking it.
//
// Expected:
//   - srcPath points at a readable manifest file on disk.
//   - destPath's parent directory exists.
//
// Returns:
//   - An error wrapping the failing I/O step.
//
// Side effects:
//   - Creates or truncates destPath.
func copyManifestFromDisk(srcPath, destPath string) error {
	srcFile, err := os.Open(srcPath) //nolint:gosec // srcPath comes from os.ReadDir of a configured agents dir
	if err != nil {
		return fmt.Errorf("opening legacy manifest %q: %w", srcPath, err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("creating new manifest %q: %w", destPath, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copying manifest %q -> %q: %w", srcPath, destPath, err)
	}
	return nil
}

// MigrateSkillsResult classifies the outcome of a single
// MigrateSkillsToConfigDir call so callers (and tests) can reason about
// which branch fired without re-stating the on-disk state. Mirrors
// MigrateAgentsResult.
type MigrateSkillsResult string

const (
	// MigrateSkillsResultMigrated means at least one skill bundle was
	// copied from the legacy XDG_DATA location into the new XDG_CONFIG
	// location. The legacy directory is left intact for the user to
	// review and remove.
	MigrateSkillsResultMigrated MigrateSkillsResult = "migrated"
	// MigrateSkillsResultSkippedNewExists means the new XDG_CONFIG skill
	// directory already exists, so the migration is a no-op — XDG_CONFIG
	// always wins when both locations exist.
	MigrateSkillsResultSkippedNewExists MigrateSkillsResult = "skipped-new-exists"
	// MigrateSkillsResultSkippedNoLegacy means there was nothing to
	// migrate: the legacy XDG_DATA directory is missing or contains no
	// skill bundles.
	MigrateSkillsResultSkippedNoLegacy MigrateSkillsResult = "skipped-no-legacy"
)

// MigrateSkillsToConfigDir copies skill bundles from the legacy XDG_DATA
// location (oldDir) into the canonical XDG_CONFIG location (newDir) the
// first time FlowState starts after the SkillDir default flipped from
// `~/.local/share/flowstate/skills/` to `~/.config/flowstate/skills/`.
//
// A "skill bundle" is a subdirectory of oldDir containing a SKILL.md
// file, optionally accompanied by extra files (resources, references).
// The migration walks the bundle tree and copies every regular file
// under each qualifying bundle, preserving the relative path layout.
//
// Like MigrateAgentsToConfigDir, this is a *copy*, not a *move*: the
// legacy directory is left in place so a user with multiple FlowState
// versions or external tooling pointed at the old path is not surprised
// by a silent disappearance. A single WARN is emitted instructing the
// user to delete the legacy directory at their convenience.
//
// Resolution rules:
//   - If newDir already exists (any content), the migration is a no-op
//     and returns MigrateSkillsResultSkippedNewExists. XDG_CONFIG wins
//     when both locations are populated — matching the agents rule.
//   - If newDir does not exist and oldDir is missing or contains no
//     bundles with a SKILL.md, return MigrateSkillsResultSkippedNoLegacy.
//   - Otherwise create newDir, copy every qualifying bundle into it, and
//     return MigrateSkillsResultMigrated.
//
// Expected:
//   - oldDir is the legacy XDG_DATA skills directory path (may not exist).
//   - newDir is the new XDG_CONFIG skills directory path (may not exist).
//
// Returns:
//   - The classified outcome (see MigrateSkillsResult).
//   - An error if a real I/O failure prevented an otherwise-valid
//     migration. A missing oldDir or an empty legacy directory is NOT
//     an error — they collapse to MigrateSkillsResultSkippedNoLegacy.
//
// Side effects:
//   - Creates newDir and copies bundles when a migration is required.
//   - Emits a single slog.Warn message on a successful migration so the
//     user knows the legacy directory can be removed.
//   - Reads oldDir and stat()s newDir.
func MigrateSkillsToConfigDir(oldDir, newDir string) (MigrateSkillsResult, error) {
	if _, err := os.Stat(newDir); err == nil {
		return MigrateSkillsResultSkippedNewExists, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat new skills dir %q: %w", newDir, err)
	}

	bundles, err := findSkillBundles(oldDir)
	if err != nil {
		return "", err
	}
	if len(bundles) == 0 {
		return MigrateSkillsResultSkippedNoLegacy, nil
	}

	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return "", fmt.Errorf("creating new skills dir %q: %w", newDir, err)
	}

	for _, bundle := range bundles {
		src := filepath.Join(oldDir, bundle)
		dst := filepath.Join(newDir, bundle)
		if err := copySkillBundleFromDisk(src, dst); err != nil {
			return "", err
		}
	}

	slog.Warn(
		"migrated skill bundles from XDG_DATA to XDG_CONFIG; "+
			"you can safely delete the old directory",
		"old_dir", oldDir,
		"new_dir", newDir,
		"count", len(bundles),
	)
	return MigrateSkillsResultMigrated, nil
}

// findSkillBundles returns the names of subdirectories of oldDir that
// look like skill bundles — i.e. they contain a SKILL.md file at the
// top level. Non-bundle entries (loose files, directories without
// SKILL.md) are silently ignored so a malformed legacy directory does
// not block a migration of the well-formed bundles around it.
//
// Returns:
//   - The bundle subdirectory names in os.ReadDir order.
//   - An error if oldDir cannot be read for a reason other than not
//     existing. A missing oldDir produces a nil slice and nil error so
//     callers can treat it as "no legacy state to migrate".
//
// Side effects:
//   - Reads oldDir and stat()s SKILL.md inside each candidate.
func findSkillBundles(oldDir string) ([]string, error) {
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading legacy skills dir %q: %w", oldDir, err)
	}

	var out []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest := filepath.Join(oldDir, entry.Name(), "SKILL.md")
		info, err := os.Stat(manifest)
		if err != nil || info.IsDir() {
			continue
		}
		out = append(out, entry.Name())
	}
	return out, nil
}

// copySkillBundleFromDisk recursively copies every regular file under
// srcDir into destDir, preserving the relative path layout and creating
// intermediate directories with 0755 permissions. Unlike copySingleFile
// (which refuses to overwrite), this helper overwrites existing files —
// the caller (MigrateSkillsToConfigDir) has already verified destDir is
// missing before invoking it, so the only writes happen on freshly-created
// destination paths.
//
// Expected:
//   - srcDir points at a readable skill bundle directory on disk.
//   - destDir's parent directory exists.
//
// Returns:
//   - An error wrapping the failing I/O step.
//
// Side effects:
//   - Creates destDir and any nested subdirectories.
//   - Creates or truncates files inside destDir.
func copySkillBundleFromDisk(srcDir, destDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walking skill bundle %q: %w", srcDir, err)
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return fmt.Errorf("relpath %q under %q: %w", path, srcDir, relErr)
		}
		target := filepath.Join(destDir, rel)
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("creating bundle subdir %q: %w", target, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyManifestFromDisk(path, target)
	})
}

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
	return seedSubdir(srcFS, "agents", ".md", destDir)
}

// SeedSwarmsDir copies all *.yml files from the source filesystem's
// "swarms" subdirectory into destDir. Files that already exist in
// destDir are skipped so user-edited swarm manifests survive a
// FlowState upgrade — exactly the contract SeedAgentsDir provides for
// agent manifests. The two seeders share their plumbing through
// seedSubdir below.
//
// Expected:
//   - srcFS is a valid fs.FS containing a "swarms" subdirectory with .yml files.
//   - destDir is a writable destination directory path (created if missing).
//
// Returns:
//   - An error if the source has no swarms directory or if file operations fail.
//   - nil on success.
//
// Side effects:
//   - Creates destDir if it does not exist.
//   - Copies each .yml file from srcFS to destDir only when the
//     destination file does not already exist.
func SeedSwarmsDir(srcFS fs.FS, destDir string) error {
	return seedSubdir(srcFS, "swarms", ".yml", destDir)
}

// SeedSchemasDir copies all *.json files from the source filesystem's
// "schemas" subdirectory into destDir. Existing schema files are
// preserved so operator overrides in XDG_CONFIG win over bundled
// defaults.
//
// Expected:
//   - srcFS is a valid fs.FS containing a "schemas" subdirectory with .json files.
//   - destDir is the directory where JSON Schemas should be copied.
//
// Returns:
//   - nil on success.
//   - An error if the source has no schemas directory or file operations fail.
//
// Side effects:
//   - Creates destDir when missing.
//   - Copies missing bundled schema files into destDir.
func SeedSchemasDir(srcFS fs.FS, destDir string) error {
	return seedSubdir(srcFS, "schemas", ".json", destDir)
}

// seedSubdir is the shared implementation behind SeedAgentsDir,
// SeedSwarmsDir, and SeedSchemasDir. Pulled out so the callers stay byte-equivalent
// in their semantics: skip-on-existing, log-friendly error wrapping,
// and matching directory-creation behaviour.
//
// Expected:
//   - srcFS is a valid fs.FS containing the named subdirectory.
//   - subdir is the literal directory name inside srcFS (e.g. "agents").
//   - ext is the file extension to copy, with the leading dot (e.g. ".md").
//   - destDir is a writable destination directory path.
//
// Returns:
//   - A wrapped error naming subdir on missing-source / IO failure.
//   - nil on success.
//
// Side effects:
//   - Creates destDir if it does not exist.
//   - Copies each matching file from srcFS to destDir only when the
//     destination file does not already exist.
func seedSubdir(srcFS fs.FS, subdir, ext, destDir string) error {
	sourceDir, err := fs.Sub(srcFS, subdir)
	if err != nil {
		return fmt.Errorf("%s directory not found in source: %w", subdir, err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	entries, err := fs.ReadDir(sourceDir, ".")
	if err != nil {
		return fmt.Errorf("reading source %s directory: %w", subdir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if filepath.Ext(filename) != ext {
			continue
		}

		destPath := filepath.Join(destDir, filename)

		if err := copySingleFile(sourceDir, filename, destPath); err != nil {
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

// SeedSkillsDir copies every embedded skill bundle from srcFS into
// destDir, preserving the bundle directory layout (one subdirectory
// per skill, each containing SKILL.md and optional sibling resources).
//
// Skills differ from agents (a flat .md tree) and swarms (a flat .yml
// tree): each skill is its own directory because the pattern allows
// for sibling references, examples and scripts to ship alongside the
// SKILL.md. SeedSkillsDir therefore walks srcFS recursively under the
// "skills" subtree, mirrors directory creation, and copies regular
// files via copySingleFile so the existing skip-on-existing semantics
// extend to skill bundles — user edits to SKILL.md (or to a sibling
// resource) survive a FlowState upgrade exactly the way they do for
// agent manifests.
//
// This seeder is the runtime bridge between the binary's bundled
// skills (//go:embed skills/*/SKILL.md in embed_skills.go) and the
// SkillDir that engine.LoadAlwaysActiveSkills walks on every prompt
// build. Without it, a fresh `flowstate` install resolves
// cfg.SkillDir to ~/.config/flowstate/skills/ — an empty directory —
// and FileSkillLoader.LoadAll silently returns the empty slice,
// stripping the four always-active skills (pre-action, discipline,
// skill-discovery, agent-discovery) from every agent prompt.
//
// Expected:
//   - srcFS is a valid fs.FS containing a "skills" subdirectory whose
//     entries are bundle directories with SKILL.md inside.
//   - destDir is a writable destination directory path (created if
//     missing).
//
// Returns:
//   - An error if the source has no skills directory, the destination
//     cannot be created, or any I/O step fails.
//   - nil on success, including when every bundle file already exists.
//
// Side effects:
//   - Creates destDir if it does not exist.
//   - Creates per-bundle subdirectories under destDir as needed.
//   - Copies each bundle file from srcFS to destDir only when the
//     destination file does not already exist (skip-on-existing).
func SeedSkillsDir(srcFS fs.FS, destDir string) error {
	skillsRoot, err := fs.Sub(srcFS, "skills")
	if err != nil {
		return fmt.Errorf("skills directory not found in source: %w", err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	return fs.WalkDir(skillsRoot, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walking embedded skills %q: %w", path, walkErr)
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(destDir, path)
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("creating bundle subdir %q: %w", target, err)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copySingleFile(skillsRoot, path, target)
	})
}

// SeedGatesDir copies every embedded v0 ext-gate bundle from srcFS
// into destDir, preserving the bundle directory layout (one
// subdirectory per gate, each containing manifest.yml plus an exec
// file like gate.py / gate.sh / gate binary).
//
// Gates differ from agents (a flat .md tree) and swarms (a flat .yml
// tree): each gate is its own directory because the manifest's exec
// field references a sibling executable that must ship alongside.
// SeedGatesDir therefore walks srcFS recursively under the "gates"
// subtree, mirrors directory creation, and copies regular files via
// copySingleFile so the existing skip-on-existing semantics extend to
// gate bundles — operator edits to manifest.yml (or to a sibling
// gate.py) survive a FlowState upgrade exactly the way they do for
// agent manifests.
//
// Executable bit propagation: copySingleFile creates files with 0644,
// which would leave gate.py / gate.sh non-executable on the
// destination. After copying every file in the bundle, SeedGatesDir
// reads the manifest.yml's exec entry and chmods that single file to
// 0755 so newSubprocessRunner's `executable bit set` check passes at
// app boot. The chmod is a no-op when the destination is already
// 0755 (skip-on-existing leaves operator edits alone) and produces a
// single warning rather than aborting boot when the manifest is
// malformed enough to omit `exec:`.
//
// This seeder is the runtime bridge between the binary's bundled
// gates (//go:embed gates/*/manifest.yml gates/*/gate.py in
// embed_gates.go) and the GatesDir that gates.Discover walks during
// app.New (via RegisterDiscoveredGates). Without it, a fresh
// `flowstate` install resolves cfg.GatesDir to ~/.config/flowstate/
// gates/ — an empty directory — and the swarm runner's `ext:*` kinds
// fail with "ext gate %q is not registered" the first time a member
// triggers a gate dispatch.
//
// Expected:
//   - srcFS is a valid fs.FS containing a "gates" subdirectory whose
//     entries are bundle directories with manifest.yml inside.
//   - destDir is a writable destination directory path (created if
//     missing).
//
// Returns:
//   - An error if the source has no gates directory, the destination
//     cannot be created, or any I/O step fails.
//   - nil on success, including when every bundle file already exists.
//
// Side effects:
//   - Creates destDir if it does not exist.
//   - Creates per-bundle subdirectories under destDir as needed.
//   - Copies each bundle file from srcFS to destDir only when the
//     destination file does not already exist (skip-on-existing).
//   - chmods the manifest's exec file to 0755 after copy.
func SeedGatesDir(srcFS fs.FS, destDir string) error {
	gatesRoot, err := fs.Sub(srcFS, "gates")
	if err != nil {
		return fmt.Errorf("gates directory not found in source: %w", err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	bundles := make(map[string]struct{})
	walkErr := fs.WalkDir(gatesRoot, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking embedded gates %q: %w", path, err)
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(destDir, path)
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("creating gate bundle subdir %q: %w", target, err)
			}
			bundles[path] = struct{}{}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copySingleFile(gatesRoot, path, target)
	})
	if walkErr != nil {
		return walkErr
	}

	for bundle := range bundles {
		if err := chmodGateExec(filepath.Join(destDir, bundle)); err != nil {
			slog.Warn("seeding gate exec bit", "bundle", bundle, "err", err)
		}
	}
	return nil
}

// chmodGateExec parses bundleDir/manifest.yml and chmods the file
// referenced by the manifest's `exec:` field to 0755. Used by
// SeedGatesDir after copySingleFile (which writes 0644) to restore the
// executable bit gate runners require. A missing or malformed manifest
// is reported via the returned error so the caller can warn-and-continue
// without aborting boot — a malformed seeded gate is still discoverable,
// it just fails at registration time rather than seed time.
//
// Expected:
//   - bundleDir is the seeded bundle's destination path containing
//     manifest.yml.
//
// Returns:
//   - nil when the exec file was found and chmodded successfully (or
//     was already executable).
//   - A wrapped error when the manifest cannot be read, parsed, or its
//     exec field is empty / points at a missing file.
//
// Side effects:
//   - Reads bundleDir/manifest.yml.
//   - chmods the resolved exec file to 0755.
func chmodGateExec(bundleDir string) error {
	manifestPath := filepath.Join(bundleDir, "manifest.yml")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", manifestPath, err)
	}
	exec := extractExecField(body)
	if exec == "" {
		return fmt.Errorf("manifest %s has no exec field", manifestPath)
	}
	execPath := exec
	if !filepath.IsAbs(execPath) {
		execPath = filepath.Join(bundleDir, execPath)
	}
	// Re-resolve through the bundle directory so an `exec:` value
	// containing path traversal (`../foo`) cannot escape the seeded
	// bundle. SeedGatesDir owns bundleDir, but a malformed manifest
	// shipped via go:embed should still be confined.
	if rel, err := filepath.Rel(bundleDir, execPath); err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("exec %s escapes bundle %s", execPath, bundleDir)
	}
	info, err := os.Stat(execPath) //nolint:gosec // execPath is confined to bundleDir by the Rel check above.
	if err != nil {
		return fmt.Errorf("stat exec %s: %w", execPath, err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		return nil
	}
	if err := os.Chmod(execPath, 0o755); err != nil { //nolint:gosec // execPath is confined to bundleDir by the Rel check above.
		return fmt.Errorf("chmod exec %s: %w", execPath, err)
	}
	return nil
}

// extractExecField pulls the `exec:` value out of a gate manifest.yml
// body without taking a yaml.v3 dependency at the seed layer (seed.go
// is otherwise stdlib-only). The caller validates non-empty; this
// helper just parses lines until it finds the first top-level `exec:`
// key. Quoted values have surrounding quotes stripped so
// `exec: "./gate.py"` and `exec: ./gate.py` resolve to the same path.
//
// Expected:
//   - body is a manifest.yml file's contents.
//
// Returns:
//   - The exec field's trimmed string value when present.
//   - An empty string when the field is missing or empty.
//
// Side effects:
//   - None.
func extractExecField(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Skip indented lines — exec must be a top-level key. Top-level
		// keys have no leading whitespace.
		if line != trimmed {
			continue
		}
		const prefix = "exec:"
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		value = strings.Trim(value, `"'`)
		return value
	}
	return ""
}
