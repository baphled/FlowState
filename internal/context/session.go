package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

// SessionInfo describes a saved session's metadata.
type SessionInfo struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	AgentID        string    `json:"agent_id"`
	MessageCount   int       `json:"message_count"`
	LastActive     time.Time `json:"last_active"`
	EmbeddingModel string    `json:"embedding_model"`
	SystemPrompt   string    `json:"system_prompt"`
	LoadedSkills   []string  `json:"loaded_skills"`
}

// SessionMetadata provides additional metadata for session persistence and enrichment.
//
// This struct is used to capture and persist key session attributes, including:
//   - AgentID: The identifier of the agent handling the session.
//   - Title: The human-readable session title (may be empty; fallback logic applies).
//   - SystemPrompt: The system prompt used for the session, if any.
//   - LoadedSkills: The list of skill names loaded for this session.
type SessionMetadata struct {
	AgentID      string   // Unique identifier for the agent (may be empty)
	Title        string   // Session title (optional; fallback to previous if empty)
	SystemPrompt string   // System prompt text (optional)
	LoadedSkills []string // Names of loaded skills (optional)
}

// SessionStore defines the interface for persisting and loading sessions.
type SessionStore interface {
	// Save persists a context store to a session.
	Save(sessionID string, store *recall.FileContextStore, meta SessionMetadata) error
	// Load retrieves a context store from a saved session.
	Load(sessionID string) (*recall.FileContextStore, error)
	// List returns metadata for all saved sessions.
	List() []SessionInfo
	// SetTitle updates the title of an existing session.
	SetTitle(sessionID string, title string) error
}

// FileSessionStore implements SessionStore with JSON file persistence.
type FileSessionStore struct {
	baseDir string
}

// sessionFile represents the persisted structure of a session stored in JSON format.
type sessionFile struct {
	SessionID      string                  `json:"session_id"`
	Title          string                  `json:"title"`
	AgentID        string                  `json:"agent_id"`
	EmbeddingModel string                  `json:"embedding_model"`
	LastActive     time.Time               `json:"last_active"`
	Messages       []recall.StoredMessage  `json:"messages"`
	Embeddings     []recall.EmbeddingEntry `json:"embeddings"`
	SystemPrompt   string                  `json:"system_prompt"`
	LoadedSkills   []string                `json:"loaded_skills"`
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
//   - store is a non-nil recall.FileContextStore with messages to persist.
//   - meta is the SessionMetadata to persist (may be empty).
//
// Returns:
//   - An error if marshalling or writing the session file fails.
//
// Side effects:
//   - Writes the session data to a JSON file in the base directory.
func (s *FileSessionStore) Save(sessionID string, store *recall.FileContextStore, meta SessionMetadata) error {
	title := meta.Title
	if title == "" {
		title = s.existingTitle(sessionID)
	}
	if title == "" {
		title = GenerateTitle(store.GetStoredMessages())
	}
	sf := sessionFile{
		SessionID:      sessionID,
		Title:          title,
		AgentID:        meta.AgentID,
		EmbeddingModel: store.GetModel(),
		LastActive:     time.Now(),
		Messages:       store.GetStoredMessages(),
		Embeddings:     store.GetEmbeddings(),
		SystemPrompt:   meta.SystemPrompt,
		LoadedSkills:   meta.LoadedSkills,
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
//   - A recall.FileContextStore populated with the session's messages and embeddings.
//   - An error if the session file cannot be read or parsed.
//
// Side effects:
//   - Reads a JSON file from the base directory.
func (s *FileSessionStore) Load(sessionID string) (*recall.FileContextStore, error) {
	return s.LoadWithModel(sessionID, "")
}

// LoadWithModel retrieves a context store using the specified embedding model.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//   - model is the embedding model to use; if empty, the stored model is used.
//
// Returns:
//   - A recall.FileContextStore populated with the session's data.
//   - An error if the session file cannot be read or parsed.
//
// Side effects:
//   - Reads a JSON file from the base directory.
func (s *FileSessionStore) LoadWithModel(sessionID string, model string) (*recall.FileContextStore, error) {
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

	store := recall.NewEmptyContextStore(effectiveModel)
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
		// Skip <sessionID>.meta.json sidecars (written by
		// session.PersistSession for hierarchy-graph recovery).
		// filepath.Glob("*.json") matches both <id>.json and
		// <id>.meta.json because the wildcard crosses dots; without
		// this guard the permissive json.Unmarshal below would
		// happily decode a sidecar into a half-populated sessionFile
		// and leak into the session listing.
		if strings.HasSuffix(match, ".meta.json") {
			continue
		}

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
			Title:          sf.Title,
			AgentID:        sf.AgentID,
			MessageCount:   len(sf.Messages),
			LastActive:     sf.LastActive,
			EmbeddingModel: sf.EmbeddingModel,
			SystemPrompt:   sf.SystemPrompt,
			LoadedSkills:   sf.LoadedSkills,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastActive.After(sessions[j].LastActive)
	})

	return sessions
}

// existingTitle reads the title from an existing session file on disk.
//
// Expected:
//   - sessionID identifies a session that may or may not exist on disk.
//
// Returns:
//   - The stored title string, or "" if the file does not exist or cannot be parsed.
//
// Side effects:
//   - Reads a JSON file from the base directory.
func (s *FileSessionStore) existingTitle(sessionID string) string {
	sessionPath := filepath.Join(s.baseDir, sessionID+".json")
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return ""
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return ""
	}
	return sf.Title
}

