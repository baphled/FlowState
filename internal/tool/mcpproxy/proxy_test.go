package mcpproxy_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/mcp"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/mcpproxy"
)

type mockClient struct {
	callToolFn func(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.ToolResult, error)
}

func (m *mockClient) Connect(_ context.Context, _ mcp.ServerConfig) error {
	return nil
}

func (m *mockClient) Disconnect(_ string) error {
	return nil
}

func (m *mockClient) ListTools(_ context.Context, _ string) ([]mcp.ToolInfo, error) {
	return nil, nil
}

func (m *mockClient) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.ToolResult, error) {
	if m.callToolFn != nil {
		return m.callToolFn(ctx, serverName, toolName, args)
	}
	return &mcp.ToolResult{Content: "", IsError: false}, nil
}

func (m *mockClient) DisconnectAll() error {
	return nil
}

var _ = Describe("Proxy", func() {
	var (
		client     *mockClient
		proxy      *mcpproxy.Proxy
		serverName string
		toolInfo   mcp.ToolInfo
	)

	BeforeEach(func() {
		client = &mockClient{}
		serverName = "test-server"
		toolInfo = mcp.ToolInfo{
			Name:        "echo",
			Description: "Echoes input back",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The message to echo",
					},
				},
				"required": []interface{}{"message"},
			},
		}
		proxy = mcpproxy.NewProxy(client, serverName, toolInfo)
	})

	Describe("Name", func() {
		It("returns the tool name from ToolInfo", func() {
			Expect(proxy.Name()).To(Equal("echo"))
		})
	})

	Describe("Description", func() {
		It("returns the tool description from ToolInfo", func() {
			Expect(proxy.Description()).To(Equal("Echoes input back"))
		})
	})

	Describe("Schema", func() {
		It("returns a schema with properties from InputSchema", func() {
			schema := proxy.Schema()
			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Properties).To(HaveKey("message"))
			Expect(schema.Properties["message"].Type).To(Equal("string"))
			Expect(schema.Properties["message"].Description).To(Equal("The message to echo"))
			Expect(schema.Required).To(ContainElement("message"))
		})

		Context("when InputSchema is nil", func() {
			BeforeEach(func() {
				toolInfo = mcp.ToolInfo{
					Name:        "no-schema",
					Description: "Tool without schema",
					InputSchema: nil,
				}
				proxy = mcpproxy.NewProxy(client, serverName, toolInfo)
			})

			It("returns an empty schema", func() {
				schema := proxy.Schema()
				Expect(schema.Type).To(Equal("object"))
				Expect(schema.Properties).To(BeEmpty())
				Expect(schema.Required).To(BeEmpty())
			})
		})
	})

	Describe("Execute", func() {
		It("delegates to the MCP client CallTool", func() {
			var capturedServer, capturedTool string
			var capturedArgs map[string]any
			client.callToolFn = func(_ context.Context, sn, tn string, args map[string]any) (*mcp.ToolResult, error) {
				capturedServer = sn
				capturedTool = tn
				capturedArgs = args
				return &mcp.ToolResult{Content: "hello", IsError: false}, nil
			}

			input := tool.Input{
				Name:      "echo",
				Arguments: map[string]interface{}{"message": "hello"},
			}
			result, err := proxy.Execute(context.Background(), input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(Equal("hello"))
			Expect(result.Error).ToNot(HaveOccurred())
			Expect(capturedServer).To(Equal("test-server"))
			Expect(capturedTool).To(Equal("echo"))
			Expect(capturedArgs).To(HaveKeyWithValue("message", "hello"))
		})

		Context("when CallTool returns an error", func() {
			It("returns the error", func() {
				client.callToolFn = func(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
					return nil, errors.New("connection lost")
				}

				input := tool.Input{
					Name:      "echo",
					Arguments: map[string]interface{}{"message": "hello"},
				}
				_, err := proxy.Execute(context.Background(), input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("connection lost"))
			})
		})

		Context("when CallTool returns IsError true", func() {
			It("returns the content as error in Result", func() {
				client.callToolFn = func(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
					return &mcp.ToolResult{Content: "tool failed", IsError: true}, nil
				}

				input := tool.Input{
					Name:      "echo",
					Arguments: map[string]interface{}{"message": "hello"},
				}
				result, err := proxy.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("tool failed"))
				Expect(result.Error).To(HaveOccurred())
				Expect(result.Error.Error()).To(ContainSubstring("tool failed"))
			})
		})

		// Bug #34 — MCP tool output bypasses the shared truncation envelope.
		// Native tools (bash, read, grep, ls) all route their Content
		// through truncate.Apply so a runaway 300KB tool result spills
		// to a session-scoped overflow file and the assistant sees a
		// short recovery hint instead of a swollen JSON blob in the
		// messages array. mcpproxy.Execute did NOT call truncate.Apply
		// before this fix, so a single `read_graph` from the mem0 MCP
		// returning a knowledge graph landed raw on the provider
		// request and ate context-window budget on every subsequent
		// turn. This Describe block pins the post-fix contract: every
		// MCP tool output flows through the same envelope as native
		// tools, by default, with no opt-in.
		Describe("output truncation (Bug #34)", func() {
			It("truncates over-cap MCP tool output and writes a spill file", func() {
				// 60KB of newline-separated lines — comfortably over the
				// 50KB default byte cap. The truncate envelope must
				// slice this to fit and append a recovery hint.
				bigPayload := strings.Repeat("knowledge-graph-line\n", 3000)
				Expect(len(bigPayload)).To(BeNumerically(">", 50*1024))

				client.callToolFn = func(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
					return &mcp.ToolResult{Content: bigPayload, IsError: false}, nil
				}

				ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-mcp-bug34")
				input := tool.Input{Name: "read_graph", Arguments: map[string]any{}}
				result, err := proxy.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(len(result.Output)).To(BeNumerically("<", len(bigPayload)),
					"MCP output above the truncation budget must be sliced, not delivered raw")
				Expect(result.Output).To(ContainSubstring("truncated"),
					"the truncate envelope appends a recovery hint mentioning the truncation")
				Expect(result.Output).To(ContainSubstring("Full output saved to:"),
					"the spill file path must be included in the hint so the agent can recover ranges")
			})

			It("leaves small MCP tool output unchanged", func() {
				const small = "tiny mcp response payload"
				client.callToolFn = func(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
					return &mcp.ToolResult{Content: small, IsError: false}, nil
				}

				ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-mcp-small")
				input := tool.Input{Name: "echo", Arguments: map[string]any{}}
				result, err := proxy.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal(small),
					"under-cap MCP output must pass through verbatim (no spurious hint)")
			})

			It("truncates over-cap MCP error output too", func() {
				// IsError responses also flow through truncate so an
				// MCP server that returns a stack trace dump does not
				// blow the context window on the failure path either.
				bigErr := strings.Repeat("ERR: traceback frame\n", 3000)
				client.callToolFn = func(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
					return &mcp.ToolResult{Content: bigErr, IsError: true}, nil
				}

				ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-mcp-err")
				input := tool.Input{Name: "broken_tool", Arguments: map[string]any{}}
				result, err := proxy.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred(),
					"IsError must still surface as result.Error after truncation")
				Expect(len(result.Output)).To(BeNumerically("<", len(bigErr)),
					"oversized error output must also be sliced through the truncate envelope")
				Expect(result.Output).To(ContainSubstring("truncated"))
			})
		})
	})

	It("satisfies the tool.Tool interface", func() {
		var _ tool.Tool = proxy
	})
})
