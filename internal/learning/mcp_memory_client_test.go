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
})
