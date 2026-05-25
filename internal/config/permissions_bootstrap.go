package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// defaultPermissionsYAML is the embedded version-1 permissions.yaml that
// ships with FlowState. The file lives at
// internal/config/defaults/permissions.yaml and uses the
// ${FLOWSTATE_VAULT_ROOT} placeholder so the bootstrap path can substitute
// the operator's real vault directory at first-run write time.
//
//go:embed defaults/permissions.yaml
var defaultPermissionsYAML string

// vaultRootPlaceholder is the literal token in the embedded default that
// the bootstrap path swaps for the operator's resolved vault path. Kept
// as a named constant so tests and the substitution helper stay in sync
// with the embedded fixture.
const vaultRootPlaceholder = "${FLOWSTATE_VAULT_ROOT}"

// permissionsFilename is the basename the bootstrap path materialises
// under the XDG config directory (or any caller-supplied directory).
// Kept as a constant so the bootstrap, the buildPathGuard caller, and
// any future operator tooling pick up the same name.
const permissionsFilename = "permissions.yaml"

// DefaultPermissionsYAML returns the embedded default permissions.yaml
// content with the ${FLOWSTATE_VAULT_ROOT} placeholder substituted for
// vaultPath. An empty vaultPath leaves the placeholder intact so the
// caller can surface a warning and either write the file untouched or
// keep the placeholder in-memory.
//
// Returns:
//   - The default permissions YAML, with substitutions applied when
//     vaultPath is non-empty.
//
// Side effects:
//   - None.
func DefaultPermissionsYAML(vaultPath string) string {
	if vaultPath == "" {
		return defaultPermissionsYAML
	}
	return strings.ReplaceAll(defaultPermissionsYAML, vaultRootPlaceholder, vaultPath)
}

// EnsurePermissionsFile materialises permissions.yaml under dir on first
// run. It is the entry point invoked from the config bootstrap path
// once the XDG config directory is known.
//
// Behaviour:
//   - File already exists → no-op (respects operator customisation).
//   - File missing AND dir writable → writes the embedded default with
//     ${FLOWSTATE_VAULT_ROOT} substituted to vaultPath. Empty vaultPath
//     logs a warning and writes the file with the placeholder intact.
//   - File missing AND dir NOT writable → logs a warning and returns
//     nil so the caller can fall back to the in-memory embedded default
//     via LoadDefaultPermissions().
//
// Expected:
//   - dir is the XDG config directory (e.g. ~/.config/flowstate).
//   - vaultPath is the operator's resolved AppConfig.VaultPath, or "".
//
// Returns:
//   - nil on every supported outcome (no-op, write success, unwritable
//     dir). A non-nil error is returned only on unexpected filesystem
//     failures during the temp+rename write — never on missing dir or
//     permission-denied, which are downgraded to warnings.
//
// Side effects:
//   - Creates <dir>/permissions.yaml via temp+rename when the file is
//     missing and the directory is writable.
//   - Emits slog warnings on the unwritable-dir and empty-vaultPath
//     paths so operators see the silent fall-back.
func EnsurePermissionsFile(dir, vaultPath string) error {
	if dir == "" {
		return nil
	}

	target := filepath.Join(dir, permissionsFilename)
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat permissions file %q: %w", target, err)
	}

	if !isDirWritable(dir) {
		slog.Warn("permissions.yaml directory is not writable — using embedded default in memory",
			"dir", dir,
		)
		return nil
	}

	if vaultPath == "" {
		slog.Warn("vault_path is empty — writing permissions.yaml with ${FLOWSTATE_VAULT_ROOT} placeholder intact",
			"path", target,
		)
	}

	content := DefaultPermissionsYAML(vaultPath)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write permissions file %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename permissions file %q -> %q: %w", tmp, target, err)
	}
	return nil
}

// LoadDefaultPermissions parses the embedded default permissions.yaml
// with ${FLOWSTATE_VAULT_ROOT} substituted to vaultPath. It is the
// fallback the bootstrap path uses when the XDG directory is not
// writable and no on-disk file is available.
//
// Returns:
//   - A parsed *Permissions on success. Never returns nil without an
//     error — the embedded fixture is guaranteed-parseable at build
//     time, so any parse failure here indicates a regression.
//
// Side effects:
//   - None.
func LoadDefaultPermissions(vaultPath string) (*Permissions, error) {
	return parsePermissionsBytes([]byte(DefaultPermissionsYAML(vaultPath)))
}

// isDirWritable returns true when the process can create files under
// dir. It probes by attempting to create and immediately remove a
// hidden temp file; this catches both "directory does not exist" and
// "permission denied" without relying on the unreliable os.FileMode
// bits across platforms.
//
// Returns:
//   - true when a probe file was created successfully.
//   - false on any error (missing dir, EACCES, read-only filesystem).
//
// Side effects:
//   - Creates and removes a temporary probe file under dir on success.
func isDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".permissions-bootstrap-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
