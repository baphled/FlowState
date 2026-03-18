package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionInfo describes a saved session's metadata.
type SessionInfo struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	MessageCount   int       `json:"message_count"`
	LastActive     time.Time `json:"last_active"`
	EmbeddingModel string    `json:"embedding_model"`
}

// SessionStore defines the interface for persisting and loading sessions.
type SessionStore interface {
	// Save persists a context store to a session.
	Save(sessionID string, store *FileContextStore) error
	// Load retrieves a context store from a saved session.
	Load(sessionID string) (*FileContextStore, error)
	// List returns metadata for all saved sessions.
	List() []SessionInfo
}

// FileSessionStore implements SessionStore with JSON file persistence.
type FileSessionStore struct {
	baseDir string
}

// sessionFile represents the persisted structure of a session stored in JSON format.
type sessionFile struct {
	SessionID      string           `json:"session_id"`
	AgentID        string           `json:"agent_id"`
	EmbeddingModel string           `json:"embedding_model"`
	LastActive     time.Time        `json:"last_active"`
	Messages       []StoredMessage  `json:"messages"`
	Embeddings     []EmbeddingEntry `json:"embeddings"`
}

// NewFileSessionStore creates a new file-based session store at the given directory.
//
// Expected:
//   - baseDir is a valid directory path.
//
// Returns:
//   - A configured FileSessionStore on success.
//   - An error if the directory cannot be created.
//
// Side effects:
//   - Creates the baseDir directory if it does not exist.
func NewFileSessionStore(baseDir string) (*FileSessionStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating session directory: %w", err)
	}
	return &FileSessionStore{baseDir: baseDir}, nil
}

// DefaultSessionStore returns a FileSessionStore in the default home directory location.
//
// Returns:
//   - A FileSessionStore configured at ~/.flowstate/sessions.
//   - An error if the home directory cannot be resolved or created.
//
// Side effects:
//   - Creates the ~/.flowstate/sessions directory if it does not exist.
func DefaultSessionStore() (*FileSessionStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	baseDir := filepath.Join(homeDir, ".flowstate", "sessions")
	return NewFileSessionStore(baseDir)
}

// Save persists a context store to a session file using atomic write.
//
// Expected:
//   - sessionID is a non-empty string identifying the session.
//   - store is a non-nil FileContextStore with messages to persist.
//
// Returns:
//   - An error if marshalling or writing the session file fails.
//
// Side effects:
//   - Writes the session data to a JSON file in the base directory.
func (s *FileSessionStore) Save(sessionID string, store *FileContextStore) error {
	sf := sessionFile{
		SessionID:      sessionID,
		AgentID:        "",
		EmbeddingModel: store.GetModel(),
		LastActive:     time.Now(),
		Messages:       store.GetStoredMessages(),
		Embeddings:     store.GetEmbeddings(),
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling session: %w", err)
	}

	sessionPath := filepath.Join(s.baseDir, sessionID+".json")
	tmpPath := sessionPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// Load retrieves a context store from a saved session using the stored embedding model.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//
// Returns:
//   - A FileContextStore populated with the session's messages and embeddings.
//   - An error if the session file cannot be read or parsed.
//
// Side effects:
//   - Reads a JSON file from the base directory.
func (s *FileSessionStore) Load(sessionID string) (*FileContextStore, error) {
	return s.LoadWithModel(sessionID, "")
}

// LoadWithModel retrieves a context store using the specified embedding model.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//   - model is the embedding model to use; if empty, the stored model is used.
//
// Returns:
//   - A FileContextStore populated with the session's data.
//   - An error if the session file cannot be read or parsed.
//
// Side effects:
//   - Reads a JSON file from the base directory.
func (s *FileSessionStore) LoadWithModel(sessionID string, model string) (*FileContextStore, error) {
	sessionPath := filepath.Join(s.baseDir, sessionID+".json")

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("unmarshalling session: %w", err)
	}

	effectiveModel := model
	if effectiveModel == "" {
		effectiveModel = sf.EmbeddingModel
	}

	store := &FileContextStore{
		path:       "",
		messages:   make([]StoredMessage, 0),
		embeddings: make([]EmbeddingEntry, 0),
		maxSize:    defaultMaxSize,
		model:      effectiveModel,
	}

	store.LoadFromSession(sf.Messages, sf.Embeddings, sf.EmbeddingModel)

	return store, nil
}

// List returns metadata for all saved sessions sorted by last active time.
//
// Returns:
//   - A slice of SessionInfo sorted by most recently active first.
//
// Side effects:
//   - Reads all JSON session files from the base directory.
func (s *FileSessionStore) List() []SessionInfo {
	pattern := filepath.Join(s.baseDir, "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return []SessionInfo{}
	}

	var sessions []SessionInfo
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			continue
		}

		var sf sessionFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}

		sessionID := strings.TrimSuffix(filepath.Base(match), ".json")
		sessions = append(sessions, SessionInfo{
			ID:             sessionID,
			AgentID:        sf.AgentID,
			MessageCount:   len(sf.Messages),
			LastActive:     sf.LastActive,
			EmbeddingModel: sf.EmbeddingModel,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastActive.After(sessions[j].LastActive)
	})

	return sessions
}
