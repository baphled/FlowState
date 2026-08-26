package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/swarm"
)

// unmarshalManifest decodes a YAML byte slice onto a swarm.Manifest so
// the depth-spec table can exercise the YAML field tags without
// touching the filesystem. Pulled into a helper because every "loader"
// row in this file does the same parse step.
func unmarshalManifest(body []byte) (*swarm.Manifest, error) {
	var m swarm.Manifest
	if err := yaml.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func depthBaseManifest() *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: swarm.SchemaVersionV1,
		ID:            "team",
		Lead:          "planner",
		Members:       []string{"explorer", "analyst"},
	}
}

var _ = Describe("Manifest.ResolveMaxDepth", func() {
	Context("when max_depth is unset", func() {
		It("returns 8 for swarm_type=analysis (the default type)", func() {
			m := depthBaseManifest()

			Expect(m.ResolveMaxDepth()).To(Equal(8))
		})

		It("returns 16 for swarm_type=codegen", func() {
			m := depthBaseManifest()
			m.SwarmType = swarm.SwarmTypeCodegen

			Expect(m.ResolveMaxDepth()).To(Equal(16))
		})

		It("returns 32 for swarm_type=orchestration", func() {
			m := depthBaseManifest()
			m.SwarmType = swarm.SwarmTypeOrchestration

			Expect(m.ResolveMaxDepth()).To(Equal(32))
		})

		It("treats an empty swarm_type as analysis", func() {
			m := depthBaseManifest()
			m.SwarmType = ""

			Expect(m.ResolveMaxDepth()).To(Equal(8))
		})
	})

	Context("when max_depth is set explicitly", func() {
		It("overrides the per-type default", func() {
			m := depthBaseManifest()
			m.SwarmType = swarm.SwarmTypeOrchestration
			m.MaxDepth = 5

			Expect(m.ResolveMaxDepth()).To(Equal(5))
		})

		It("overrides even when swarm_type is unset", func() {
			m := depthBaseManifest()
			m.MaxDepth = 12

			Expect(m.ResolveMaxDepth()).To(Equal(12))
		})
	})
})

var _ = Describe("DefaultMaxDepth helper", func() {
	It("maps each swarm type to its addendum-A4 default", func() {
		Expect(swarm.DefaultMaxDepthForType(swarm.SwarmTypeAnalysis)).To(Equal(8))
		Expect(swarm.DefaultMaxDepthForType(swarm.SwarmTypeCodegen)).To(Equal(16))
		Expect(swarm.DefaultMaxDepthForType(swarm.SwarmTypeOrchestration)).To(Equal(32))
	})

	It("falls back to the analysis default for an empty type", func() {
		Expect(swarm.DefaultMaxDepthForType("")).To(Equal(8))
	})
})

var _ = Describe("Manifest.Validate (swarm_type)", func() {
	It("accepts swarm_type=analysis", func() {
		m := depthBaseManifest()
		m.SwarmType = swarm.SwarmTypeAnalysis

		Expect(m.Validate(nil)).To(Succeed())
	})

	It("accepts swarm_type=codegen", func() {
		m := depthBaseManifest()
		m.SwarmType = swarm.SwarmTypeCodegen

		Expect(m.Validate(nil)).To(Succeed())
	})

	It("accepts swarm_type=orchestration", func() {
		m := depthBaseManifest()
		m.SwarmType = swarm.SwarmTypeOrchestration

		Expect(m.Validate(nil)).To(Succeed())
	})

	It("accepts an empty swarm_type (defaults to analysis)", func() {
		m := depthBaseManifest()
		m.SwarmType = ""

		Expect(m.Validate(nil)).To(Succeed())
	})

	It("rejects an unknown swarm_type", func() {
		m := depthBaseManifest()
		m.SwarmType = "magical"

		err := m.Validate(nil)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("swarm_type"))
		Expect(err.Error()).To(ContainSubstring("magical"))
	})

	It("rejects a negative max_depth", func() {
		m := depthBaseManifest()
		m.MaxDepth = -1

		err := m.Validate(nil)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("max_depth"))
	})
})

var _ = Describe("Loader (swarm_type + max_depth)", func() {
	It("parses swarm_type and max_depth from YAML", func() {
		body := []byte(`schema_version: "1.0.0"
id: codegen-team
lead: planner
swarm_type: codegen
max_depth: 20
members:
  - explorer
`)

		m, err := unmarshalManifest(body)

		Expect(err).NotTo(HaveOccurred())
		Expect(m.SwarmType).To(Equal(swarm.SwarmTypeCodegen))
		Expect(m.MaxDepth).To(Equal(20))
		Expect(m.ResolveMaxDepth()).To(Equal(20))
	})

	It("resolves the type default when max_depth is omitted", func() {
		body := []byte(`schema_version: "1.0.0"
id: orch-team
lead: planner
swarm_type: orchestration
members:
  - explorer
`)

		m, err := unmarshalManifest(body)

		Expect(err).NotTo(HaveOccurred())
		Expect(m.MaxDepth).To(Equal(0))
		Expect(m.ResolveMaxDepth()).To(Equal(32))
	})
})
