// Package learning provides learning entry storage for capturing agent interactions.
package learning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

// Entry represents a captured agent interaction for learning purposes.
type Entry struct {
	Timestamp   time.Time `json:"timestamp"`
	AgentID     string    `json:"agent_id"`
	UserMessage string    `json:"user_message"`
	Response    string    `json:"response"`
	ToolsUsed   []string  `json:"tools_used"`
	Outcome     string    `json:"outcome"`
}

// Store defines the interface for capturing and querying learning entries.
type Store interface {
	// Capture stores a learning entry.
	Capture(entry Entry) error
	// Query searches entries matching the given query string.
	Query(query string) []Entry
}

// JSONFileStore implements Store with JSON file persistence.
type JSONFileStore struct {
	path    string
	entries []Entry
	mu      sync.RWMutex
	bus     *eventbus.EventBus
}

// NewJSONFileStore creates a new JSONFileStore at the given path.
//
// Expected:
//   - path is a valid file path for persisting learning entries.
//
// Returns:
//   - A JSONFileStore loaded with any existing entries from the file.
//
// Side effects:
//   - Reads the existing JSON file at path if present.
func NewJSONFileStore(path string) *JSONFileStore {
	store := &JSONFileStore{
		path:    path,
		entries: make([]Entry, 0),
	}
	store.load()
	return store
}

// SetEventBus configures the event bus for emitting learning recorded events.
//
// Expected:
//   - bus is a non-nil EventBus for event emission.
//
// Returns:
//   - None.
//
// Side effects:
//   - Stores the bus reference for use in Capture.
func (s *JSONFileStore) SetEventBus(bus *eventbus.EventBus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bus = bus
}

// load reads and unmarshals learning entries from the JSON file.
//
// Expected:
//   - The file at s.path may or may not exist.
//
// Returns:
//   - None.
//
// Side effects:
//   - Populates s.entries with unmarshalled data.
//   - Silently initialises an empty slice if the file does not exist or is invalid.
func (s *JSONFileStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}

	if err := json.Unmarshal(data, &s.entries); err != nil {
		s.entries = make([]Entry, 0)
		return
	}

	if s.entries == nil {
		s.entries = make([]Entry, 0)
	}
}

// persist writes the current entries to the JSON file atomically.
//
// Expected:
//   - s.entries contains the data to persist.
//
// Returns:
//   - An error if directory creation, marshalling, or file operations fail.
//   - nil on success.
//
// Side effects:
//   - Creates the directory if it does not exist.
//   - Writes a temporary file and renames it to the target path.
func (s *JSONFileStore) persist() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// Capture stores a learning entry to the JSON file.
//
// Expected:
//   - entry is a populated Entry to persist.
//
// Returns:
//   - An error if the entry cannot be written to disk.
//
// Side effects:
//   - Appends the entry and persists the updated list to the JSON file.
//   - Emits LearningRecordedEvent if event bus is configured.
func (s *JSONFileStore) Capture(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, entry)
	if err := s.persist(); err != nil {
		return err
	}

	// Emit event if bus is configured (AC3)
	if s.bus != nil {
		event := events.NewLearningRecordedEvent(events.LearningRecordedEventData{
			AgentID:     entry.AgentID,
			UserMessage: entry.UserMessage,
			Outcome:     entry.Outcome,
			ToolsUsed:   entry.ToolsUsed,
			Timestamp:   time.Now(),
		})
		s.bus.Publish(events.EventLearningRecorded, event)
	}

	return nil
}

// Query returns all entries that contain the query string in UserMessage, Response, or Outcome.
//
// Expected:
//   - query is the substring to search for within entry fields.
//
// Returns:
//   - A slice of matching Entry values, or an empty slice if none match.
//
// Side effects:
//   - None.
func (s *JSONFileStore) Query(query string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.entries) == 0 {
		return []Entry{}
	}

	var results []Entry
	for _, entry := range s.entries {
		if strings.Contains(entry.UserMessage, query) ||
			strings.Contains(entry.Response, query) ||
			strings.Contains(entry.Outcome, query) {
			results = append(results, entry)
		}
	}

	if results == nil {
		return []Entry{}
	}

	return results
}
