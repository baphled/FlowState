package app_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
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
