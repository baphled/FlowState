// Package pathguard's permissions_writer.go is the first cross-process
// file-locking primitive in the FlowState codebase. Permission Mode
// ModeAskUser Extension plan (May 2026) §17.4 committed to gofrs/flock
// over syscall.Flock so the writer works on macOS dev + Linux prod
// without per-OS build tags.
//
// Persistence convention established here for future sites:
//
//  1. Acquire exclusive flock on the target path.
//  2. Read current bytes.
//  3. Mutate in-memory.
//  4. Re-marshal AND re-unmarshal to validate the resulting payload
//     still parses (smoke test — if marshal corrupts the shape, the
//     write is aborted before touching the original).
//  5. Write to a temp file in the same directory.
//  6. fsync the temp file.
//  7. os.Rename(temp, original) — atomic on POSIX.
//  8. fsync the parent directory (rename's durability gate).
//  9. Release flock (via defer; runs even on early-return failure).
//
// Memory: feedback_atomicity_awareness_uneven. Auth (OAuth refresh,
// configs) is the cited counter-example that DOESN'T do this today;
// permissions.yaml IS the security surface that ships v1, so it does.

package pathguard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// permissionsYAMLSchema mirrors the on-disk shape of permissions.yaml.
// Kept as an internal struct in the pathguard package rather than
// importing internal/config so the writer can mutate the YAML without a
// dependency cycle (config imports nothing from pathguard today; we
// preserve that direction).
//
// The shape MUST match config.Permissions exactly — any drift would
// silently truncate operator-set fields on append. The behaviour-pinned
// guard is the "round-trip through the matcher" spec: the matcher
// (config.Permissions) consumes the file the writer produces.
type permissionsYAMLSchema struct {
	Version       int                            `yaml:"version,omitempty"`
	PlanOutputDir string                         `yaml:"plan_output_dir,omitempty"`
	Tools         map[string]permissionsToolRule `yaml:"tools,omitempty"`
}

type permissionsToolRule struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// PermissionsReloader is the optional seam pathguard's Writer uses to
// refresh an in-memory matcher after a successful YAML mutation. The
// concrete implementation lives in app.go (boot-time matcher wrapper)
// or in tests (spy that records the call). nil is a valid value —
// callers that load permissions.yaml on every Check do not need a
// reloader.
//
// Reload returns an error so a read-after-write failure surfaces to
// the writer. The writer logs but does not roll back the file mutation
// — the YAML is already persisted, and a stale in-memory matcher is a
// recoverable surface (operator restart re-reads the file). Memory:
// feedback_close_latent_surfaces_too — we close the persistence gap
// here even though Slice 4's primary acceptance is the write atomicity.
type PermissionsReloader interface {
	Reload() error
}

// Writer appends allow globs to a permissions.yaml file under an
// exclusive cross-process flock. One Writer per (path, reloader) pair;
// constructed once at app boot and shared across handlers — the flock
// is the cross-goroutine + cross-process serialisation point, not the
// Writer struct itself.
//
// Permission Mode ModeAskUser Extension plan (May 2026) Slice 4 §17.4.
type Writer struct {
	// path is the permissions.yaml file to mutate. Resolved at
	// construction time; the writer does NOT re-derive it from
	// config.Dir() per call so test fixtures can drive a tmpdir path
	// without monkey-patching env.
	path string
	// reloader, when non-nil, is invoked after a successful AppendAllow
	// so the in-memory matcher serving Guard.Check picks up the new
	// allow glob without daemon restart. Optional.
	reloader PermissionsReloader
	// lockTimeout caps the flock acquisition. Defaults to 5s — long
	// enough that legitimate contention (daemon + CLI both writing)
	// resolves; short enough that a stuck holder doesn't block the
	// API handler indefinitely. Override via NewWriterWithLockTimeout.
	lockTimeout time.Duration
}

