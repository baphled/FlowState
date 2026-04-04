package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("CategoryResolver routing", Label("integration"), func() {
	Describe("visual-engineering category", func() {
		It("routes via vision model descriptor", func() {
			resolver := engine.NewCategoryResolver(nil)

			cfg, err := resolver.Resolve("visual-engineering")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("vision"))
		})

		It("uses category only for model routing, not agent identity", func() {
			resolver := engine.NewCategoryResolver(nil)

			cfg, err := resolver.Resolve("visual-engineering")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Provider).To(BeEmpty())
			Expect(cfg.Model).To(Equal("vision"))
		})
	})

	Describe("unknown category handling", func() {
		It("falls through gracefully for unknown category", func() {
			resolver := engine.NewCategoryResolver(nil)

			_, err := resolver.Resolve("unknown-category-xyz")

			Expect(err).To(HaveOccurred())
		})

		It("succeeds cleanly when category is empty and resolver is nil on DelegateTool", func() {
			targetProvider := &mockProvider{
				name: "test-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "response", Done: true},
				},
			}
			targetManifest := agent.Manifest{
				ID:                "target-agent",
				Name:              "Target Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are target."},
				ContextManagement: agent.DefaultContextManagement(),
			}
			targetEng := engine.New(engine.Config{
				ChatProvider: targetProvider,
				Manifest:     targetManifest,
			})

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEng},
				agent.Delegation{CanDelegate: true},
				"orchestrator",
			)

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "target-agent",
					"message":       "run this task",
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("response"))
		})
	})
})

var _ = Describe("Agent Registry Resolution", Label("integration"), func() {
	var (
		reg            *agent.Registry
		enginesByID    map[string]*engine.Engine
		targetProvider *mockProvider
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{
			ID:      "senior-engineer",
			Name:    "Senior Engineer",
			Aliases: []string{"lead-dev", "Guru"},
		})
		reg.Register(&agent.Manifest{
			ID:   "qa-agent",
			Name: "QA Agent",
		})

		targetProvider = &mockProvider{
			name: "test-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "agent resolved response", Done: true},
			},
		}

		seniorEng := engine.New(engine.Config{
			ChatProvider: targetProvider,
			Manifest: agent.Manifest{
				ID:                "senior-engineer",
				Name:              "Senior Engineer",
				Instructions:      agent.Instructions{SystemPrompt: "You are senior."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})
		qaEng := engine.New(engine.Config{
			ChatProvider: targetProvider,
			Manifest: agent.Manifest{
				ID:                "qa-agent",
				Name:              "QA Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})

		enginesByID = map[string]*engine.Engine{
			"senior-engineer": seniorEng,
			"qa-agent":        qaEng,
		}
	})

	It("resolves agent by exact name to correct engine", func() {
		delegateTool := engine.NewDelegateTool(enginesByID, agent.Delegation{CanDelegate: true}, "orchestrator").
			WithRegistry(reg)

		id, err := delegateTool.ResolveByNameOrAlias("senior-engineer")

		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("senior-engineer"))
	})

	It("resolves by alias case-insensitively to canonical agent ID", func() {
		delegateTool := engine.NewDelegateTool(enginesByID, agent.Delegation{CanDelegate: true}, "orchestrator").
			WithRegistry(reg)

		id, err := delegateTool.ResolveByNameOrAlias("guru")

		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("senior-engineer"))
	})

	It("returns error listing available agents for unknown agent", func() {
		delegateTool := engine.NewDelegateTool(enginesByID, agent.Delegation{CanDelegate: true}, "orchestrator").
			WithRegistry(reg)

		_, err := delegateTool.ResolveByNameOrAlias("nonexistent-agent")

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nonexistent-agent"))
		Expect(err.Error()).To(ContainSubstring("available agents"))
	})

	It("SetManifest updates manifest and invalidates the system prompt cache", func() {
		eng := engine.New(engine.Config{
			ChatProvider: targetProvider,
			Manifest: agent.Manifest{
				ID:   "initial-agent",
				Name: "Initial Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "Original prompt.",
				},
			},
		})

		original := eng.BuildSystemPrompt()
		Expect(original).To(Equal("Original prompt."))

		eng.SetManifest(agent.Manifest{
			ID:   "updated-agent",
			Name: "Updated Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "Updated prompt.",
			},
		})

		updated := eng.BuildSystemPrompt()
		Expect(updated).To(Equal("Updated prompt."))
		Expect(updated).NotTo(Equal(original))
	})
})

var _ = Describe("DelegationAllowlist", Label("integration"), func() {
	var (
		targetProvider *mockProvider
		targetEng      *engine.Engine
	)

	BeforeEach(func() {
		targetProvider = &mockProvider{
			name: "test-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "allowed response", Done: true},
			},
		}
		targetEng = engine.New(engine.Config{
			ChatProvider: targetProvider,
			Manifest: agent.Manifest{
				ID:                "allowed-agent",
				Name:              "Allowed Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are allowed."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})
	})

	It("allows delegation when agent is in the allowlist", func() {
		delegation := agent.Delegation{
			CanDelegate:         true,
			DelegationAllowlist: []string{"allowed-agent"},
		}
		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"allowed-agent": targetEng},
			delegation,
			"orchestrator",
		)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "allowed-agent",
				"message":       "do something",
			},
		}

		result, err := delegateTool.Execute(ctx, input)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("allowed response"))
	})

	It("returns error when agent is NOT in the allowlist", func() {
		delegation := agent.Delegation{
			CanDelegate:         true,
			DelegationAllowlist: []string{"other-agent"},
		}

		blockedProvider := &mockProvider{
			name: "test-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "blocked response", Done: true},
			},
		}
		blockedEng := engine.New(engine.Config{
			ChatProvider: blockedProvider,
			Manifest: agent.Manifest{
				ID:                "blocked-agent",
				Name:              "Blocked Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are blocked."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})
		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"blocked-agent": blockedEng},
			delegation,
			"orchestrator",
		)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "blocked-agent",
				"message":       "do something",
			},
		}

		_, err := delegateTool.Execute(ctx, input)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not in allowlist"))
	})

	It("allows all agents when allowlist is empty (backward compatible)", func() {
		delegation := agent.Delegation{
			CanDelegate:         true,
			DelegationAllowlist: []string{},
		}
		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"allowed-agent": targetEng},
			delegation,
			"orchestrator",
		)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "allowed-agent",
				"message":       "do something",
			},
		}

		result, err := delegateTool.Execute(ctx, input)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("allowed response"))
	})
})
