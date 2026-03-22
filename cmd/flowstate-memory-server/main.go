// Package main provides the FlowState memory MCP server.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baphled/flowstate/internal/memory"
)

func main() {
	path := os.Getenv("MEMORY_FILE_PATH")
	store := memory.NewJSONLStore(path)
	graph := memory.NewGraph()

	existing, err := store.Load()
	if err != nil {
		log.Printf("warning: loading memory: %v", err)
	} else {
		graph.CreateEntities(existing.Entities)
		graph.CreateRelations(existing.Relations)
	}

	server := mcp.NewServer(
		&mcp.Implementation{Name: "flowstate-memory-server", Version: "1.0.0"},
		nil,
	)
	memory.RegisterTools(server, graph, store)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("memory server failed: %v", err)
	}
}
