// Package memory provides native agent tools for querying the FlowState memory store.
//
// It exposes two tools using the OpenCode naming convention so agents can
// whitelist them via capabilities.tools without depending on an external MCP server:
//   - mcp_memory_search_nodes: semantic search for entities in the vector store.
//   - mcp_memory_open_nodes: retrieve specific entities and their relations by name.
//
// Both tools delegate to a learning.MemoryClient (backed by VectorStoreMemoryClient
// when Qdrant is configured) giving agents direct access to the real mem0/Qdrant
// store rather than the file-backed JSONL server.
package memory
