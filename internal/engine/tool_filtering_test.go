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
				Expect(names).To(ConsistOf("bash", "read", "write", "web"))
				Expect(names).NotTo(ContainElement("skill_load"))
				Expect(names).NotTo(ContainElement("todowrite"))
				Expect(names).NotTo(ContainElement("delegate"))
				Expect(names).NotTo(ContainElement("background_output"))
				Expect(names).NotTo(ContainElement("background_cancel"))
				Expect(names).NotTo(ContainElement("coordination_store"))
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
				Expect(names).To(ConsistOf("delegate", "background_output", "background_cancel"))
				Expect(names).NotTo(ContainElement("bash"))
				Expect(names).NotTo(ContainElement("read"))
				Expect(names).NotTo(ContainElement("write"))
				Expect(names).NotTo(ContainElement("web"))
			})
		})

		Context("when manifest has empty tools list", func() {
			It("exposes all registered tools for backward compatibility", func() {
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
				Expect(chatProvider.capturedRequest.Tools).To(HaveLen(len(allTools)))
			})
		})

		Context("when manifest has nil tools list", func() {
			It("exposes all registered tools for backward compatibility", func() {
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
				Expect(chatProvider.capturedRequest.Tools).To(HaveLen(len(allTools)))
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
				Expect(firstNames).To(ConsistOf("bash"))

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
				Expect(secondNames).To(ConsistOf("bash", "web"))
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
				Expect(names).To(ConsistOf("bash", "read", "write", "coordination_store"))
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
				Expect(names).To(ConsistOf("bash", "create_entities", "search_nodes"))
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
				Expect(names).To(ConsistOf("bash"))
				Expect(names).NotTo(ContainElement("create_entities"))
				Expect(names).NotTo(ContainElement("search_nodes"))
				Expect(names).NotTo(ContainElement("query_vault"))
			})
		})

		Context("when the manifest tools list is empty, the legacy permissive branch exposes every registered tool", func() {
			It("exposes all built-in tools for backward compatibility regardless of mcp_servers", func() {
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
				Expect(chatProvider.capturedRequest.Tools).To(HaveLen(len(allTools)))
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
				Expect(names).To(ConsistOf("bash"))
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
						"memory":    {"create_entities"},
						"vault-rag": {"search_nodes"},
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
				Expect(names).To(ConsistOf("bash", "create_entities", "search_nodes"))
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
				Expect(names).To(ConsistOf("bash"))
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
				Expect(delegatorNames).To(ConsistOf("bash", "create_entities", "search_nodes"))
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
				Expect(childNames).To(ConsistOf("web", "query_vault"))
				Expect(childNames).NotTo(ContainElement("bash"))
				Expect(childNames).NotTo(ContainElement("create_entities"))
				Expect(childNames).NotTo(ContainElement("search_nodes"))
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
