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

type SessionInfo struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	MessageCount   int       `json:"message_count"`
	LastActive     time.Time `json:"last_active"`
	EmbeddingModel string    `json:"embedding_model"`
}

type SessionStore interface {
	Save(sessionID string, store *FileContextStore) error
	Load(sessionID string) (*FileContextStore, error)
	List() []SessionInfo
}

type FileSessionStore struct {
	baseDir string
}

type sessionFile struct {
	SessionID      string           `json:"session_id"`
	AgentID        string           `json:"agent_id"`
	EmbeddingModel string           `json:"embedding_model"`
	LastActive     time.Time        `json:"last_active"`
	Messages       []storedMessage  `json:"messages"`
	Embeddings     []embeddingEntry `json:"embeddings"`
}

func NewFileSessionStore(baseDir string) (*FileSessionStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating session directory: %w", err)
	}
	return &FileSessionStore{baseDir: baseDir}, nil
}

func DefaultSessionStore() (*FileSessionStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	baseDir := filepath.Join(homeDir, ".flowstate", "sessions")
	return NewFileSessionStore(baseDir)
}

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

	if err := os.WriteFile(tmpPath, data, 0o644); err != nil { //nolint:gosec // Session files need to be readable
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

func (s *FileSessionStore) Load(sessionID string) (*FileContextStore, error) {
	return s.LoadWithModel(sessionID, "")
}

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
		messages:   make([]storedMessage, 0),
		embeddings: make([]embeddingEntry, 0),
		maxSize:    defaultMaxSize,
		model:      effectiveModel,
	}

	store.LoadFromSession(sf.Messages, sf.Embeddings, sf.EmbeddingModel)

	return store, nil
}

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