// SetTitle updates the title of an existing session.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//   - title is the new title to set.
//
// Returns:
//   - An error if the session file cannot be read, parsed, or written.
//
// Side effects:
//   - Reads and rewrites the session JSON file with the updated title.
func (s *FileSessionStore) SetTitle(sessionID string, title string) error {
	sessionPath := filepath.Join(s.baseDir, sessionID+".json")
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return fmt.Errorf("reading session file: %w", err)
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return fmt.Errorf("unmarshalling session: %w", err)
	}
	sf.Title = title
	updated, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling session: %w", err)
	}
	return os.WriteFile(sessionPath, updated, 0o600)
}

// Delete removes the session's persisted state from disk. Both the session
// JSON file and the co-located `.events.jsonl` (plus any stale `.tmp`
// sibling) are removed so a deleted session leaves no residual artefacts
// behind.
//
// Missing files are tolerated — Delete is an idempotent housekeeping
// operation and callers should not have to pre-stat the session before
// invoking it.
//
// Expected:
//   - sessionID is a non-empty string identifying the session.
//
// Returns:
//   - An error only when an unexpected filesystem failure occurs while
//     removing an existing file.
//
// Side effects:
//   - Removes <baseDir>/<sessionID>.json when present.
//   - Removes <baseDir>/<sessionID>.events.jsonl and its `.tmp` sibling
//     when present (via session.RemoveSwarmEventsForSession).
func (s *FileSessionStore) Delete(sessionID string) error {
	sessionPath := filepath.Join(s.baseDir, sessionID+".json")
	if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing session file: %w", err)
	}
	if err := session.RemoveSwarmEventsForSession(s.baseDir, sessionID); err != nil {
		return fmt.Errorf("removing session events: %w", err)
	}
	return nil
}

// SaveEvents persists SwarmEvent entries for a session to a JSONL file
// co-located with the session data. Empty event slices produce no file.
//
// Post-P4 this routes to the compaction path — the close-time authority
// that rewrites the JSONL file atomically with fsync and rename. Per-event
// durability during streaming is handled by AppendEvent (the WAL path).
//
// Expected:
//   - sessionID is a non-empty string identifying the session.
//   - events may be nil or empty (no file is created).
//
// Returns:
//   - An error if the file cannot be written.
//
// Side effects:
//   - Writes <baseDir>/<sessionID>.events.jsonl to disk via temp-file, fsync,
//     and rename.
func (s *FileSessionStore) SaveEvents(sessionID string, events []streaming.SwarmEvent) error {
	return session.PersistSwarmEvents(s.baseDir, sessionID, events)
}

// AppendEvent writes a single event to the session's JSONL WAL and fsyncs
// the file before returning. This is the hot-path durability entry point
// used by the chat intent's persistedSwarmStore decorator (P4). Callers are
// expected to invoke it from stream worker goroutines; errors surface to
// the caller so the decorator can log them without blocking the stream.
//
// Expected:
//   - sessionID is a non-empty string identifying the session.
//   - ev is a populated SwarmEvent (producers stamp SchemaVersion and UTC
//     timestamps at creation; this method does not rewrite either).
//
// Returns:
//   - An error if the directory cannot be created, the append fails, or
//     the fsync fails.
//
// Side effects:
//   - Creates baseDir if absent.
//   - Appends one JSONL line to <baseDir>/<sessionID>.events.jsonl and
//     fsyncs the file before returning.
func (s *FileSessionStore) AppendEvent(sessionID string, ev streaming.SwarmEvent) error {
	return session.AppendSwarmEventToSession(s.baseDir, sessionID, ev)
}

// LoadEvents reads SwarmEvent entries for a session from a JSONL file.
// Returns an empty slice without error when no events file exists.
//
// Expected:
//   - sessionID is a non-empty string identifying the session.
//
// Returns:
//   - A slice of SwarmEvent entries (may be empty/nil).
//   - An error only when the file exists but cannot be read.
//
// Side effects:
//   - Reads a file from disk.
func (s *FileSessionStore) LoadEvents(sessionID string) ([]streaming.SwarmEvent, error) {
	return session.LoadSwarmEvents(s.baseDir, sessionID)
}

// GenerateTitle generates a session title from the first user message in the message list.
//
// Expected:
//   - messages is a slice of recall.StoredMessage (may be empty).
//
// Returns:
//   - A string containing the content of the first user message, truncated to 60 characters
//     with "..." appended if longer. Returns "Untitled Session" if no messages exist or
//     if no user-role message is found.
//
// Side effects:
//   - None; this is a pure function.
func GenerateTitle(messages []recall.StoredMessage) string {
	for _, msg := range messages {
		if msg.Message.Role == "user" {
			content := msg.Message.Content
			if len(content) > 60 {
				return content[:60] + "..."
			}
			return content
		}
	}
	return "Untitled Session"
}
