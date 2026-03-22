// Package main provides a minimal MCP memory server using the go-sdk stdio transport.
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "flowstate-memory", Version: "0.1.0"}, nil)

	type pingArgs struct{}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "health check for the memory server",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ pingArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "pong"},
			},
		}, nil, nil
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("memory server failed: %v", err)
	}
}
