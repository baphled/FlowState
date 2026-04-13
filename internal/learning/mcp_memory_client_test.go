package learning_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/mcp"
)

type mockMCPClient struct {
	calledWith struct {
		server string
		tool   string
		args   map[string]any
	}
	result *mcp.ToolResult
	err    error
}

func (m *mockMCPClient) Connect(ctx context.Context, config mcp.ServerConfig) error { return nil }
func (m *mockMCPClient) Disconnect(serverName string) error                         { return nil }
func (m *mockMCPClient) ListTools(ctx context.Context, serverName string) ([]mcp.ToolInfo, error) {
	return nil, nil
}
func (m *mockMCPClient) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.ToolResult, error) {
	m.calledWith.server = serverName
	m.calledWith.tool = toolName
	m.calledWith.args = args
	return m.result, m.err
}
func (m *mockMCPClient) DisconnectAll() error { return nil }

var _ = Describe("MCPMemoryClient", func() {
	var (
		client *mockMCPClient
		mem    *learning.MCPMemoryClient
	)

	BeforeEach(func() {
		client = &mockMCPClient{}
		mem = &learning.MCPMemoryClient{
			MCPClient: client,
			MCPServer: "memory",
		}
	})

	Describe("CreateEntities", func() {
		It("calls the create_entities tool and returns entities on success", func() {
			entities := []learning.Entity{{Name: "Test", EntityType: "Type", Observations: []string{"foo"}}}
			client.result = &mcp.ToolResult{Content: `{"entities":[{"name":"Test","entityType":"Type","observations":["foo"]}]}`}
			client.err = nil

			result, err := mem.CreateEntities(context.Background(), entities)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].Name).To(Equal("Test"))
			Expect(client.calledWith.tool).To(Equal("create_entities"))
			Expect(client.calledWith.args["entities"]).To(Equal([]learning.Entity{{Name: "Test", EntityType: "Type", Observations: []string{"foo"}}}))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			_, err := mem.CreateEntities(context.Background(), nil)
			Expect(err).To(MatchError("tool error"))
		})
	})

	Describe("CreateRelations", func() {
		It("calls the create_relations tool and returns relations on success", func() {
			relations := []learning.Relation{{From: "A", To: "B", RelationType: "parent"}}
			client.result = &mcp.ToolResult{Content: `{"relations":[{"from":"A","to":"B","relationType":"parent"}]}`}
			client.err = nil

			result, err := mem.CreateRelations(context.Background(), relations)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].From).To(Equal("A"))
			Expect(result[0].To).To(Equal("B"))
			Expect(result[0].RelationType).To(Equal("parent"))
			Expect(client.calledWith.tool).To(Equal("create_relations"))
			Expect(client.calledWith.args["relations"]).To(Equal(relations))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			_, err := mem.CreateRelations(context.Background(), nil)
			Expect(err).To(MatchError("tool error"))
		})

		It("returns error if MCP response is malformed", func() {
			relations := []learning.Relation{{From: "A", To: "B", RelationType: "parent"}}
			client.result = &mcp.ToolResult{Content: `{"not_relations":[]}`}
			client.err = nil

			_, err := mem.CreateRelations(context.Background(), relations)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("SearchNodes", func() {
		It("calls the search_nodes tool and returns entities on success", func() {
			client.result = &mcp.ToolResult{Content: `{"entities":[{"name":"Alice","entityType":"Person","observations":["obs1"]}]}`}
			client.err = nil

			result, err := mem.SearchNodes(context.Background(), "Alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].Name).To(Equal("Alice"))
			Expect(result[0].EntityType).To(Equal("Person"))
			Expect(result[0].Observations).To(Equal([]string{"obs1"}))
			Expect(client.calledWith.tool).To(Equal("search_nodes"))
			Expect(client.calledWith.args["query"]).To(Equal("Alice"))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			_, err := mem.SearchNodes(context.Background(), "fail")
			Expect(err).To(MatchError("tool error"))
		})

		It("returns error if MCP response is malformed", func() {
			client.result = &mcp.ToolResult{Content: `{"not_entities":[]}`}
			client.err = nil

			_, err := mem.SearchNodes(context.Background(), "Alice")
			Expect(err).To(HaveOccurred())
		})

		It("returns empty result without error when MCP returns non-JSON text such as 'undefined'", func() {
			client.result = &mcp.ToolResult{Content: `undefined`}
			client.err = nil

			result, err := mem.SearchNodes(context.Background(), "Alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})

		It("returns empty result without error when MCP returns whitespace-only content", func() {
			client.result = &mcp.ToolResult{Content: "   \n\t  "}
			client.err = nil

			result, err := mem.SearchNodes(context.Background(), "Alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})

		It("returns empty result without error when MCP returns an empty string", func() {
			client.result = &mcp.ToolResult{Content: ""}
			client.err = nil

			result, err := mem.SearchNodes(context.Background(), "Alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})

		It("still returns the IsError sentinel when MCP reports tool error", func() {
			client.result = &mcp.ToolResult{Content: "boom", IsError: true}
			client.err = nil

			_, err := mem.SearchNodes(context.Background(), "Alice")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("OpenNodes", func() {
		It("calls the open_nodes tool and returns KnowledgeGraph on success", func() {
			client.result = &mcp.ToolResult{Content: `{"entities":[{"name":"Alice","entityType":"Person","observations":["obs1"]}],"relations":[{"from":"Alice","to":"Bob","relationType":"friend"}]}`}
			client.err = nil

			result, err := mem.OpenNodes(context.Background(), []string{"Alice", "Bob"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Entities).To(HaveLen(1))
			Expect(result.Entities[0].Name).To(Equal("Alice"))
			Expect(result.Entities[0].EntityType).To(Equal("Person"))
			Expect(result.Entities[0].Observations).To(Equal([]string{"obs1"}))
			Expect(result.Relations).To(HaveLen(1))
			Expect(result.Relations[0].From).To(Equal("Alice"))
			Expect(result.Relations[0].To).To(Equal("Bob"))
			Expect(result.Relations[0].RelationType).To(Equal("friend"))
			Expect(client.calledWith.tool).To(Equal("open_nodes"))
			Expect(client.calledWith.args["names"]).To(Equal([]string{"Alice", "Bob"}))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			_, err := mem.OpenNodes(context.Background(), []string{"Alice"})
			Expect(err).To(MatchError("tool error"))
		})

		It("returns error if MCP response is malformed", func() {
			client.result = &mcp.ToolResult{Content: `{"not_entities":[],"not_relations":[]}`}
			client.err = nil

			_, err := mem.OpenNodes(context.Background(), []string{"Alice"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("AddObservations", func() {
		It("calls the add_observations tool and returns entries on success", func() {
			observations := []learning.ObservationEntry{{EntityName: "Alice", Contents: []string{"likes Go"}}}
			client.result = &mcp.ToolResult{Content: `{"observations":[{"entityName":"Alice","contents":["likes Go"]}]}`}
			client.err = nil

			result, err := mem.AddObservations(context.Background(), observations)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].EntityName).To(Equal("Alice"))
			Expect(result[0].Contents).To(Equal([]string{"likes Go"}))
			Expect(client.calledWith.tool).To(Equal("add_observations"))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			_, err := mem.AddObservations(context.Background(), nil)
			Expect(err).To(MatchError("tool error"))
		})
	})

	Describe("DeleteEntities", func() {
		It("calls the delete_entities tool and returns deleted names on success", func() {
			client.result = &mcp.ToolResult{Content: `{"deleted":["Alice","Bob"]}`}
			client.err = nil

			result, err := mem.DeleteEntities(context.Background(), []string{"Alice", "Bob"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal([]string{"Alice", "Bob"}))
			Expect(client.calledWith.tool).To(Equal("delete_entities"))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			_, err := mem.DeleteEntities(context.Background(), nil)
			Expect(err).To(MatchError("tool error"))
		})
	})

	Describe("DeleteObservations", func() {
		It("calls the delete_observations tool on success", func() {
			deletions := []learning.DeletionEntry{{EntityName: "Alice", Observations: []string{"old fact"}}}
			client.result = &mcp.ToolResult{Content: `{}`}
			client.err = nil

			err := mem.DeleteObservations(context.Background(), deletions)
			Expect(err).NotTo(HaveOccurred())
			Expect(client.calledWith.tool).To(Equal("delete_observations"))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			err := mem.DeleteObservations(context.Background(), nil)
			Expect(err).To(MatchError("tool error"))
		})
	})

	Describe("DeleteRelations", func() {
		It("calls the delete_relations tool on success", func() {
			relations := []learning.Relation{{From: "A", To: "B", RelationType: "friend"}}
			client.result = &mcp.ToolResult{Content: `{}`}
			client.err = nil

			err := mem.DeleteRelations(context.Background(), relations)
			Expect(err).NotTo(HaveOccurred())
			Expect(client.calledWith.tool).To(Equal("delete_relations"))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			err := mem.DeleteRelations(context.Background(), nil)
			Expect(err).To(MatchError("tool error"))
		})
	})

	Describe("ReadGraph", func() {
		It("calls the read_graph tool and returns full KnowledgeGraph on success", func() {
			client.result = &mcp.ToolResult{Content: `{"entities":[{"name":"Alice","entityType":"Person","observations":["obs1"]}],"relations":[{"from":"Alice","to":"Bob","relationType":"friend"}]}`}
			client.err = nil

			result, err := mem.ReadGraph(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Entities).To(HaveLen(1))
			Expect(result.Relations).To(HaveLen(1))
			Expect(client.calledWith.tool).To(Equal("read_graph"))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			_, err := mem.ReadGraph(context.Background())
			Expect(err).To(MatchError("tool error"))
		})
	})

	Describe("WriteLearningRecord", func() {
		It("creates an entity from the record via CreateEntities", func() {
			client.result = &mcp.ToolResult{Content: `{"entities":[{"name":"agent-1","entityType":"learning-record","observations":["Outcome: success"]}]}`}
			client.err = nil

			record := &learning.Record{AgentID: "agent-1", ToolsUsed: []string{"bash"}, Outcome: "success"}
			err := mem.WriteLearningRecord(record)
			Expect(err).NotTo(HaveOccurred())
			Expect(client.calledWith.tool).To(Equal("create_entities"))
		})

		It("returns error if MCP tool call fails", func() {
			client.err = errors.New("tool error")
			record := &learning.Record{AgentID: "agent-1", Outcome: "fail"}
			err := mem.WriteLearningRecord(record)
			Expect(err).To(MatchError("tool error"))
		})
	})
})
