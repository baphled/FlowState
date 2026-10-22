package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("ManifestBuilder", func() {
	It("builds a minimal valid manifest", func() {
		m := swarm.NewManifestBuilder("demo").
			WithDescription("test swarm").
			WithLead("librarian").
			WithMember("librarian").
			Build()

		Expect(m.ID).To(Equal("demo"))
		Expect(m.Description).To(Equal("test swarm"))
		Expect(m.Lead).To(Equal("librarian"))
		Expect(m.Members).To(ConsistOf("librarian"))
		Expect(m.Validate(nil)).To(Succeed())
	})

	It("appends a gate via WithGate", func() {
		m := swarm.NewManifestBuilder("demo").
			WithLead("librarian").
			WithMember("librarian").
			WithGate("post-fact-check", "ext:test-fact-check", swarm.LifecyclePostMember, "librarian").
			Build()

		Expect(m.Harness.Gates).To(HaveLen(1))
		Expect(m.Harness.Gates[0].Name).To(Equal("post-fact-check"))
		Expect(m.Harness.Gates[0].Kind).To(Equal("ext:test-fact-check"))
		Expect(m.Harness.Gates[0].Target).To(Equal("librarian"))
	})

	It("supports multiple members + gates", func() {
		m := swarm.NewManifestBuilder("demo").
			WithLead("planner").
			WithMember("explorer").
			WithMember("librarian").
			WithGate("g1", "builtin:result-schema", swarm.LifecyclePostMember, "explorer").
			WithGate("g2", "ext:fact-check", swarm.LifecyclePostMember, "librarian").
			Build()

		Expect(m.Members).To(Equal([]string{"explorer", "librarian"}))
		Expect(m.Harness.Gates).To(HaveLen(2))
	})
})
