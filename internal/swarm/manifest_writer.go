package swarm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// manifestFileExtension is the on-disk extension every persisted swarm
// manifest uses. The loader scans for both `.yml` and `.yaml` but
// authoring goes through `.yml` so the file the wizard writes lines up
// with the file the loader picks up first.
const manifestFileExtension = ".yml"

// manifestDirPerm is the permission mode applied when the writer has to
// create the manifests directory. 0o755 mirrors the rest of the user
// config tree (see internal/config) so a fresh install ends up with a
// uniform layout.
const manifestDirPerm os.FileMode = 0o755

// manifestFilePerm is the permission mode applied to the manifest file
// itself. 0o644 keeps the contract consistent with what `flowstate
// swarm validate` and the loader expect (world-readable, owner-writable).
const manifestFilePerm os.FileMode = 0o644

// ErrManifestWriterNoDir is returned when the writer is invoked without
// a directory configured. Callers use errors.Is to distinguish
// configuration mistakes from filesystem failures.
var ErrManifestWriterNoDir = errors.New("swarm manifest writer: directory is not configured")

// ManifestWriter persists swarm manifests to disk under a configured
// directory. It is the symmetric counterpart to package-level Load /
// LoadDir: read-side helpers stay free functions, write-side state
// (the destination directory) lives on the type so callers can build
// one once at app startup and inject it into every authoring path
// (TUI wizard, future CLI subcommand, future API endpoint).
//
// Persisting a domain object is core business logic and belongs in
// the domain package. The TUI wizard at
// internal/tui/intents/chat/slashcommand/swarm_builder.go used to
// inline os.MkdirAll / os.WriteFile / os.Remove against the same
// `<dir>/<name>.yml` shape — that direct filesystem coupling is
// what this type retires. See the Multi-Access Method ADR's
// "Wrappers not duplicates" rule for the architectural rationale.
type ManifestWriter struct {
	// dir is the directory new manifests are written into. Empty is
	// treated as a configuration error; every public method short-
	// circuits with ErrManifestWriterNoDir before touching the
	// filesystem so an unconfigured writer never silently writes to
	// the process's working directory.
	dir string
}

// NewManifestWriter returns a writer rooted at dir. The constructor
// does not touch the filesystem; the directory is created lazily on
// the first successful Write so callers can probe / construct without
// side effects.
//
// Expected:
//   - dir is the directory swarm manifests should be persisted under.
//     Empty is allowed (Write / Delete will then return
//     ErrManifestWriterNoDir) so the wizard can construct a writer
//     during early bootstrap before the config has been resolved.
//
// Returns:
//   - A configured *ManifestWriter.
//
// Side effects:
//   - None.
func NewManifestWriter(dir string) *ManifestWriter {
	return &ManifestWriter{dir: dir}
}

// Dir returns the directory the writer is configured to persist into.
// Used by callers that need to surface the path in user-facing
// completion messages (the wizard's "Wrote swarm manifest to ..."
// blurb is the canonical example).
//
// Returns:
//   - The configured directory; may be empty when the writer was
//     constructed without one.
//
// Side effects:
//   - None.
func (w *ManifestWriter) Dir() string {
	return w.dir
}

// Path returns the absolute filesystem path Write would target for
// name. Useful for callers that need the path before deciding
// whether to call Write (e.g. the wizard's overwrite-confirmation
// branch stat()s the path before prompting). No filesystem access.
//
// Expected:
//   - name is a swarm id (no extension).
//
// Returns:
//   - The path `<dir>/<name>.yml`. When the writer's dir is empty the
//     path is `<name>.yml` so callers can still compare strings; the
//     path is NOT meaningful for I/O until the writer is fully
//     configured.
//
// Side effects:
//   - None.
func (w *ManifestWriter) Path(name string) string {
	return filepath.Join(w.dir, name+manifestFileExtension)
}

// Write serialises m to YAML and persists it under
// `<dir>/<name>.yml`, creating the directory if missing. Existing
// files at the same path are overwritten — overwrite confirmation is
// the caller's responsibility (the TUI wizard implements it with a
// stat probe before calling Write).
//
// Expected:
//   - name is a non-empty swarm id (no extension, no path
//     separators); the wizard sanitises this before calling.
//   - m is a non-nil Manifest pointer. Validation is the caller's
//     responsibility: Write does not invoke m.Validate so the wizard
//     can persist a partially-authored draft if it ever needs to
//     (today it always builds a complete Manifest).
//
// Returns:
//   - nil on a successful write.
//   - ErrManifestWriterNoDir wrapped with context when the writer
//     has no directory configured.
//   - A wrapped filesystem or yaml.Marshal error otherwise.
//
// Side effects:
//   - Creates the configured directory tree if missing (0o755).
//   - Writes the YAML file (0o644).
func (w *ManifestWriter) Write(name string, m *Manifest) error {
	if w.dir == "" {
		return fmt.Errorf("write swarm manifest: %w", ErrManifestWriterNoDir)
	}
	if name == "" {
		return errors.New("write swarm manifest: name is required")
	}
	if m == nil {
		return errors.New("write swarm manifest: manifest is nil")
	}

	if err := os.MkdirAll(w.dir, manifestDirPerm); err != nil {
		return fmt.Errorf("create swarm directory %q: %w", w.dir, err)
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal swarm manifest %q: %w", name, err)
	}

	path := w.Path(name)
	if err := os.WriteFile(path, out, manifestFilePerm); err != nil {
		return fmt.Errorf("write swarm manifest %q: %w", path, err)
	}
	return nil
}

// Delete removes the manifest file at `<dir>/<name>.yml`. The
// operation is idempotent: a missing file is not an error so callers
// driving rollback paths (the wizard's Cancel after a partial write)
// can call Delete unconditionally without first stat()ing the path.
//
// Expected:
//   - name is a non-empty swarm id (no extension).
//
// Returns:
//   - nil on success or when the file was already absent.
//   - ErrManifestWriterNoDir wrapped with context when the writer
//     has no directory configured.
//   - A wrapped filesystem error otherwise.
//
// Side effects:
//   - Removes the manifest file when present.
func (w *ManifestWriter) Delete(name string) error {
	if w.dir == "" {
		return fmt.Errorf("delete swarm manifest: %w", ErrManifestWriterNoDir)
	}
	if name == "" {
		return errors.New("delete swarm manifest: name is required")
	}

	path := w.Path(name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete swarm manifest %q: %w", path, err)
	}
	return nil
}
