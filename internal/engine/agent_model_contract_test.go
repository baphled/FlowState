// Package engine — Agent Model Routing Contract
//
// This file is the authoritative specification for which abstract model
// descriptor each built-in FlowState agent complexity routes to.
// If you change an agent manifest's complexity field, update this table.
// If you add a new complexity tier, add it to DefaultCategoryRouting first.
package engine_test

import (
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
)

// agentContract describes the expected routing for a single built-in agent.
type agentContract struct {
	agentID            string
	expectedComplexity string
	expectedDescriptor string
}

// builtInAgentContracts is the single authoritative mapping of every built-in
// FlowState agent to its complexity tier and expected abstract model descriptor.
var builtInAgentContracts = []agentContract{
	{agentID: "planner", expectedComplexity: "deep", expectedDescriptor: "reasoning"},
	{agentID: "plan-writer", expectedComplexity: "medium", expectedDescriptor: "balanced"},
	{agentID: "plan-reviewer", expectedComplexity: "medium", expectedDescriptor: "balanced"},
	{agentID: "executor", expectedComplexity: "deep", expectedDescriptor: "reasoning"},
	{agentID: "analyst", expectedComplexity: "deep", expectedDescriptor: "reasoning"},
	{agentID: "explorer", expectedComplexity: "low", expectedDescriptor: "fast"},
	{agentID: "librarian", expectedComplexity: "low", expectedDescriptor: "fast"},
}

var _ = Describe("Agent Model Routing Contract", Label("integration", "contract"), func() {
	Describe("every agent complexity resolves to its expected abstract descriptor", func() {
		DescribeTable("agentID → complexity → descriptor",
			func(contract agentContract) {
				resolver := engine.NewCategoryResolver(nil)

				cfg, err := resolver.Resolve(contract.expectedComplexity)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Model).To(Equal(contract.expectedDescriptor))
			},
			Entry("planner", builtInAgentContracts[0]),
			Entry("plan-writer", builtInAgentContracts[1]),
			Entry("plan-reviewer", builtInAgentContracts[2]),
			Entry("executor", builtInAgentContracts[3]),
			Entry("analyst", builtInAgentContracts[4]),
			Entry("explorer", builtInAgentContracts[5]),
			Entry("librarian", builtInAgentContracts[6]),
		)
	})

	Describe("medium tier regression guard", func() {
		It("DefaultCategoryRouting contains the 'medium' entry (regression guard)", func() {
			routing := engine.DefaultCategoryRouting()

			_, ok := routing["medium"]

			Expect(ok).To(BeTrue())
		})

		It("DefaultCategoryRouting resolves 'medium' to 'balanced'", func() {
			resolver := engine.NewCategoryResolver(nil)

			cfg, err := resolver.Resolve("medium")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("balanced"))
		})

		It("explorer and librarian agents are no longer silently unmapped", func() {
			resolver := engine.NewCategoryResolver(nil)

			cfg, err := resolver.Resolve("low")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
		})
	})

	Describe("low tier regression guard", func() {
		It("DefaultCategoryRouting contains the 'low' entry (regression guard)", func() {
			routing := engine.DefaultCategoryRouting()

			_, ok := routing["low"]

			Expect(ok).To(BeTrue())
		})

		It("DefaultCategoryRouting resolves 'low' to 'fast'", func() {
			resolver := engine.NewCategoryResolver(nil)

			cfg, err := resolver.Resolve("low")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
		})
	})

	Describe("manifest .Complexity field matches contract table", func() {
		var agentsDir string

		BeforeEach(func() {
			_, file, _, ok := runtime.Caller(0)
			Expect(ok).To(BeTrue())
			agentsDir = filepath.Join(filepath.Dir(file), "..", "app", "agents")
		})

		DescribeTable("agent manifest complexity matches contract",
			func(contract agentContract) {
				manifestPath := filepath.Join(agentsDir, contract.agentID+".md")
				manifest, err := agent.LoadManifestMarkdown(manifestPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(manifest.Complexity).To(Equal(contract.expectedComplexity))
			},
			Entry("planner", builtInAgentContracts[0]),
			Entry("plan-writer", builtInAgentContracts[1]),
			Entry("plan-reviewer", builtInAgentContracts[2]),
			Entry("executor", builtInAgentContracts[3]),
			Entry("analyst", builtInAgentContracts[4]),
			Entry("explorer", builtInAgentContracts[5]),
			Entry("librarian", builtInAgentContracts[6]),
		)
	})
})
