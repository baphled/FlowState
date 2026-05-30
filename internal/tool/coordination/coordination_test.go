package coordination_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	store "github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	coordination "github.com/baphled/flowstate/internal/tool/coordination"
)

var _ = Describe("CoordinationTool", func() {
	var (
		t   tool.Tool
		mem *store.MemoryStore
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		mem = store.NewMemoryStore()
		t = coordination.New(mem)
	})

	Describe("Name", func() {
		It("returns coordination_store", func() {
			Expect(t.Name()).To(Equal("coordination_store"))
		})
	})

	Describe("Schema", func() {
		It("returns a valid schema with required parameters", func() {
			s := t.Schema()
			Expect(s.Type).To(Equal("object"))
			Expect(s.Properties).To(HaveKey("operation"))
			Expect(s.Properties).To(HaveKey("key"))
			Expect(s.Properties).To(HaveKey("value"))
			Expect(s.Properties).To(HaveKey("prefix"))
			Expect(s.Required).To(ConsistOf("operation"))
			Expect(s.Properties["operation"].Enum).To(ConsistOf("get", "set", "list", "delete"))
		})
	})

	Describe("Execute", func() {
		Context("set operation", func() {
			It("stores a value", func() {
				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "chain1/plan",
						"value":     "my plan content",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("stored"))

				val, storeErr := mem.Get("chain1/plan")
				Expect(storeErr).NotTo(HaveOccurred())
				Expect(string(val)).To(Equal("my plan content"))
			})
		})

		Context("get operation", func() {
			It("returns a stored value", func() {
				Expect(mem.Set("chain1/review", []byte("review content"))).To(Succeed())

				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "get",
						"key":       "chain1/review",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("review content"))
			})
		})

		Context("list operation", func() {
			It("returns matching keys", func() {
				Expect(mem.Set("chain1/plan", []byte("p"))).To(Succeed())
				Expect(mem.Set("chain1/review", []byte("r"))).To(Succeed())
				Expect(mem.Set("chain2/plan", []byte("p2"))).To(Succeed())

				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "list",
						"prefix":    "chain1/",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("chain1/plan"))
				Expect(result.Output).To(ContainSubstring("chain1/review"))
				Expect(result.Output).NotTo(ContainSubstring("chain2/"))
			})
		})

		Context("delete operation", func() {
			It("removes a key", func() {
				Expect(mem.Set("chain1/temp", []byte("tmp"))).To(Succeed())

				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "delete",
						"key":       "chain1/temp",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("deleted"))

				_, storeErr := mem.Get("chain1/temp")
				Expect(storeErr).To(HaveOccurred())
			})
		})

		Context("inside an engine-owned swarm", func() {
			// The engine OWNS the chainID for planning-loop runs
			// (ChainIDAssigned && ChainPrefix != ""). resolveSwarmChainNamespace
			// resolves member output via SlugifyChainID(ChainPrefix), but the
			// member writes to whatever coordination_store key the LEAD dictated
			// in its delegate brief. Live planning-loop session 836526fd: the
			// planner free-formed chainID=planning/planner (a slashed value) and
			// told the explorer to write planning/planner/codebase-findings, while
			// the post-member gate looked under {engineChainID}/codebase-findings —
			// MISMATCH → gate sees empty → swarm fails. The engine must normalise
			// the member write key to the authoritative chainID prefix so the gate
			// (which resolves the authoritative chainID) finds it.
			var ownedCtx context.Context

			BeforeEach(func() {
				swarmCtx := swarm.Context{
					SwarmID:     "planning-loop",
					LeadAgent:   "planner",
					ChainPrefix: "planning-loop-7d67530ef355",
				}
				swarmCtx.AssignRunChainID("836526fd-8bcd-4d32-8ec8-99bc1c225968")
				// AssignRunChainID is a no-op when ChainPrefix != SwarmID, so
				// stamp the owned flag directly for an explicit per-run prefix.
				if !swarmCtx.ChainIDAssigned {
					swarmCtx.ChainPrefix = "planning-loop-7d67530ef355"
					swarmCtx.ChainIDAssigned = true
				}
				ownedCtx = swarm.WithScope(ctx, &swarmCtx)
			})

			It("normalises a slashed free-form chainID write to the authoritative chainID", func() {
				_, err := t.Execute(ownedCtx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "planning/planner/codebase-findings",
						"value":     `{"findings":[{"file":"x.go"}]}`,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				// The post-member gate resolves SlugifyChainID(ChainPrefix) and
				// reads {chainID}/codebase-findings — the write MUST land there.
				val, getErr := mem.Get("planning-loop-7d67530ef355/codebase-findings")
				Expect(getErr).NotTo(HaveOccurred())
				Expect(string(val)).To(ContainSubstring("x.go"))

				// The drifted key must NOT exist.
				_, driftErr := mem.Get("planning/planner/codebase-findings")
				Expect(driftErr).To(HaveOccurred())
			})

			It("prefixes a bare suffix-only key with the authoritative chainID", func() {
				_, err := t.Execute(ownedCtx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "external-refs",
						"value":     `{"references":[{"url":"http://x"}]}`,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				val, getErr := mem.Get("planning-loop-7d67530ef355/external-refs")
				Expect(getErr).NotTo(HaveOccurred())
				Expect(string(val)).To(ContainSubstring("http://x"))
			})

			It("preserves multi-segment section suffixes under the authoritative chainID", func() {
				_, err := t.Execute(ownedCtx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "bogus-prefix/sections/architecture",
						"value":     `{"section":"architecture"}`,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				val, getErr := mem.Get("planning-loop-7d67530ef355/sections/architecture")
				Expect(getErr).NotTo(HaveOccurred())
				Expect(string(val)).To(ContainSubstring("architecture"))
			})
		})

		Context("standalone (no swarm scope)", func() {
			It("leaves the write key unchanged", func() {
				_, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "freeform/whatever-key",
						"value":     "v",
					},
				})
				Expect(err).NotTo(HaveOccurred())

				val, getErr := mem.Get("freeform/whatever-key")
				Expect(getErr).NotTo(HaveOccurred())
				Expect(string(val)).To(Equal("v"))
			})
		})

		Context("unknown operation", func() {
			It("returns an error", func() {
				_, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "invalid",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unknown operation"))
			})
		})
	})
})
