// Package gates implements v0 ext-gate manifest loading and on-disk
// discovery. Each gate is a directory under the configured gates_dir
// containing manifest.yml plus an executable; the manifest declares
// the gate's name, its exec path, a per-dispatch timeout, and an
// optional default policy struct forwarded as request.policy.
package gates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Manifest is one parsed gate manifest. Dir is populated by LoadManifest
// from the manifest's on-disk location and used to resolve relative
// exec paths.
type Manifest struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version"`
	Exec        string         `yaml:"exec"`
	Timeout     time.Duration  `yaml:"timeout"`
	Policy      map[string]any `yaml:"policy"`
	// Dir is the absolute parent directory of the loaded manifest.yml.
	// Set by LoadManifest; never serialised.
	Dir string `yaml:"-"`
}

// LoadManifest parses the YAML at path, validates it, and applies
// per-field defaults. The returned Manifest has Dir populated to the
// parent directory of path so AbsoluteExecPath can resolve relative
// exec entries.
func LoadManifest(path string) (Manifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve manifest dir for %s: %w", path, err)
	}
	m.Dir = abs
	applyManifestDefaults(&m)
	if err := validateManifest(&m); err != nil {
		return Manifest{}, fmt.Errorf("manifest %s: %w", path, err)
	}
	return m, nil
}

// AbsoluteExecPath returns the gate's executable resolved against its
// manifest directory. An absolute Exec is returned verbatim; a
// relative Exec joins onto Dir.
func (m Manifest) AbsoluteExecPath() string {
	if filepath.IsAbs(m.Exec) {
		return m.Exec
	}
	return filepath.Join(m.Dir, m.Exec)
}

// applyManifestDefaults sets Timeout=30s when zero and leaves every
// other field as-loaded.
func applyManifestDefaults(m *Manifest) {
	if m.Timeout == 0 {
		m.Timeout = 30 * time.Second
	}
}

// validateManifest enforces Name and Exec presence and a non-negative
// Timeout. Returns the first failure as an error.
func validateManifest(m *Manifest) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("name: required")
	}
	if strings.TrimSpace(m.Exec) == "" {
		return fmt.Errorf("exec: required")
	}
	if m.Timeout < 0 {
		return fmt.Errorf("timeout: must be non-negative (got %s)", m.Timeout)
	}
	return nil
}
