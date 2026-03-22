# FlowState Memory Server

A Go-based knowledge graph server implementing the [Model Context Protocol (MCP)](https://modelcontextprotocol.io). This package provides a persistent, searchable memory layer for AI agents, allowing them to store and retrieve entities and relationships within a structured knowledge graph.

## Overview

The `memory` package provides a standalone MCP server that manages a knowledge graph using a local JSONL file for persistence. It enables AI agents to "remember" discoveries, link related concepts, and maintain long-term context across different sessions.

## Requirements

- **Go**: 1.22 or later
- **MCP SDK**: `github.com/modelcontextprotocol/go-sdk` (v1.4.1)

## Data Model

The server organises information into entities and relations. 

### Entities

An entity represents a node in the graph, such as a person, place, project, or concept.

```go
type Entity struct {
	Name         string   `json:"name"`
	EntityType   string   `json:"entityType"`
	Observations []string `json:"observations"`
}
```

**JSON Example:**
```json
{
  "name": "FlowState",
  "entityType": "Project",
  "observations": [
    "A general-purpose AI assistant TUI",
    "Uses Bubble Tea for the interface",
    "Supports Model Context Protocol"
  ]
}
```

### Relations

A relation represents a directed link between two existing entities.

```go
type Relation struct {
	From         string `json:"from"`
	To           string `json:"to"`
	RelationType string `json:"relationType"`
}
```

**JSON Example:**
```json
{
  "from": "FlowState",
  "to": "MCP",
  "relationType": "implements"
}
```

### Knowledge Graph

The complete graph consists of a collection of entities and their relationships.

```go
type KnowledgeGraph struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}
```

## Tool Schemas

The server exposes nine tools for interacting with the knowledge graph. All communication follows the MCP standard over `stdio` transport.

### `create_entities`

Creates new entities in the knowledge graph.

**Input:**
```json
{
  "entities": [
    {
      "name": "Golang",
      "entityType": "Language",
      "observations": ["Created by Google", "Strongly typed"]
    }
  ]
}
```

**Output:**
```json
{
  "entities": [
    {
      "name": "Golang",
      "entityType": "Language",
      "observations": ["Created by Google", "Strongly typed"]
    }
  ]
}
```

### `create_relations`

Establishes relationships between existing entities.

**Input:**
```json
{
  "relations": [
    {
      "from": "FlowState",
      "to": "Golang",
      "relationType": "written_in"
    }
  ]
}
```

**Output:**
```json
{
  "relations": [
    {
      "from": "FlowState",
      "to": "Golang",
      "relationType": "written_in"
    }
  ]
}
```

### `add_observations`

Appends new observations to an existing entity.

**Input:**
```json
{
  "observations": [
    {
      "entityName": "FlowState",
      "contents": ["Supports local memory via JSONL"]
    }
  ]
}
```

**Output:**
```json
{
  "observations": [
    {
      "entityName": "FlowState",
      "contents": ["A general-purpose AI assistant TUI", "Uses Bubble Tea for the interface", "Supports Model Context Protocol", "Supports local memory via JSONL"]
    }
  ]
}
```

### `delete_entities`

Removes entities from the graph by name. This operation cascades, automatically deleting any relations associated with the removed entities.

**Input:**
```json
{
  "entityNames": ["Golang"]
}
```

**Output:**
```json
{
  "status": "success",
  "deleted": ["Golang"]
}
```

### `delete_observations`

Removes specific observations from an entity.

**Input:**
```json
{
  "deletions": [
    {
      "entityName": "FlowState",
      "observations": ["Uses Bubble Tea for the interface"]
    }
  ]
}
```

**Output:**
```json
{
  "entityName": "FlowState",
  "observations": ["A general-purpose AI assistant TUI", "Supports Model Context Protocol", "Supports local memory via JSONL"]
}
```

### `delete_relations`

Removes specific relationships between entities.

**Input:**
```json
{
  "relations": [
    {
      "from": "FlowState",
      "to": "Golang",
      "relationType": "written_in"
    }
  ]
}
```

**Output:**
```json
{
  "status": "success"
}
```

### `read_graph`

Returns the entire knowledge graph, including all entities and relations.

**Input:**
`{}`

**Output:**
```json
{
  "entities": [...],
  "relations": [...]
}
```

### `search_nodes`

Performs a full-text search across all entity names, types, and observations.

**Input:**
```json
{
  "query": "protocol"
}
```

**Output:**
```json
{
  "entities": [
    {
      "name": "FlowState",
      "entityType": "Project",
      "observations": ["Supports Model Context Protocol", ...]
    }
  ]
}
```

### `open_nodes`

Retrieves specific entities by name, including their associated relations.

**Input:**
```json
{
  "names": ["FlowState"]
}
```

**Output:**
```json
{
  "entities": [...],
  "relations": [...]
}
```

## Persistence

The server uses a **JSONL (JSON Lines)** file format for persistence. This ensures that:
- Each entity or relation occupies a single line.
- Writes are atomic to prevent data corruption.
- The file remains human-readable and easy to parse.

The default serialisation behaviour ensures that the graph is always synchronised with the underlying storage.

## Configuration

The server is configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMORY_FILE_PATH` | Path to the JSONL persistence file | `memory.jsonl` |

## Quick Start

1. **Build the server binary**:
   ```bash
   go build -o flowstate-memory-server ./internal/memory/main.go
   ```

2. **Run the server** (defaults to stdio transport):
   ```bash
   export MEMORY_FILE_PATH="my_memory.jsonl"
   ./flowstate-memory-server
   ```

3. **Integrate with FlowState**:
   Add the following to your `config.yaml`:
   ```yaml
   mcp_servers:
     - name: "memory"
       command: "/path/to/flowstate-memory-server"
       enabled: true
   ```

## Extraction

This package is designed to be modular. To extract it into a standalone Go module:

1. Copy the `internal/memory/` directory to a new location.
2. Initialise a new module: `go mod init <your-module-name>`.
3. Tidy the dependencies: `go mod tidy`.
4. Ensure you have the `github.com/modelcontextprotocol/go-sdk` dependency.

The core logic is decoupled from the main FlowState application, making it suitable for use as a generic knowledge graph MCP server in other projects.
