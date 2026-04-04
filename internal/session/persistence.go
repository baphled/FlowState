package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const metaFileSuffix = ".meta.json"

// Metadata holds the subset of Session fields needed for persistence.
type Metadata struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id"`
	AgentID   string    `json:"agent_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// PersistSession writes session metadata to a .meta.json file in sessionsDir.
//
// Expected:
//   - sessionsDir is the directory to write the metadata file into (created if absent).
//   - sess is a non-nil Session whose ID, ParentID, AgentID, Status, and CreatedAt are persisted.
//
// Returns:
//   - An error if the directory cannot be created or the file cannot be written.
//
// Side effects:
//   - Creates sessionsDir (including parents) when it does not exist.
//   - Writes <sessionsDir>/<sess.ID>.meta.json to disk.
func PersistSession(sessionsDir string, sess *Session) error {
	meta := Metadata{
		ID:        sess.ID,
		ParentID:  sess.ParentID,
		AgentID:   sess.AgentID,
		Status:    sess.Status,
		CreatedAt: sess.CreatedAt,
	}

	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(sessionsDir, 0o750); err != nil {
		return err
	}

	path := filepath.Join(sessionsDir, sess.ID+metaFileSuffix)
	return os.WriteFile(path, data, 0o600)
}

// LoadSessionsFromDirectory scans sessionsDir for .meta.json files and returns
// the parsed sessions. Corrupt or unreadable files are silently skipped.
//
// Expected:
//   - sessionsDir is the directory to scan (may be empty or non-existent).
//
// Returns:
//   - A slice of Sessions reconstructed from the metadata files.
//   - An error only when the directory cannot be read at all (non-existent directories return an empty slice).
//
// Side effects:
//   - Reads files from disk.
func LoadSessionsFromDirectory(sessionsDir string) ([]*Session, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Session{}, nil
		}
		return nil, err
	}

	sessions := make([]*Session, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), metaFileSuffix) {
			continue
		}
		sess := loadMetaFile(filepath.Join(sessionsDir, entry.Name()))
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}

	return sessions, nil
}

// loadMetaFile reads and parses a single .meta.json file, returning nil on any error.
//
// Expected:
//   - path is the absolute or relative path to a .meta.json file.
//
// Returns:
//   - A *Session populated from the file's Metadata fields, or nil if the file cannot be read or parsed.
//
// Side effects:
//   - Reads a file from disk at path.
func loadMetaFile(path string) *Session {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}

	return &Session{
		ID:        meta.ID,
		ParentID:  meta.ParentID,
		AgentID:   meta.AgentID,
		Status:    meta.Status,
		CreatedAt: meta.CreatedAt,
	}
}
