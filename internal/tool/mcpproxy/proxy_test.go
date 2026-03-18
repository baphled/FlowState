package mcpproxy_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/mcp"
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
	})

	It("satisfies the tool.Tool interface", func() {
		var _ tool.Tool = proxy
	})
})