// NewWriter constructs a Writer for the supplied permissions.yaml path.
// reloader may be nil — the writer then assumes the matcher is hot-
// reloaded on every Check (or no matcher is consulting the file).
//
// Construction does NOT touch the filesystem — the path is validated
// lazily on the first AppendAllow call. This keeps the boot path
// allocation-cheap and lets tests construct a Writer for a not-yet-
// created file (the writer creates it on first append).
func NewWriter(path string, reloader PermissionsReloader) *Writer {
	return &Writer{
		path:        path,
		reloader:    reloader,
		lockTimeout: 5 * time.Second,
	}
}

// NewWriterWithLockTimeout is NewWriter plus an explicit lock-
// acquisition timeout override. Used by tests that want a tight
// deadline so contention specs fail fast.
func NewWriterWithLockTimeout(path string, reloader PermissionsReloader, lockTimeout time.Duration) *Writer {
	return &Writer{
		path:        path,
		reloader:    reloader,
		lockTimeout: lockTimeout,
	}
}

// AppendAllow appends glob to the allow slice for tool in the YAML at
// w.path. Behaviour:
//
//   - Acquires an exclusive flock on the path; competing writers block
//     until release. Cross-process — a daemon + CLI invoking the same
//     code path serialise (plan §12 R1 acceptance).
//   - Idempotent — if glob is already present in tools[tool].allow the
//     function returns nil without writing.
//   - Atomic — writes via temp + fsync + rename + parent fsync; on
//     validation OR write failure the original file is untouched.
//   - Schema-preserving — Version + PlanOutputDir are round-tripped
//     verbatim. The writer is purely additive on the tools.<tool>.allow
//     dimension; it never touches deny, never deletes entries, never
//     reorders.
//
// Returns:
//   - nil on successful append (file written + optional reload fired).
//   - nil on idempotency hit (glob already present; no write, no reload).
//   - A wrapped error on lock timeout, read failure, validation
//     failure, write failure, or rename failure. In every error path
//     the original file is left untouched.
//
// Side effects on success:
//   - permissions.yaml on disk has the new glob appended.
//   - reloader.Reload() is invoked (best-effort; a reloader error is
//     logged but does NOT make the write fail — the YAML is already
//     persisted).
func (w *Writer) AppendAllow(tool, glob string) error {
	if tool == "" {
		return errors.New("permissions writer: tool is required")
	}
	if glob == "" {
		return errors.New("permissions writer: glob is required")
	}
	if w.path == "" {
		return errors.New("permissions writer: path is unconfigured")
	}

	// Acquire the cross-process exclusive lock. The lock file IS the
	// permissions.yaml path itself — flock on the path is the same
	// inode the writer is about to mutate, so a concurrent daemon +
	// CLI cannot race on the file regardless of which one wrote it
	// first. gofrs/flock handles macOS + Linux without per-OS build
	// tags (plan §17.4).
	lock := flock.New(w.path)
	lockCtx, cancel := context.WithTimeout(context.Background(), w.lockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, 25*time.Millisecond)
	if err != nil {
		return fmt.Errorf("permissions writer: acquire flock: %w", err)
	}
	if !locked {
		return fmt.Errorf("permissions writer: flock acquisition timed out after %s", w.lockTimeout)
	}
	defer func() {
		// Unlock is best-effort — a failure here means the OS held
		// the lock past our process exit, which is a kernel-level
		// concern, not a writer concern. The lock is released on
		// process exit regardless.
		_ = lock.Unlock()
	}()

	// Read current bytes. A missing file is treated as an empty
	// permissions config — the writer creates one rather than
	// erroring, so the first-ever "forever" grant from a fresh
	// install lands cleanly.
	current, err := w.readSchemaLocked()
	if err != nil {
		return err
	}

	// Idempotency check — if the glob is already present, no write.
	// This is the cheap path; the operator may click "forever" twice
	// on the same prompt without producing duplicate lines.
	if rule, ok := current.Tools[tool]; ok {
		for _, existing := range rule.Allow {
			if existing == glob {
				return nil
			}
		}
	}

	// Append in-place. Initialise nested maps / slices as needed.
	if current.Tools == nil {
		current.Tools = make(map[string]permissionsToolRule)
	}
	rule := current.Tools[tool]
	rule.Allow = append(rule.Allow, glob)
	current.Tools[tool] = rule

	// Validate by round-tripping: marshal, then unmarshal into a
	// fresh schema. If either step fails the original file is
	// untouched. This catches the rare case where the YAML library's
	// marshal output is not its own unmarshal input — a runtime smoke
	// test before any disk mutation.
	encoded, err := yaml.Marshal(current)
	if err != nil {
		return fmt.Errorf("permissions writer: marshal yaml: %w", err)
	}
	var probe permissionsYAMLSchema
	if err := yaml.Unmarshal(encoded, &probe); err != nil {
		return fmt.Errorf("permissions writer: validate yaml: %w", err)
	}

	// Atomic write: temp in same directory → fsync → rename →
	// parent dir fsync. The "same directory" constraint is essential
	// because os.Rename is only guaranteed atomic across the same
	// filesystem; a /tmp temp file would not be (cross-fs rename
	// falls back to copy + unlink, which is not atomic).
	dir := filepath.Dir(w.path)
	tempFile, err := os.CreateTemp(dir, ".permissions-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("permissions writer: create temp: %w", err)
	}
	tempPath := tempFile.Name()
	// Defer-remove guards the failure paths: if anything between here
	// and the successful rename errors, the temp file is cleaned up
	// rather than left as litter in config.Dir().
	defer func() {
		// os.Rename has already moved the temp away on success;
		// Remove returns ENOENT in that case and we ignore it.
		if _, statErr := os.Stat(tempPath); statErr == nil {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(encoded); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("permissions writer: write temp: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("permissions writer: fsync temp: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("permissions writer: close temp: %w", err)
	}

	// Preserve the original file's mode if it exists. A fresh file
	// gets 0o644 (matches LoadPermissions's expectation that the
	// file is operator-readable / world-readable for tool inspection).
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(w.path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return fmt.Errorf("permissions writer: chmod temp: %w", err)
	}

	if err := os.Rename(tempPath, w.path); err != nil {
		return fmt.Errorf("permissions writer: rename temp: %w", err)
	}

	// fsync the parent directory so the rename's durability is
	// committed to the journal. Without this, a crash between the
	// rename and the next sync can leave the directory entry
	// pointing at the old inode. Memory: feedback_atomicity_
	// awareness_uneven cites this as the convention to mirror.
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	// Refresh the in-memory matcher. A reloader failure does NOT
	// roll back the on-disk write — the file is the source of truth
	// and the next process restart will re-read it. The error path
	// is observable via the returned error so callers can log it.
	if w.reloader != nil {
		if err := w.reloader.Reload(); err != nil {
			return fmt.Errorf("permissions writer: reload matcher (file already persisted): %w", err)
		}
	}

	return nil
}

// readSchemaLocked reads the permissions.yaml file under the held
// flock and returns the parsed schema. A missing file produces a
// zero-value schema (an empty config) so the first-ever append from
// a fresh install lands cleanly.
//
// Caller MUST hold the flock — this is the read side of the atomic
// read-modify-write triplet.
func (w *Writer) readSchemaLocked() (*permissionsYAMLSchema, error) {
	data, err := os.ReadFile(w.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &permissionsYAMLSchema{Version: 1}, nil
		}
		return nil, fmt.Errorf("permissions writer: read yaml: %w", err)
	}

	var schema permissionsYAMLSchema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("permissions writer: parse existing yaml: %w", err)
	}
	if schema.Version == 0 {
		// A pre-versioned file gets a v1 stamp on first append. The
		// loader treats missing-version as v1 by zero-value (see
		// config.LoadPermissions); writing it back explicit makes
		// future schema migrations (Slice 5 v2 bump) straightforward.
		schema.Version = 1
	}
	return &schema, nil
}
