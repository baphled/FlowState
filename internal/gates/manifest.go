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
	// Inputs declares the coord-store keys the host should pack into a
	// JSON object before forwarding to the gate's Payload. Each entry is
	// a logical name (the JSON key the gate sees) plus the member id and
	// output_key the host reads from the coordination store. The special
	// member token "${target}" resolves to the dispatching gate's Target
	// at composition time so a manifest can stay member-agnostic where
	// useful. An empty Inputs slice preserves the legacy single-key
	// payload shape (the gate sees the raw bytes of the dispatching
	// member's "<target>/<output_key>" coord-store value).
	Inputs []InputSpec `yaml:"inputs"`
	// Dir is the absolute parent directory of the loaded manifest.yml.
	// Set by LoadManifest; never serialised.
	Dir string `yaml:"-"`
}

// InputSpec is one entry in Manifest.Inputs. All three fields are
// required at load time; the validator rejects empty values so the
// composition path never has to invent a coord-store key.
type InputSpec struct {
	// Name is the JSON object key the host writes the value under in
	// the composed payload. Logical names are not member-coupled —
	// "task_plan" is a contract between the manifest author and the
	// gate executable, independent of which agent actually produced it.
	Name string `yaml:"name"`
	// Member is the agent id whose coord-store sub-tree the host reads
	// from. The literal token "${target}" resolves to the dispatching
	// gate's Target so manifests can declare "the member this gate
	// fires around" without naming them.
	Member string `yaml:"member"`
	// OutputKey is the coord-store sub-key under that member. The full
	// lookup is "<chainPrefix>/<member>/<output_key>".
	OutputKey string `yaml:"output_key"`
}

// TargetPlaceholder is the literal token that, when found in
// InputSpec.Member, resolves at dispatch time to the dispatching gate's
// Target. Exported so the swarm composition path can use the same
// string the manifest validator accepts and tests can pin the contract.
const TargetPlaceholder = "${target}"

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
	if err := validateInputs(m.Inputs); err != nil {
		return err
	}
	return nil
}

// validateInputs enforces per-entry presence rules and rejects
// duplicate logical names. The validator runs at manifest load time so
// a malformed inputs block fails boot rather than silently producing a
// broken payload at first dispatch.
//
// Expected:
//   - inputs may be nil or empty; both pass.
//
// Returns:
//   - nil when every entry has non-empty name, member, output_key and
//     no two entries share a name.
//   - A descriptive error annotated with the offending index/field
//     otherwise. Field paths follow the "inputs[<i>].<field>" shape so
//     operator log readers can locate the offender quickly.
//
// Side effects:
//   - None.
func validateInputs(inputs []InputSpec) error {
	seen := make(map[string]struct{}, len(inputs))
	for i, in := range inputs {
		if strings.TrimSpace(in.Name) == "" {
			return fmt.Errorf("inputs[%d].name: required", i)
		}
		if strings.TrimSpace(in.Member) == "" {
			return fmt.Errorf("inputs[%d].member: required (input %q)", i, in.Name)
		}
		if strings.TrimSpace(in.OutputKey) == "" {
			return fmt.Errorf("inputs[%d].output_key: required (input %q)", i, in.Name)
		}
		if _, dup := seen[in.Name]; dup {
			return fmt.Errorf("inputs[%d]: duplicate name %q", i, in.Name)
		}
		seen[in.Name] = struct{}{}
	}
	return nil
}
