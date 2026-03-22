package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/memory"
)

var _ = Describe("Tools", func() {
	var (
		server  *mcp.Server
		graph   *memory.Graph
		store   *memory.JSONLStore
		session *mcp.ClientSession
		ctx     context.Context
		tmpDir  string
	)

	BeforeEach(func() {
		ctx = context.Background()

		tmpDir = GinkgoT().TempDir()
		storePath := filepath.Join(tmpDir, "test-memory.jsonl")
		store = memory.NewJSONLStore(storePath)
		graph = memory.NewGraph()

		server = mcp.NewServer(&mcp.Implementation{
			Name:    "test-memory",
			Version: "0.1.0",
		}, nil)

		memory.RegisterTools(server, graph, store)

		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		serverErr := make(chan error, 1)
		go func() {
			serverErr <- server.Run(ctx, serverTransport)
		}()

		client := mcp.NewClient(&mcp.Implementation{
			Name:    "test-client",
			Version: "1.0.0",
		}, nil)

		var err error
		session, err = client.Connect(ctx, clientTransport, nil)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			session.Close()
			Eventually(serverErr).Should(Receive())
		})
	})

	Describe("create_entities", func() {
		It("creates entities in the graph", func() {
			result := callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{
						"name":         "TestEntity",
						"entityType":   "Project",
						"observations": []any{"obs1", "obs2"},
					},
				},
			})
			Expect(result.IsError).To(BeFalse())

			kg := graph.ReadGraph()
			Expect(kg.Entities).To(HaveLen(1))
			Expect(kg.Entities[0].Name).To(Equal("TestEntity"))
			Expect(kg.Entities[0].EntityType).To(Equal("Project"))
			Expect(kg.Entities[0].Observations).To(ConsistOf("obs1", "obs2"))
		})

		It("persists after creation", func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{
						"name":         "Persisted",
						"entityType":   "Test",
						"observations": []any{"saved"},
					},
				},
			})

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(HaveLen(1))
			Expect(loaded.Entities[0].Name).To(Equal("Persisted"))
		})
	})

	Describe("create_relations", func() {
		BeforeEach(func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "A", "entityType": "T", "observations": []any{}},
					map[string]any{"name": "B", "entityType": "T", "observations": []any{}},
				},
			})
		})

		It("creates relations between existing entities", func() {
			result := callTool(ctx, session, "create_relations", map[string]any{
				"relations": []any{
					map[string]any{"from": "A", "to": "B", "relationType": "knows"},
				},
			})
			Expect(result.IsError).To(BeFalse())

			kg := graph.ReadGraph()
			Expect(kg.Relations).To(HaveLen(1))
			Expect(kg.Relations[0].From).To(Equal("A"))
			Expect(kg.Relations[0].To).To(Equal("B"))
		})

		It("persists after creation", func() {
			callTool(ctx, session, "create_relations", map[string]any{
				"relations": []any{
					map[string]any{"from": "A", "to": "B", "relationType": "uses"},
				},
			})

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Relations).To(HaveLen(1))
		})
	})

	Describe("add_observations", func() {
		BeforeEach(func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "Target", "entityType": "T", "observations": []any{"existing"}},
				},
			})
		})

		It("adds observations to an existing entity", func() {
			result := callTool(ctx, session, "add_observations", map[string]any{
				"observations": []any{
					map[string]any{
						"entityName": "Target",
						"contents":   []any{"new-obs"},
					},
				},
			})
			Expect(result.IsError).To(BeFalse())

			kg := graph.ReadGraph()
			Expect(kg.Entities[0].Observations).To(ConsistOf("existing", "new-obs"))
		})

		It("returns error for non-existent entity", func() {
			result := callTool(ctx, session, "add_observations", map[string]any{
				"observations": []any{
					map[string]any{
						"entityName": "NonExistent",
						"contents":   []any{"obs"},
					},
				},
			})
			Expect(result.IsError).To(BeTrue())
		})
	})

	Describe("delete_entities", func() {
		BeforeEach(func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "ToDelete", "entityType": "T", "observations": []any{}},
					map[string]any{"name": "ToKeep", "entityType": "T", "observations": []any{}},
				},
			})
			callTool(ctx, session, "create_relations", map[string]any{
				"relations": []any{
					map[string]any{"from": "ToDelete", "to": "ToKeep", "relationType": "knows"},
				},
			})
		})

		It("removes entities and cascades relations", func() {
			result := callTool(ctx, session, "delete_entities", map[string]any{
				"entityNames": []any{"ToDelete"},
			})
			Expect(result.IsError).To(BeFalse())

			kg := graph.ReadGraph()
			Expect(kg.Entities).To(HaveLen(1))
			Expect(kg.Entities[0].Name).To(Equal("ToKeep"))
			Expect(kg.Relations).To(BeEmpty())
		})

		It("persists after deletion", func() {
			callTool(ctx, session, "delete_entities", map[string]any{
				"entityNames": []any{"ToDelete"},
			})

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(HaveLen(1))
		})
	})

	Describe("delete_observations", func() {
		BeforeEach(func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "Entity", "entityType": "T", "observations": []any{"keep", "remove"}},
				},
			})
		})

		It("removes specific observations", func() {
			result := callTool(ctx, session, "delete_observations", map[string]any{
				"deletions": []any{
					map[string]any{
						"entityName":   "Entity",
						"observations": []any{"remove"},
					},
				},
			})
			Expect(result.IsError).To(BeFalse())

			kg := graph.ReadGraph()
			Expect(kg.Entities[0].Observations).To(ConsistOf("keep"))
		})

		It("returns error for non-existent entity", func() {
			result := callTool(ctx, session, "delete_observations", map[string]any{
				"deletions": []any{
					map[string]any{
						"entityName":   "Ghost",
						"observations": []any{"x"},
					},
				},
			})
			Expect(result.IsError).To(BeTrue())
		})
	})

	Describe("delete_relations", func() {
		BeforeEach(func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "X", "entityType": "T", "observations": []any{}},
					map[string]any{"name": "Y", "entityType": "T", "observations": []any{}},
				},
			})
			callTool(ctx, session, "create_relations", map[string]any{
				"relations": []any{
					map[string]any{"from": "X", "to": "Y", "relationType": "linked"},
				},
			})
		})

		It("removes specified relations", func() {
			result := callTool(ctx, session, "delete_relations", map[string]any{
				"relations": []any{
					map[string]any{"from": "X", "to": "Y", "relationType": "linked"},
				},
			})
			Expect(result.IsError).To(BeFalse())

			kg := graph.ReadGraph()
			Expect(kg.Relations).To(BeEmpty())
		})

		It("persists after deletion", func() {
			callTool(ctx, session, "delete_relations", map[string]any{
				"relations": []any{
					map[string]any{"from": "X", "to": "Y", "relationType": "linked"},
				},
			})

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Relations).To(BeEmpty())
		})
	})

	Describe("read_graph", func() {
		It("returns full graph as JSON", func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "Alpha", "entityType": "T", "observations": []any{"o1"}},
				},
			})

			result := callTool(ctx, session, "read_graph", map[string]any{})
			Expect(result.IsError).To(BeFalse())

			text := extractText(result)
			var kg memory.KnowledgeGraph
			Expect(json.Unmarshal([]byte(text), &kg)).To(Succeed())
			Expect(kg.Entities).To(HaveLen(1))
			Expect(kg.Entities[0].Name).To(Equal("Alpha"))
		})
	})

	Describe("search_nodes", func() {
		BeforeEach(func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "Searchable", "entityType": "Project", "observations": []any{"unique-marker"}},
					map[string]any{"name": "Other", "entityType": "Misc", "observations": []any{"nothing"}},
				},
			})
		})

		It("finds entities matching the query", func() {
			result := callTool(ctx, session, "search_nodes", map[string]any{
				"query": "unique-marker",
			})
			Expect(result.IsError).To(BeFalse())

			text := extractText(result)
			var entities []memory.Entity
			Expect(json.Unmarshal([]byte(text), &entities)).To(Succeed())
			Expect(entities).To(HaveLen(1))
			Expect(entities[0].Name).To(Equal("Searchable"))
		})
	})

	Describe("open_nodes", func() {
		BeforeEach(func() {
			callTool(ctx, session, "create_entities", map[string]any{
				"entities": []any{
					map[string]any{"name": "Node1", "entityType": "T", "observations": []any{}},
					map[string]any{"name": "Node2", "entityType": "T", "observations": []any{}},
					map[string]any{"name": "Node3", "entityType": "T", "observations": []any{}},
				},
			})
			callTool(ctx, session, "create_relations", map[string]any{
				"relations": []any{
					map[string]any{"from": "Node1", "to": "Node2", "relationType": "linked"},
					map[string]any{"from": "Node2", "to": "Node3", "relationType": "linked"},
				},
			})
		})

		It("returns requested entities and relevant relations", func() {
			result := callTool(ctx, session, "open_nodes", map[string]any{
				"names": []any{"Node1", "Node2"},
			})
			Expect(result.IsError).To(BeFalse())

			text := extractText(result)
			var response struct {
				Entities  []memory.Entity   `json:"entities"`
				Relations []memory.Relation `json:"relations"`
			}
			Expect(json.Unmarshal([]byte(text), &response)).To(Succeed())
			Expect(response.Entities).To(HaveLen(2))
			Expect(response.Relations).To(HaveLen(1))
			Expect(response.Relations[0].From).To(Equal("Node1"))
		})
	})

	Describe("tool listing", func() {
		It("registers all 9 tools", func() {
			toolsResult, err := session.ListTools(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(toolsResult.Tools).To(HaveLen(9))

			names := make([]string, len(toolsResult.Tools))
			for i, t := range toolsResult.Tools {
				names[i] = t.Name
			}
			Expect(names).To(ConsistOf(
				"create_entities",
				"create_relations",
				"add_observations",
				"delete_entities",
				"delete_observations",
				"delete_relations",
				"read_graph",
				"search_nodes",
				"open_nodes",
			))
		})
	})
})

func callTool(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(result).NotTo(BeNil())
	return result
}

func extractText(result *mcp.CallToolResult) string {
	Expect(result.Content).NotTo(BeEmpty())
	textContent, ok := result.Content[0].(*mcp.TextContent)
	Expect(ok).To(BeTrue())
	return textContent.Text
}
