package app_test

import (
	"io/fs"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"

	"gopkg.in/yaml.v3"
)

// loadFixtureSwarmDir returns the testdata/swarms directory the
// fixtures live under so NewForTest can hand the path to
// setupSwarmRegistry.
func loadFixtureSwarmDir() string {
	abs, err := filepath.Abs("testdata/swarms")
	Expect(err).NotTo(HaveOccurred())
	return abs
}

var _ = Describe("SwarmWiring", func() {
	Context("when NewForTest receives a swarms-dir fixture", func() {
		It("populates SwarmRegistry with the per-type variants", func() {
			tc := app.TestConfig{SwarmsDir: loadFixtureSwarmDir()}

			a, err := app.NewForTest(tc)
			Expect(err).NotTo(HaveOccurred())
			Expect(a.SwarmRegistry).NotTo(BeNil())

			tests := []struct {
				id    string
				depth int
			}{
				{"codegen-12", 12},
				{"orchestration-30", 30},
				{"analysis-default", 8},
			}
			for _, tt := range tests {
				m, ok := a.SwarmRegistry.Get(tt.id)
				Expect(ok).To(BeTrue(), "swarm %s should be registered", tt.id)
				Expect(m.ResolveMaxDepth()).To(Equal(tt.depth), "swarm %s expected depth %d", tt.id, tt.depth)
			}
		})
	})

	Context("when NewForTest receives no SwarmsDir", func() {
		It("leaves SwarmRegistry nil so resolveAgentOrSwarm preserves the historical pass-through", func() {
			a, err := app.NewForTest(app.TestConfig{})

			Expect(err).NotTo(HaveOccurred())
			Expect(a.SwarmRegistry).To(BeNil())
		})
	})
})

// TestEmbeddedSwarmsAttachFSPollutionGuard pins the follow-up wiring
// for commit f8b482a1: the builtin:fs-pollution-guard runner is
// registered at boot (App.buildSwarmGateRunner), but it only fires
// when a GateSpec attaches it. This test asserts the embedded
// dev-swarm and engineer-swarm manifests carry post-member
// fs-pollution-guard entries for their write-capable members so the
// gate cannot silently regress to registered-but-never-fired.
func TestEmbeddedSwarmsAttachFSPollutionGuard(t *testing.T) {
	expected := map[string]map[string]bool{
		"dev-swarm.yml": {
			"Senior-Engineer":        false,
			"Writer":                 false,
			"Knowledge-Base-Curator": false,
		},
		"engineer-swarm.yml": {
			"Mid-Engineer":    false,
			"Junior-Engineer": false,
		},
	}

	swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
	if err != nil {
		t.Fatalf("embedding swarms fs: %v", err)
	}
	for file, targets := range expected {
		body, err := fs.ReadFile(swarmsDir, file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		manifest, err := parseSwarmManifest(body)
		if err != nil {
			t.Fatalf("loading %s: %v", file, err)
		}
		for _, gate := range manifest.Harness.Gates {
			if gate.Kind != swarm.FSPollutionGateKind {
				continue
			}
			if _, ok := targets[gate.Target]; ok {
				targets[gate.Target] = true
			}
		}
		for target, found := range targets {
			if !found {
				t.Errorf("%s: no builtin:fs-pollution-guard gate targets %s", file, target)
			}
		}
	}
}

// parseSwarmManifest mirrors swarm.Load's parse+validate pipeline for
// an in-memory manifest body so the test can read from the embed FS
// instead of the source tree.
func parseSwarmManifest(body []byte) (*swarm.Manifest, error) {
	var m swarm.Manifest
	if err := yaml.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if err := m.Validate(nil); err != nil {
		return nil, err
	}
	return &m, nil
}
