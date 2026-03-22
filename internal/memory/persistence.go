package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const defaultMemoryFile = "memory.jsonl"

const (
	recordTypeEntity   = "entity"
	recordTypeRelation = "relation"
)

// JSONLStore persists a KnowledgeGraph to a JSONL file.
//
// Each line in the file is a JSON object representing either
// an entity or a relation, distinguished by a "type" field.
//
// Expected:
//   - Initialised via NewJSONLStore.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None (methods have individual side effects).
type JSONLStore struct {
	path string
}

// entityRecord is the JSONL representation of an Entity.
//
// Expected:
//   - Type is always "entity".
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type entityRecord struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	EntityType   string   `json:"entityType"`
	Observations []string `json:"observations"`
}

// relationRecord is the JSONL representation of a Relation.
//
// Expected:
//   - Type is always "relation".
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type relationRecord struct {
	Type         string `json:"type"`
	From         string `json:"from"`
	To           string `json:"to"`
	RelationType string `json:"relationType"`
}

// NewJSONLStore creates a new JSONLStore with the given file path.
//
// If path is empty, the MEMORY_FILE_PATH environment variable is used.
// If both are empty, defaults to "memory.jsonl".
//
// Expected:
//   - path is a valid filesystem path or empty string.
//
// Returns:
//   - *JSONLStore configured with the resolved path.
//
// Side effects:
//   - Reads the MEMORY_FILE_PATH environment variable when path is empty.
func NewJSONLStore(path string) *JSONLStore {
	if path == "" {
		path = os.Getenv("MEMORY_FILE_PATH")
	}
	if path == "" {
		path = defaultMemoryFile
	}
	return &JSONLStore{path: filepath.Clean(path)}
}

// Load reads the JSONL file and returns the KnowledgeGraph.
//
// Returns an empty graph if the file does not exist.
// Malformed JSON lines are silently skipped.
//
// Expected:
//   - The store has been initialised via NewJSONLStore.
//
// Returns:
//   - *KnowledgeGraph containing all valid entities and relations.
//   - error if the file exists but cannot be opened.
//
// Side effects:
//   - Reads from the filesystem.
func (s *JSONLStore) Load() (*KnowledgeGraph, error) {
	file, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return emptyGraph(), nil
		}
		return nil, fmt.Errorf("opening memory file: %w", err)
	}
	defer file.Close()

	return parseJSONLFile(file)
}

// Save writes the KnowledgeGraph to the JSONL file atomically.
//
// Uses a temporary file and rename to prevent corruption.
// Creates parent directories if they do not exist.
//
// Expected:
//   - graph is a valid KnowledgeGraph pointer.
//
// Returns:
//   - error if the write or rename fails.
//
// Side effects:
//   - Writes to the filesystem.
//   - Creates parent directories if they do not exist.
func (s *JSONLStore) Save(graph *KnowledgeGraph) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, "*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if err := writeGraphToFile(tmpFile, graph); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// parseJSONLFile reads entities and relations from an open JSONL file.
//
// Expected:
//   - file is an open *os.File positioned at the start.
//
// Returns:
//   - *KnowledgeGraph containing parsed entities and relations.
//   - error only for scanner errors; malformed lines are skipped.
//
// Side effects:
//   - Reads from the file.
func parseJSONLFile(file *os.File) (*KnowledgeGraph, error) {
	graph := emptyGraph()
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		addRecordToGraph(graph, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading memory file: %w", err)
	}

	return graph, nil
}

// addRecordToGraph parses a single JSONL line and appends to the graph.
//
// Malformed lines or lines with unknown type are silently skipped.
//
// Expected:
//   - line is a raw JSON byte slice.
//   - graph is a valid KnowledgeGraph pointer.
//
// Returns:
//   - (nothing)
//
// Side effects:
//   - Appends to graph.Entities or graph.Relations on success.
func addRecordToGraph(graph *KnowledgeGraph, line []byte) {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &probe) != nil {
		return
	}

	switch probe.Type {
	case recordTypeEntity:
		addEntityRecord(graph, line)
	case recordTypeRelation:
		addRelationRecord(graph, line)
	}
}

// addEntityRecord parses an entity record and appends it to the graph.
//
// Expected:
//   - line is valid JSON representing an entityRecord.
//
// Returns:
//   - (nothing)
//
// Side effects:
//   - Appends to graph.Entities on success.
func addEntityRecord(graph *KnowledgeGraph, line []byte) {
	var rec entityRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	if rec.Observations == nil {
		rec.Observations = []string{}
	}
	graph.Entities = append(graph.Entities, Entity{
		Name:         rec.Name,
		EntityType:   rec.EntityType,
		Observations: rec.Observations,
	})
}

// addRelationRecord parses a relation record and appends it to the graph.
//
// Expected:
//   - line is valid JSON representing a relationRecord.
//
// Returns:
//   - (nothing)
//
// Side effects:
//   - Appends to graph.Relations on success.
func addRelationRecord(graph *KnowledgeGraph, line []byte) {
	var rec relationRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	graph.Relations = append(graph.Relations, Relation{
		From:         rec.From,
		To:           rec.To,
		RelationType: rec.RelationType,
	})
}

// writeGraphToFile writes all entities and relations as JSONL to the file.
//
// Expected:
//   - file is an open, writable *os.File.
//   - graph is a valid KnowledgeGraph pointer.
//
// Returns:
//   - error if JSON marshalling or writing fails.
//
// Side effects:
//   - Writes JSONL lines to the file.
func writeGraphToFile(file *os.File, graph *KnowledgeGraph) error {
	encoder := json.NewEncoder(file)

	for _, e := range graph.Entities {
		rec := entityRecord{
			Type:         recordTypeEntity,
			Name:         e.Name,
			EntityType:   e.EntityType,
			Observations: e.Observations,
		}
		if err := encoder.Encode(rec); err != nil {
			return fmt.Errorf("encoding entity %s: %w", e.Name, err)
		}
	}

	for _, r := range graph.Relations {
		rec := relationRecord{
			Type:         recordTypeRelation,
			From:         r.From,
			To:           r.To,
			RelationType: r.RelationType,
		}
		if err := encoder.Encode(rec); err != nil {
			return fmt.Errorf("encoding relation %s->%s: %w", r.From, r.To, err)
		}
	}

	return nil
}

// emptyGraph returns a KnowledgeGraph with initialised empty slices.
//
// Returns:
//   - *KnowledgeGraph with non-nil empty Entities and Relations.
//
// Side effects:
//   - None.
func emptyGraph() *KnowledgeGraph {
	return &KnowledgeGraph{
		Entities:  []Entity{},
		Relations: []Relation{},
	}
}
