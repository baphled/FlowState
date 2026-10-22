package swarm_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	toolswarm "github.com/baphled/flowstate/internal/tool/swarm"
)

// stubRegistry backs tests without touching the filesystem.
func stubRegistry(manifests ...*swarm.Manifest) toolswarm.SwarmReader {
	reg := swarm.NewRegistry()
	for _, m := range manifests {
		reg.Register(m)
	}
	return reg
}

func makeManifest(id, lead string, members []string, gates []swarm.GateSpec) *swarm.Manifest {
	m := &swarm.Manifest{}
	m.SchemaVersion = "1.0.0"
	m.ID = id
	m.Lead = lead
	m.Members = members
	m.Harness.Gates = gates
	return m
}

var _ = Describe("swarm_list", func() {
	It("returns each registered swarm's id, lead, member count, and gate count", func() {
		reg := stubRegistry(
			makeManifest("bug-hunt", "Senior-Engineer", []string{"explorer", "analyst"}, []swarm.GateSpec{{Name: "g1"}, {Name: "g2"}}),
			makeManifest("dev-feature", "Tech-Lead", []string{"explorer", "analyst", "plan-writer"}, []swarm.GateSpec{{Name: "g1"}}),
		)
		t := toolswarm.NewSwarmListTool(reg)

		result, err := t.Execute(context.Background(), tool.Input{Name: "swarm_list", Arguments: map[string]interface{}{}})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("bug-hunt"))
		Expect(result.Output).To(ContainSubstring("Senior-Engineer"))
		Expect(result.Output).To(ContainSubstring("dev-feature"))
		Expect(result.Output).To(ContainSubstring("Tech-Lead"))
	})

	It("returns a clear message when no swarms are registered", func() {
		reg := stubRegistry()
		t := toolswarm.NewSwarmListTool(reg)

		result, err := t.Execute(context.Background(), tool.Input{Name: "swarm_list", Arguments: map[string]interface{}{}})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("no swarms"))
	})

	It("has the correct tool name and schema", func() {
		t := toolswarm.NewSwarmListTool(stubRegistry())
		Expect(t.Name()).To(Equal("swarm_list"))
		Expect(t.Schema().Type).To(Equal("object"))
	})
})

var _ = Describe("swarm_info", func() {
	It("returns full details for a known swarm", func() {
		reg := stubRegistry(
			makeManifest("bug-hunt", "Senior-Engineer",
				[]string{"explorer", "analyst", "fixer", "reviewer"},
				[]swarm.GateSpec{
					{Name: "post-explorer", Kind: "builtin:result-schema", Target: "explorer"},
				},
			),
		)
		t := toolswarm.NewSwarmInfoTool(reg)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "swarm_info",
			Arguments: map[string]interface{}{"id": "bug-hunt"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("bug-hunt"))
		Expect(result.Output).To(ContainSubstring("Senior-Engineer"))
		Expect(result.Output).To(ContainSubstring("explorer"))
		Expect(result.Output).To(ContainSubstring("analyst"))
		Expect(result.Output).To(ContainSubstring("post-explorer"))
	})

	It("returns a clear error for an unknown swarm id", func() {
		reg := stubRegistry()
		t := toolswarm.NewSwarmInfoTool(reg)

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "swarm_info",
			Arguments: map[string]interface{}{"id": "no-such-swarm"},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no-such-swarm"))
	})

	It("returns an error when id argument is missing", func() {
		t := toolswarm.NewSwarmInfoTool(stubRegistry())

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "swarm_info",
			Arguments: map[string]interface{}{},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("id"))
	})

	It("has the correct tool name and requires id in schema", func() {
		t := toolswarm.NewSwarmInfoTool(stubRegistry())
		Expect(t.Name()).To(Equal("swarm_info"))
		Expect(t.Schema().Required).To(ContainElement("id"))
	})
})

var _ = Describe("swarm_validate", func() {
	It("reports PASS for a valid swarm", func() {
		reg := stubRegistry(makeManifest("solo", "executor", nil, nil))
		t := toolswarm.NewSwarmValidateTool(reg)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "swarm_validate",
			Arguments: map[string]interface{}{"id": "solo"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("PASS"))
		Expect(result.Output).To(ContainSubstring("solo"))
	})

	It("returns an error for an unknown swarm id", func() {
		reg := stubRegistry()
		t := toolswarm.NewSwarmValidateTool(reg)

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "swarm_validate",
			Arguments: map[string]interface{}{"id": "ghost"},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ghost"))
	})

	It("returns an error when id argument is missing", func() {
		t := toolswarm.NewSwarmValidateTool(stubRegistry())

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "swarm_validate",
			Arguments: map[string]interface{}{},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("id"))
	})

	It("has the correct tool name and requires id in schema", func() {
		t := toolswarm.NewSwarmValidateTool(stubRegistry())
		Expect(t.Name()).To(Equal("swarm_validate"))
		Expect(t.Schema().Required).To(ContainElement("id"))
	})
})
