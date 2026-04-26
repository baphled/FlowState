package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads a single swarm manifest YAML file, unmarshals it onto a
// Manifest value, and runs Validate against the no-op validator so
// scalar / self-reference / gate-prefix rules fire even before any
// registry is available. Registry-aware re-validation runs again
// inside NewRegistryFromDir.
//
// Returns the parsed manifest on success or a wrapped error
// identifying the path on read / parse / validate failure.
//
// Expected:
//   - path is a filesystem path to a .yml or .yaml swarm manifest.
//
// Returns:
//   - The parsed *Manifest on success.
//   - A wrapped error naming path when read, parse, or validate fail.
//
// Side effects:
//   - Reads path from the filesystem.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- swarm manifests are user config files chosen by the loader
	if err != nil {
		return nil, fmt.Errorf("reading swarm manifest %q: %w", path, err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing swarm manifest %q: %w", path, err)
	}

	if err := m.Validate(nil); err != nil {
		return nil, fmt.Errorf("validating swarm manifest %q: %w", path, err)
	}

	return &m, nil
}

// LoadDir walks a directory for *.yml / *.yaml swarm manifests and
// loads each one. The result is sorted by manifest id so the caller
// (and the registry) sees a deterministic order regardless of the
// underlying filesystem walk order. Per-file failures are aggregated
// so a single malformed manifest does not mask the rest.
//
// The "directory does not exist" case returns a wrapped error so
// callers can distinguish it from "directory exists but has no
// manifests" (the latter returns a nil slice with no error).
//
// Expected:
//   - dir is a filesystem path.
//
// Returns:
//   - The slice of successfully-loaded manifests sorted by id.
//   - A wrapped error aggregating per-file failures or naming the
//     directory when stat / glob fails. Manifests + a non-nil error
//     can co-occur when some files load and some fail.
//
// Side effects:
//   - Reads the directory and every matching YAML file.
func LoadDir(dir string) ([]*Manifest, error) {
	cleanDir := filepath.Clean(dir)
	info, err := os.Stat(cleanDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("swarm directory %q does not exist: %w", cleanDir, err)
		}
		return nil, fmt.Errorf("stat swarm directory %q: %w", cleanDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("swarm directory %q is not a directory", cleanDir)
	}

	paths, err := discoverSwarmManifestPaths(cleanDir)
	if err != nil {
		return nil, err
	}

	var (
		manifests []*Manifest
		errs      []string
	)
	for _, path := range paths {
		m, err := Load(path)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		manifests = append(manifests, m)
	}

	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })

	if len(errs) > 0 {
		return manifests, fmt.Errorf("swarm manifest load failures:\n%s", strings.Join(errs, "\n"))
	}
	return manifests, nil
}

// discoverSwarmManifestPaths finds all *.yml and *.yaml files in the
// directory and returns them in sorted order. Kept private so the
// public API is only Load / LoadDir; the registry constructor calls
// LoadDir, never this helper directly.
//
// Expected:
//   - dir is a filesystem path.
//
// Returns:
//   - A new slice of matching file paths in lexicographic order.
//   - A wrapped error when the underlying glob calls fail.
//
// Side effects:
//   - Reads the directory contents via filepath.Glob.
func discoverSwarmManifestPaths(dir string) ([]string, error) {
	ymlMatches, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, fmt.Errorf("glob *.yml in %q: %w", dir, err)
	}
	yamlMatches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob *.yaml in %q: %w", dir, err)
	}
	combined := append(ymlMatches, yamlMatches...) //nolint:gocritic // appendAssign is acceptable here; we want a fresh slice
	sort.Strings(combined)
	return combined, nil
}
