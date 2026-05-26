package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/permissionmode"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

func toolNames(tools []provider.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

var _ = Describe("Tool schema filtering", Label("integration"), func() {
	var (
		chatProvider *mockProvider
		allTools     []tool.Tool
	)

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Hello! How can I help?", Done: true},
			},
		}

		allTools = []tool.Tool{
			&mockTool{name: "bash", description: "Execute commands"},
			&mockTool{name: "read", description: "Read files"},
			&mockTool{name: "write", description: "Write files"},
			&mockTool{name: "web", description: "Fetch web content"},
			&mockTool{name: "skill_load", description: "Load skills"},
			&mockTool{name: "todowrite", description: "Write todos"},
			&mockTool{name: "delegate", description: "Delegate tasks"},
			&mockTool{name: "background_output", description: "Get background output"},
			&mockTool{name: "background_cancel", description: "Cancel background tasks"},
			&mockTool{name: "coordination_store", description: "Coordination store"},
			&mockTool{name: "create_entities", description: "Create entities in memory"},
			&mockTool{name: "search_nodes", description: "Search nodes in memory"},
			&mockTool{name: "query_vault", description: "Query the vault-rag index"},
		}
	})

	Describe("buildToolSchemas respects manifest capabilities", func() {
		Context("when manifest declares specific tools", func() {
			It("only exposes declared tools to the provider", func() {
				manifest := agent.Manifest{
					ID:   "executor",
					Name: "Executor",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an executor.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file", "web"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())

				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1 (Agent Runtime Quality plan, May 2026): every
				// manifest inherits {todowrite, todo_update, skill_load}.
				// Of those, only todowrite + skill_load are registered
				// on this fixture's `allTools` slice — todo_update is
				// not — so the intersection adds those two.
				// Behaviour-Pinned: the pre-D1 NotTo(ContainElement) pins
				// on skill_load/todowrite are FLIPPED — those tools now
				// appear by inheritance unless explicitly denied.
				Expect(names).To(ConsistOf("bash", "read", "write", "web", "skill_load", "todowrite"))
				Expect(names).NotTo(ContainElement("delegate"))
				Expect(names).NotTo(ContainElement("background_output"))
				Expect(names).NotTo(ContainElement("background_cancel"))
				Expect(names).NotTo(ContainElement("coordination_store"))
			})
		})

		Context("when manifest declares specific tools and uses ToolsDeny to opt out of base tools", func() {
			It("inherits the base set minus the deny entries", func() {
				manifest := agent.Manifest{
					ID:   "deny-base",
					Name: "Deny Base",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a deny-base executor.",
					},
					Capabilities: agent.Capabilities{
						Tools:     []string{"bash", "file", "web"},
						ToolsDeny: []string{"todowrite"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				names := toolNames(chatProvider.capturedRequest.Tools)
				// D3 (Agent Runtime Quality plan, May 2026): ToolsDeny
				// subtracts entries from the effective set. todowrite
				// is denied; skill_load remains inherited from the base.
				Expect(names).To(ConsistOf("bash", "read", "write", "web", "skill_load"))
				Expect(names).NotTo(ContainElement("todowrite"))
			})
		})

		Context("when manifest declares delegate capability", func() {
			It("includes delegate, background_output, and background_cancel", func() {
				manifest := agent.Manifest{
					ID:   "planner",
					Name: "Planner",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a planner.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{"delegate"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "plan something")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())

				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited. todo_update is not registered
				// on this fixture's allTools, so it does not appear.
				Expect(names).To(ConsistOf("delegate", "background_output", "background_cancel", "skill_load", "todowrite"))
				Expect(names).NotTo(ContainElement("bash"))
				Expect(names).NotTo(ContainElement("read"))
				Expect(names).NotTo(ContainElement("write"))
				Expect(names).NotTo(ContainElement("web"))
			})

			// Commit 3 — Gap B: the pre-commit-3 `delegate` bundle silently
			// expanded to include autoresearch_run + autoresearch_prune.
			// This let a coordinator with `tools: [delegate]` do its own
			// background research instead of delegating a researcher
			// member. The bundle is now narrowed to the lifecycle tools
			// (delegate + background_output + background_cancel + the
			// always-on suggest_delegate); autoresearch_* requires
			// explicit declaration.
			//
			// The schema-based test above does not catch this directly
			// because the fixture's allTools slice does not register
			// autoresearch_run/_prune as tools, so they would never reach
			// the schema even if `allowed[]` contained them. The white-
			// box assertion below uses buildAllowedToolSetFor through the
			// new export to pin the `allowed[]` membership directly.
			It("does NOT expand the delegate bundle to autoresearch_run or autoresearch_prune", func() {
				manifest := agent.Manifest{
					ID:   "narrow-coordinator",
					Name: "Narrow Coordinator",
					Capabilities: agent.Capabilities{
						Tools: []string{"delegate"},
					},
				}
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				allowed := eng.BuildAllowedToolSetForTest(manifest)

				Expect(allowed).To(HaveKey("delegate"))
				Expect(allowed).To(HaveKey("background_output"))
				Expect(allowed).To(HaveKey("background_cancel"))
				Expect(allowed).NotTo(HaveKey("autoresearch_run"),
					"declaring `delegate` must not implicitly grant background-research surfaces — coordinators that need autoresearch must declare it explicitly")
				Expect(allowed).NotTo(HaveKey("autoresearch_prune"),
					"declaring `delegate` must not implicitly grant autoresearch_prune — paired with autoresearch_run, this surface is a delete-by-the-orchestrator hazard if granted silently")
			})

			It("DOES grant autoresearch_run + background_* when the manifest declares autoresearch_run explicitly", func() {
				manifest := agent.Manifest{
					ID:   "explicit-researcher",
					Name: "Explicit Researcher",
					Capabilities: agent.Capabilities{
						Tools: []string{"autoresearch_run"},
					},
				}
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				allowed := eng.BuildAllowedToolSetForTest(manifest)

				Expect(allowed).To(HaveKey("autoresearch_run"))
				Expect(allowed).To(HaveKey("background_output"),
					"autoresearch_run still needs its lifecycle pair — the bundle expansion for autoresearch_run is unchanged")
				Expect(allowed).To(HaveKey("background_cancel"))
				Expect(allowed).NotTo(HaveKey("delegate"),
					"the autoresearch_run alias does not back-fill delegate; the dependency arrow runs the other way")
			})
		})

		Context("when manifest declares the `file` bundle alias", func() {
			// Tool-Scoped Permissions plan (Slice C): the `file` bundle
			// alias must expand to every filesystem-mutating tool the
			// pathguard *ForTool wires gate. Pre-Slice C the alias
			// resolved to {read, write} only, which made per-tool
			// allow/deny rules for edit/multiedit/apply_patch in
			// permissions.yaml unreachable from the common
			// `tools: [file]` manifest shape. The schema-based
			// ConsistOf checks above can't catch the gap because the
			// shared allTools fixture doesn't register edit/multiedit/
			// apply_patch as schema tools — so this assertion uses
			// BuildAllowedToolSetForTest to pin the `allowed[]`
			// membership directly.
			It("expands the bundle to read+write+edit+multiedit+apply_patch", func() {
				manifest := agent.Manifest{
					ID:   "file-bundle",
					Name: "File Bundle",
					Capabilities: agent.Capabilities{
						Tools: []string{"file"},
					},
				}
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				allowed := eng.BuildAllowedToolSetForTest(manifest)

				Expect(allowed).To(HaveKey("read"))
				Expect(allowed).To(HaveKey("write"))
				Expect(allowed).To(HaveKey("edit"),
					"the file bundle must surface edit so per-tool allow/deny rules in permissions.yaml are reachable")
				Expect(allowed).To(HaveKey("multiedit"),
					"the file bundle must surface multiedit so per-tool allow/deny rules in permissions.yaml are reachable")
				Expect(allowed).To(HaveKey("apply_patch"),
					"the file bundle must surface apply_patch so per-tool allow/deny rules in permissions.yaml are reachable")
			})
		})

		Context("when manifest has empty tools list", func() {
			// Behaviour-Pinned: pre-D1 this was "fail-closed → only
			// suggest_delegate". After D1 (Agent Runtime Quality plan,
			// May 2026), every manifest inherits the base toolset, so
			// the agent gets {skill_load, todowrite} from the registered
			// fixture (todo_update is not in allTools). Implementation
			// surfaces (bash/read/write/web/delegate) still absent
			// because the manifest did not declare them.
			It("exposes the inherited base toolset, no implementation surfaces", func() {
				manifest := agent.Manifest{
					ID:   "legacy-agent",
					Name: "Legacy Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a legacy agent.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				Expect(names).To(ConsistOf("skill_load", "todowrite"))
				Expect(names).NotTo(ContainElement("bash"))
				Expect(names).NotTo(ContainElement("read"))
				Expect(names).NotTo(ContainElement("write"))
				Expect(names).NotTo(ContainElement("web"))
				Expect(names).NotTo(ContainElement("delegate"))
			})
		})

		Context("when manifest has nil tools list", func() {
			// Behaviour-Pinned: same flip as the empty-tools case above.
			It("exposes the inherited base toolset, no implementation surfaces", func() {
				manifest := agent.Manifest{
					ID:   "nil-tools-agent",
					Name: "Nil Tools Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a nil tools agent.",
					},
					Capabilities: agent.Capabilities{},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				Expect(names).To(ConsistOf("skill_load", "todowrite"))
				Expect(names).NotTo(ContainElement("bash"))
				Expect(names).NotTo(ContainElement("read"))
				Expect(names).NotTo(ContainElement("write"))
				Expect(names).NotTo(ContainElement("web"))
				Expect(names).NotTo(ContainElement("delegate"))
			})
		})

		Context("when manifest changes via SetManifest", func() {
			It("rebuilds tool schemas with new filter", func() {
				restrictedManifest := agent.Manifest{
					ID:   "restricted",
					Name: "Restricted",
					Instructions: agent.Instructions{
						SystemPrompt: "You are restricted.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{"bash"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     restrictedManifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "first")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				firstNames := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(firstNames).To(ConsistOf("bash", "skill_load", "todowrite"))

				expandedManifest := agent.Manifest{
					ID:   "expanded",
					Name: "Expanded",
					Instructions: agent.Instructions{
						SystemPrompt: "You are expanded.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "web"},
					},
				}
				eng.SetManifest(expandedManifest)

				chunks, err = eng.Stream(context.Background(), "", "second")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				secondNames := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(secondNames).To(ConsistOf("bash", "web", "skill_load", "todowrite"))
			})
		})

		Context("when manifest declares coordination_store directly", func() {
			It("includes coordination_store in the exposed tools", func() {
				manifest := agent.Manifest{
					ID:   "explorer",
					Name: "Explorer",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an explorer.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file", "coordination_store"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "explore")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())

				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(names).To(ConsistOf("bash", "read", "write", "coordination_store", "skill_load", "todowrite"))
			})
		})
	})

	Describe("buildToolSchemas gates MCP tools by manifest MCPServers allowlist", func() {
		Context("when manifest declares mcp_servers, only the declared servers' tools are exposed", func() {
			It("exposes manifest tools plus the tools of the declared MCP servers", func() {
				manifest := agent.Manifest{
					ID:   "mcp-allowlist-agent",
					Name: "MCP Allowlist Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an MCP allowlist agent.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"bash"},
						MCPServers: []string{"memory"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
					MCPServerTools: map[string][]string{
						"memory": {"create_entities", "search_nodes"},
					},
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(names).To(ConsistOf("bash", "create_entities", "search_nodes", "skill_load", "todowrite"))
				Expect(names).NotTo(ContainElement("web"))
				Expect(names).NotTo(ContainElement("read"))
				Expect(names).NotTo(ContainElement("write"))
			})
		})

		Context("when manifest declares an empty mcp_servers list, no MCP tools are exposed", func() {
			It("excludes MCP tools even though MCPServerTools is configured on the engine", func() {
				manifest := agent.Manifest{
					ID:   "mcp-empty-allowlist-agent",
					Name: "MCP Empty Allowlist Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an agent that opts out of MCP tools.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"bash"},
						MCPServers: []string{},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
					MCPServerTools: map[string][]string{
						"memory":    {"create_entities", "search_nodes"},
						"vault-rag": {"query_vault"},
					},
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(names).To(ConsistOf("bash", "skill_load", "todowrite"))
				Expect(names).NotTo(ContainElement("create_entities"))
				Expect(names).NotTo(ContainElement("search_nodes"))
				Expect(names).NotTo(ContainElement("query_vault"))
			})
		})

		Context("when the manifest tools list is empty (legacy fail-closed semantics)", func() {
			// Behaviour-Pinned: pre-D1 this was "no built-in tools and
			// no MCP tools (suggest_delegate aside)". After D1, every
			// manifest inherits the base toolset, so {skill_load,
			// todowrite} appear from the registered fixture.
			It("exposes only the inherited base toolset, no MCP tools, no built-ins", func() {
				manifest := agent.Manifest{
					ID:   "legacy-permissive-agent",
					Name: "Legacy Permissive Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a legacy permissive agent.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{},
						MCPServers: []string{},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
					MCPServerTools: map[string][]string{
						"memory": {"create_entities", "search_nodes"},
					},
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				Expect(names).To(ConsistOf("skill_load", "todowrite"))
				Expect(names).NotTo(ContainElement("bash"))
				Expect(names).NotTo(ContainElement("create_entities"))
				Expect(names).NotTo(ContainElement("search_nodes"))
			})
		})

		Context("when MCPServerTools is nil and manifest restricts tools", func() {
			It("exposes only the tools declared in the manifest", func() {
				manifest := agent.Manifest{
					ID:   "no-mcp-agent",
					Name: "No MCP Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an agent with no MCP tools.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"bash"},
						MCPServers: []string{"memory"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider:   chatProvider,
					Manifest:       manifest,
					Tools:          allTools,
					MCPServerTools: nil,
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(names).To(ConsistOf("bash", "skill_load", "todowrite"))
			})
		})

		Context("when manifest opts into multiple MCP servers", func() {
			It("includes tools from each declared server but excludes undeclared ones", func() {
				manifest := agent.Manifest{
					ID:   "multi-mcp-agent",
					Name: "Multi MCP Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an agent with multiple MCP servers.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"bash"},
						MCPServers: []string{"memory", "vault-rag"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
					MCPServerTools: map[string][]string{
						"memory":     {"create_entities"},
						"vault-rag":  {"search_nodes"},
						"undeclared": {"web"},
					},
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(names).To(ConsistOf("bash", "create_entities", "search_nodes", "skill_load", "todowrite"))
			})
		})

		Context("when a declared server is not present in the engine's MCPServerTools", func() {
			It("silently ignores the unknown server and exposes only the manifest's built-in tools", func() {
				manifest := agent.Manifest{
					ID:   "unknown-mcp-agent",
					Name: "Unknown MCP Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an agent referencing an unavailable MCP server.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"bash"},
						MCPServers: []string{"nonexistent"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
					MCPServerTools: map[string][]string{
						"memory": {"create_entities", "search_nodes"},
					},
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(names).To(ConsistOf("bash", "skill_load", "todowrite"))
			})
		})

		Context("delegation handoff: child agent's manifest gates its own MCP exposure", func() {
			It("re-evaluates the MCPServers gate against the new manifest after SetManifest", func() {
				delegatorManifest := agent.Manifest{
					ID:   "delegator",
					Name: "Delegator",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the delegator.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"bash"},
						MCPServers: []string{"memory"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     delegatorManifest,
					Tools:        allTools,
					MCPServerTools: map[string][]string{
						"memory":    {"create_entities", "search_nodes"},
						"vault-rag": {"query_vault"},
					},
				})

				// Delegator turn: should see memory tools only, NOT vault-rag.
				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}
				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				delegatorNames := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(delegatorNames).To(ConsistOf("bash", "create_entities", "search_nodes", "skill_load", "todowrite"))
				Expect(delegatorNames).NotTo(ContainElement("query_vault"))

				// Hand off to a child whose manifest opts into vault-rag only.
				childManifest := agent.Manifest{
					ID:   "child-librarian",
					Name: "Child Librarian",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the child librarian.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"web"},
						MCPServers: []string{"vault-rag"},
					},
				}
				eng.SetManifest(childManifest)

				chunks, err = eng.Stream(context.Background(), "", "lookup")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}
				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				childNames := toolNames(chatProvider.capturedRequest.Tools)
				// D1: base tools inherited.
				Expect(childNames).To(ConsistOf("web", "query_vault", "skill_load", "todowrite"))
				Expect(childNames).NotTo(ContainElement("bash"))
				Expect(childNames).NotTo(ContainElement("create_entities"))
				Expect(childNames).NotTo(ContainElement("search_nodes"))
			})
		})
	})

	// Plan-mode schema filter — Permission Modes plan (May 2026) §4 Slice 4.
	// When the active permission mode is "plan", the engine subtracts the
	// canonical mutating-tool set (permissionmode.MutatingTools) from the
	// per-call schema BEFORE the LLM sees it. A stray tool_use therefore
	// returns the tool-not-found surface, not the access-denied surface.
	Describe("Plan-mode schema filter", func() {
		var (
			planAllTools     []tool.Tool
			mutatingToolNames = []string{"bash", "write", "edit", "multiedit", "apply_patch"}
		)

		BeforeEach(func() {
			// Use a dedicated fixture that registers every mutating tool
			// by canonical name plus a representative non-mutating set
			// so the Plan-mode filter is observable end-to-end at the
			// provider request layer.
			planAllTools = []tool.Tool{
				&mockTool{name: "bash", description: "Execute commands"},
				&mockTool{name: "write", description: "Write files"},
				&mockTool{name: "edit", description: "Edit files"},
				&mockTool{name: "multiedit", description: "Multi-edit files"},
				&mockTool{name: "apply_patch", description: "Apply diffs"},
				&mockTool{name: "read", description: "Read files"},
				&mockTool{name: "grep", description: "Grep files"},
				&mockTool{name: "glob", description: "Glob files"},
				&mockTool{name: "ls", description: "List directories"},
				&mockTool{name: "lsp", description: "LSP queries"},
				&mockTool{name: "skill_load", description: "Load skills"},
				&mockTool{name: "todowrite", description: "Write todos"},
			}
		})

		Context("when ctx carries ModePlan", func() {
			It("filters bash/write/edit/multiedit/apply_patch out of the assembled schemas", func() {
				manifest := agent.Manifest{
					ID:   "plan-agent",
					Name: "Plan Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a planning agent.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        planAllTools,
				})

				ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModePlan)
				chunks, err := eng.Stream(ctx, "", "plan a refactor")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				names := toolNames(chatProvider.capturedRequest.Tools)
				for _, mutator := range mutatingToolNames {
					Expect(names).NotTo(ContainElement(mutator),
						"Plan mode must filter %q out of the schema", mutator)
				}
				// Read-only tools from the `file` bundle expansion + base
				// inherited tools should still surface. The `file` bundle
				// is read+write+edit+multiedit+apply_patch; Plan strips
				// the mutating four, leaving read.
				Expect(names).To(ContainElement("read"),
					"Plan mode must preserve read tools from the file bundle")
				Expect(names).To(ContainElement("skill_load"),
					"Plan mode must preserve inherited base tools")
				Expect(names).To(ContainElement("todowrite"),
					"Plan mode must preserve inherited base tools")
			})
		})

		Context("when ctx carries ModeYolo", func() {
			It("does NOT filter mutating tools — every declared tool surfaces", func() {
				manifest := agent.Manifest{
					ID:   "yolo-agent",
					Name: "Yolo Agent",
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        planAllTools,
				})

				ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModeYolo)
				chunks, err := eng.Stream(ctx, "", "do anything")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				names := toolNames(chatProvider.capturedRequest.Tools)
				for _, mutator := range mutatingToolNames {
					Expect(names).To(ContainElement(mutator),
						"Yolo mode must NOT filter %q — Plan filter is mode-gated", mutator)
				}
			})
		})

		Context("when ctx carries ModeDefault", func() {
			It("does NOT filter mutating tools — every declared tool surfaces", func() {
				manifest := agent.Manifest{
					ID:   "default-agent",
					Name: "Default Agent",
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        planAllTools,
				})

				ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModeDefault)
				chunks, err := eng.Stream(ctx, "", "work as normal")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				names := toolNames(chatProvider.capturedRequest.Tools)
				for _, mutator := range mutatingToolNames {
					Expect(names).To(ContainElement(mutator),
						"Default mode must NOT filter %q", mutator)
				}
			})
		})

		Context("when ctx carries ModeAcceptEdits", func() {
			It("does NOT filter mutating tools — Accept-Edits enforcement is pathguard-side, not engine-side", func() {
				manifest := agent.Manifest{
					ID:   "accept-edits-agent",
					Name: "Accept Edits Agent",
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        planAllTools,
				})

				ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModeAcceptEdits)
				chunks, err := eng.Stream(ctx, "", "edit a file")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				names := toolNames(chatProvider.capturedRequest.Tools)
				for _, mutator := range mutatingToolNames {
					Expect(names).To(ContainElement(mutator),
						"Accept-Edits mode must NOT filter %q — auto-accept lives in the prompt layer, schema is unchanged", mutator)
				}
			})
		})

		Context("when ctx is unstamped (no permission_mode key)", func() {
			It("does NOT filter mutating tools — FromContext defaults to Default", func() {
				manifest := agent.Manifest{
					ID:   "unstamped-agent",
					Name: "Unstamped Agent",
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        planAllTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "work")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				names := toolNames(chatProvider.capturedRequest.Tools)
				for _, mutator := range mutatingToolNames {
					Expect(names).To(ContainElement(mutator),
						"Unstamped ctx must behave as Default — %q must surface", mutator)
				}
			})
		})

		// Synthetic deny-set case: a manifest with no Capabilities.Tools
		// declared still inherits the base set. The buildAllowedToolSetFor
		// path returns a non-nil allowed map in that case, so this Context
		// validates the "copy-on-write filter" branch of the implementation
		// — the case where allowedSet is non-nil and Plan mode subtracts
		// mutators from it. The complementary "allowedSet == nil" branch
		// is exercised by the AllowedToolSet whitebox spec below.
		Context("when manifest inherits the base set (no Capabilities.Tools)", func() {
			It("Plan mode preserves the inherited non-mutating base set", func() {
				manifest := agent.Manifest{
					ID:           "base-only-agent",
					Name:         "Base Only Agent",
					Capabilities: agent.Capabilities{},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        planAllTools,
				})

				ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModePlan)
				chunks, err := eng.Stream(ctx, "", "plan only")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				names := toolNames(chatProvider.capturedRequest.Tools)
				// Base set inherited: skill_load + todowrite. Plan filter
				// doesn't strike either because neither is mutating.
				Expect(names).To(ConsistOf("skill_load", "todowrite"))
			})
		})

		// Runtime gate parity — Permission Modes plan Slice 4 follow-up.
		//
		// Slice 4 (commit 45efec09) added the Plan-mode filter inside
		// assembleToolSchemasLocked so the schema advertised to the LLM
		// excludes the mutating tool set. Live verification with glm-4.6
		// surfaced the gap: when a permissive provider hallucinates a
		// `write` tool_use OUTSIDE the advertised schema, the runtime
		// gate at executeToolCall consulted effectiveAllowedToolsForCtx —
		// which was mode-blind — and dispatched the call. The file was
		// written despite Plan mode.
		//
		// The fix restores the documented "shared seam == single source
		// of truth" invariant (engine.go effectiveAllowedToolsForCtx
		// docstring): the Plan-mode filter must apply at the seam BOTH
		// surfaces share, so a stray tool_use returns the same tool-not-
		// found rejection the runtime gate already emits for out-of-
		// manifest calls (PR7 / Option A). The rejection wraps
		// tool.ErrToolNotFound and tags IsError so the LLM's tool loop
		// sees the structured failure and can pivot on the next turn.
		Context("when Plan-mode ctx reaches executeToolCall with a hallucinated mutating tool", func() {
			It("rejects the call at the runtime gate without invoking Execute", func() {
				// Manifest declares the `file` bundle. Under Default mode
				// the bundle expansion would let executeToolCall dispatch
				// `write` directly. Under Plan mode the seam must strip
				// the mutating tools from the allowed set BEFORE the gate
				// checks the call.
				manifest := agent.Manifest{
					ID:   "plan-runtime-gate-agent",
					Name: "Plan Runtime Gate Agent",
					Capabilities: agent.Capabilities{
						Tools: []string{"file"},
					},
				}

				// executableMockTool tracks execCalled so the load-bearing
				// assertion below ("the tool body never ran") can fire.
				fakeWrite := &executableMockTool{
					name:        "write",
					description: "fake write",
					execResult:  tool.Result{Output: "should never run"},
				}

				providerReg := provider.NewRegistry()
				providerReg.Register(&mockProvider{name: "spy"})
				eng := engine.New(engine.Config{
					Manifest:      manifest,
					AgentRegistry: agent.NewRegistry(),
					Registry:      providerReg,
					ChatProvider:  &mockProvider{name: "spy"},
				})
				eng.AddTool(fakeWrite)

				ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModePlan)
				result, err := eng.ExecuteToolCallForTest(ctx, "sess-plan-runtime-gate", &provider.ToolCall{
					ID:        "call-write-hallucinated",
					Name:      "write",
					Arguments: map[string]any{"path": "/tmp/plan-mode-rejected.md", "content": "should not appear"},
				})

				Expect(err).NotTo(HaveOccurred(),
					"the gate emits an IsError tool_result, not a Go error — matches the OpenAI Agents SDK ToolNotFoundBehavior=return_error_to_model shape so the LLM's tool loop can self-correct on the next turn")
				Expect(result.IsError).To(BeTrue(),
					"the call did not run; the rejection must be tagged IsError so the chunk path stamps role=tool_error and the provider serialiser routes through the failure shape")
				Expect(errors.Is(result.Error, tool.ErrToolNotFound)).To(BeTrue(),
					"Plan-mode runtime rejection shares the PR7 Option A sentinel: tool.ErrToolNotFound covers BOTH 'tool absent from registry' and 'tool not available to this agent under the current mode' — callers using errors.Is(tool.ErrToolNotFound) continue to recognise the failure shape")
				Expect(result.Output).To(ContainSubstring("'write' not available"),
					"the rejection body must name the rejected tool so the model can reason about which call was refused")
				Expect(fakeWrite.execCalled).To(BeFalse(),
					"the load-bearing assertion: the gate must fire BEFORE Execute under Plan mode — the side-effecting tool body must never run for a hallucinated mutating call, which is the scenario the live glm-4.6 probe captured against commit 45efec09")
			})

			It("permits read-only tools from the same bundle — Plan strips ONLY the mutating subset", func() {
				// Companion pin: the same manifest under Plan mode must
				// still let `read` reach Execute. The filter is a strict
				// subset (MutatingTools), not a wholesale bundle ban.
				manifest := agent.Manifest{
					ID:   "plan-read-agent",
					Name: "Plan Read Agent",
					Capabilities: agent.Capabilities{
						Tools: []string{"file"},
					},
				}

				fakeRead := &executableMockTool{
					name:        "read",
					description: "fake read",
					execResult:  tool.Result{Output: "read output"},
				}

				providerReg := provider.NewRegistry()
				providerReg.Register(&mockProvider{name: "spy"})
				eng := engine.New(engine.Config{
					Manifest:      manifest,
					AgentRegistry: agent.NewRegistry(),
					Registry:      providerReg,
					ChatProvider:  &mockProvider{name: "spy"},
				})
				eng.AddTool(fakeRead)

				ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModePlan)
				result, err := eng.ExecuteToolCallForTest(ctx, "sess-plan-read", &provider.ToolCall{
					ID:        "call-read-allowed",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/probe"},
				})

				Expect(err).NotTo(HaveOccurred())
				Expect(result.IsError).To(BeFalse(),
					"read is in the file bundle and is not in MutatingTools — Plan mode must not strip it")
				Expect(fakeRead.execCalled).To(BeTrue(),
					"the read call must reach Execute — the runtime gate's mode-aware filter is a strict subset, not a bundle-level ban")
			})

			It("permits the same hallucinated mutating tool under Default mode — proves Plan-mode specificity", func() {
				// Symmetry pin: without ModePlan in ctx, the same call
				// shape executes normally. This catches a regression
				// where the runtime gate accidentally over-filters
				// regardless of mode.
				manifest := agent.Manifest{
					ID:   "default-mutating-agent",
					Name: "Default Mutating Agent",
					Capabilities: agent.Capabilities{
						Tools: []string{"file"},
					},
				}

				fakeWrite := &executableMockTool{
					name:        "write",
					description: "fake write",
					execResult:  tool.Result{Output: "write output"},
				}

				providerReg := provider.NewRegistry()
				providerReg.Register(&mockProvider{name: "spy"})
				eng := engine.New(engine.Config{
					Manifest:      manifest,
					AgentRegistry: agent.NewRegistry(),
					Registry:      providerReg,
					ChatProvider:  &mockProvider{name: "spy"},
				})
				eng.AddTool(fakeWrite)

				// No mode stamp — FromContext canonicalises to Default.
				result, err := eng.ExecuteToolCallForTest(context.Background(), "sess-default-write", &provider.ToolCall{
					ID:        "call-write-default",
					Name:      "write",
					Arguments: map[string]any{"path": "/tmp/default-write.md", "content": "ok"},
				})

				Expect(err).NotTo(HaveOccurred())
				Expect(result.IsError).To(BeFalse(),
					"Default mode must let `write` through — the gate filter is mode-gated")
				Expect(fakeWrite.execCalled).To(BeTrue(),
					"under Default mode the runtime gate must not invoke the Plan-mode filter — write reaches Execute")
			})
		})

		// Whitebox: documents the allowedSet=nil branch by exercising
		// MutatingTools / IsMutating directly. The engine-side equivalent
		// branch fires only when buildAllowedToolSetFor returns nil, which
		// the production path (manifest inheritance) avoids — but the
		// branch is reachable via test seam if a future code path
		// disables manifest restrictions, so we pin the helper contract.
		Context("permissionmode.IsMutating helper", func() {
			It("returns true for every canonical mutating tool", func() {
				for _, name := range mutatingToolNames {
					Expect(permissionmode.IsMutating(name)).To(BeTrue(),
						"%q must be classed as mutating", name)
				}
			})

			It("returns false for canonical read-only tools", func() {
				for _, name := range []string{"read", "grep", "glob", "ls", "lsp", "skill_load", "todowrite"} {
					Expect(permissionmode.IsMutating(name)).To(BeFalse(),
						"%q must NOT be classed as mutating", name)
				}
			})
		})
	})

	Describe("clean stream for simple messages", func() {
		Context("when user sends 'hello' to an agent with limited tools", func() {
			It("receives a direct text response with no tool call chunks", func() {
				manifest := agent.Manifest{
					ID:   "executor",
					Name: "Executor",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an executor.",
					},
					Capabilities: agent.Capabilities{
						Tools: []string{"bash", "file", "web"},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        allTools,
				})

				chunks, err := eng.Stream(context.Background(), "", "hello")
				Expect(err).NotTo(HaveOccurred())

				var collectedContent string
				for chunk := range chunks {
					Expect(chunk.EventType).NotTo(Equal("tool_call"))
					Expect(chunk.Error).NotTo(HaveOccurred())
					collectedContent += chunk.Content
				}

				Expect(collectedContent).To(Equal("Hello! How can I help?"))
			})
		})
	})
})
