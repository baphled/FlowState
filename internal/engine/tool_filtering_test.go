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

	Describe("buildToolSchemas respects MCPServers capability", func() {
		Context("when manifest declares mcp_servers, it includes all tools from those servers", func() {
			It("exposes tools declared in the manifest tools list plus all tools from matching MCP servers", func() {
				manifest := agent.Manifest{
					ID:   "mcp-agent",
					Name: "MCP Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an MCP agent.",
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
				Expect(names).To(ContainElements("bash", "create_entities", "search_nodes"))
				Expect(names).NotTo(ContainElement("web"))
				Expect(names).NotTo(ContainElement("read"))
				Expect(names).NotTo(ContainElement("write"))
			})
		})

		Context("when manifest declares mcp_servers but tools list is empty", func() {
			It("exposes all tools for backward compatibility", func() {
				manifest := agent.Manifest{
					ID:   "mcp-only-agent",
					Name: "MCP Only Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an MCP only agent.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{},
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
				Expect(chatProvider.capturedRequest.Tools).To(HaveLen(len(allTools)))
			})
		})

		Context("when manifest declares unknown mcp_server", func() {
			It("silently ignores the unknown server and only exposes declared tools", func() {
				manifest := agent.Manifest{
					ID:   "unknown-mcp-agent",
					Name: "Unknown MCP Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are an agent with an unknown MCP server.",
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

		Context("when manifest declares both tools and mcp_servers", func() {
			It("merges tools from both sources", func() {
				manifest := agent.Manifest{
					ID:   "merged-agent",
					Name: "Merged Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a merged agent.",
					},
					Capabilities: agent.Capabilities{
						Tools:      []string{"bash", "web"},
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
				Expect(names).To(ConsistOf("bash", "web", "create_entities", "search_nodes"))
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
