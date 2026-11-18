package swarm

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadManifest reads and validates one YAML swarm manifest from
// path. Manifests are data files authored by operators; loading
// performs no execution.
func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Manifest{}, fmt.Errorf("swarm: read manifest %q: %w", path, err)
	}
	return ParseManifest(raw)
}

// ParseManifest decodes and validates one YAML manifest body.
func ParseManifest(raw []byte) (Manifest, error) {
	var doc manifestDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Manifest{}, fmt.Errorf("swarm: parse manifest: %w", err)
	}
	m := Manifest{Name: doc.Name, Scope: doc.Scope, Gates: docGates(doc.Gates)}
	for _, p := range doc.Packets {
		m.Packets = append(m.Packets, Packet(p))
	}
	if err := ValidateManifest(m); err != nil {
		return Manifest{}, fmt.Errorf("swarm: invalid manifest: %w", err)
	}
	return m, nil
}

// docGates converts gate docs to Gates.
func docGates(ds []gateDoc) []Gate {
	gs := make([]Gate, 0, len(ds))
	for _, d := range ds {
		gs = append(gs, Gate(d))
	}
	return gs
}
