package mcp_test

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SDK Proof", func() {
	It("connects, lists tools, calls a tool, disconnects", func() {
		ctx := context.Background()

		// 1. Create in-memory transport pair
		clientTransport, serverTransport := mcp.NewInMemoryTransports()

		// 2. Start a minimal MCP server on the server transport
		server := mcp.NewServer(&mcp.Implementation{
			Name:    "test-server",
			Version: "1.0.0",
		}, nil)

		// Register a simple echo tool using the generic AddTool
		type EchoInput struct {
			Message string `json:"message" jsonschema:"Message to echo"`
		}
		type EchoOutput struct {
			Result string `json:"result"`
		}

		mcp.AddTool(server, &mcp.Tool{
			Name:        "echo",
			Description: "Echo the input message",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args EchoInput) (*mcp.CallToolResult, EchoOutput, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "Echo: " + args.Message},
				},
			}, EchoOutput{Result: args.Message}, nil
		})

		// Start server in background
		serverErr := make(chan error, 1)
		go func() {
			serverErr <- server.Run(ctx, serverTransport)
		}()

		// 3. Connect client to client transport
		client := mcp.NewClient(&mcp.Implementation{
			Name:    "test-client",
			Version: "1.0.0",
		}, nil)

		session, err := client.Connect(ctx, clientTransport, nil)
		Expect(err).NotTo(HaveOccurred())
		defer session.Close()

		// 4. Call ListTools — expect 1 tool
		toolsResult, err := session.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(toolsResult.Tools).To(HaveLen(1))
		Expect(toolsResult.Tools[0].Name).To(Equal("echo"))

		// 5. Call CallTool("echo", {"message": "hello"}) — expect result
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "echo",
			Arguments: map[string]any{
				"message": "hello",
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Content).To(HaveLen(1))

		textContent, ok := result.Content[0].(*mcp.TextContent)
		Expect(ok).To(BeTrue())
		Expect(textContent.Text).To(ContainSubstring("Echo: hello"))

		// 6. Close
		session.Close()

		// Verify server shuts down cleanly
		Eventually(serverErr).Should(Receive())
	})
})
