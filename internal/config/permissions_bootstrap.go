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

// xdgDataHomePlaceholder is the literal token in the embedded default
// that the bootstrap path swaps for the operator's resolved XDG_DATA_HOME
// directory. Used to root the default plan_output_dir at
// ${XDG_DATA_HOME}/flowstate/plans without hardcoding a path in the
// embedded YAML. Resolved by ResolveXDGDataHome() so tests can pin a
// deterministic value via XDG_DATA_HOME.
const xdgDataHomePlaceholder = "${XDG_DATA_HOME_PLACEHOLDER}"

// permissionsFilename is the basename the bootstrap path materialises
// under the XDG config directory (or any caller-supplied directory).
// Kept as a constant so the bootstrap, the buildPathGuard caller, and
// any future operator tooling pick up the same name.
const permissionsFilename = "permissions.yaml"

// ResolveXDGDataHome returns the operator's XDG_DATA_HOME directory,
// falling back to ${HOME}/.local/share per the XDG Base Directory
// Specification when the env var is unset or empty.
//
// Returns:
//   - The absolute XDG data directory path.
//   - The empty string if neither XDG_DATA_HOME nor HOME is resolvable
//     (i.e. a fully sandboxed env with neither set) — callers MUST
//     check for this and skip the plan_output_dir mkdir step rather
//     than passing "" downstream.
//
// Side effects:
//   - None.
func ResolveXDGDataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}

// DefaultPermissionsYAML returns the embedded default permissions.yaml
// content with the ${FLOWSTATE_VAULT_ROOT} placeholder substituted for
// vaultPath. An empty vaultPath leaves the placeholder intact so the
// caller can surface a warning and either write the file untouched or
// keep the placeholder in-memory.
//
// The ${XDG_DATA_HOME_PLACEHOLDER} token is substituted via
// ResolveXDGDataHome(); when that helper returns empty (no XDG_DATA_HOME
// and no HOME) the placeholder is left intact so callers can detect the
// unresolved state.
//
// Returns:
//   - The default permissions YAML, with substitutions applied when
//     the relevant inputs are non-empty.
//
// Side effects:
//   - Reads XDG_DATA_HOME / HOME via os.Getenv / os.UserHomeDir.
func DefaultPermissionsYAML(vaultPath string) string {
	content := defaultPermissionsYAML
	if vaultPath != "" {
		content = strings.ReplaceAll(content, vaultRootPlaceholder, vaultPath)
	}
	if xdgData := ResolveXDGDataHome(); xdgData != "" {
		content = strings.ReplaceAll(content, xdgDataHomePlaceholder, xdgData)
	}
	return content
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

	// Plan-Mode Output Directory plan §3 Slice 1: materialise the
	// resolved plan_output_dir on first run so the agent's first
	// Plan-mode write does not race a missing directory.
	//
	// Failure-handling stance: mkdir failures (read-only FS,
	// permission denied) are downgraded to slog warnings rather than
	// surfacing as a bootstrap error. The bootstrap path already
	// uses the same downgrade for the parent dir's unwritable case
	// (above), and propagating the failure here would block boot
	// entirely. The Plan-mode write attempt itself will surface the
	// missing directory as an explicit pathguard denial — agents see
	// a "path not under plan_output_dir" rejection rather than a
	// silent succeed. Per the loud-disclosure stance the slog warning
	// here is the operator-side surface that the dir was not
	// provisionable.
	if resolved, err := resolvePlanOutputDirFromYAML(content); err == nil && resolved != "" {
		if mkErr := os.MkdirAll(resolved, 0o700); mkErr != nil {
			slog.Warn("plan_output_dir is not writable — Plan-mode file mutations will be denied",
				"path", resolved,
				"error", mkErr,
			)
		}
	}
	return nil
}

// resolvePlanOutputDirFromYAML extracts the plan_output_dir value from
// the (already-substituted) permissions.yaml content. It re-parses
// rather than receiving the value via a side channel so the bootstrap
// cannot drift away from the operator-visible YAML — whatever ends up
// in the file is exactly what gets mkdir'd.
//
// Returns:
//   - The resolved plan_output_dir, empty when unset OR when the YAML
//     still carries the placeholder (caller skips mkdir in that case).
//   - A non-nil error only on a YAML parse failure; missing field is
//     not an error.
//
// Side effects:
//   - None.
func resolvePlanOutputDirFromYAML(content string) (string, error) {
	perms, err := parsePermissionsBytes([]byte(content))
	if err != nil {
		return "", err
	}
	if perms == nil {
		return "", nil
	}
	if strings.Contains(perms.PlanOutputDir, xdgDataHomePlaceholder) {
		// Unresolved placeholder — neither XDG_DATA_HOME nor HOME is
		// set. Skip mkdir; the empty effective value will surface as
		// a Plan-mode denial at write time.
		return "", nil
	}
	return perms.PlanOutputDir, nil
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
