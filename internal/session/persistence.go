package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/streaming"
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

// LoadSessionMetadata reads a single session's .meta.json sidecar
// from sessionsDir and returns the parsed Session. Returns nil and a
// nil error when the sidecar does not exist (an absence is a normal
// "this is a fresh session" signal, not an error). Corrupt files
// surface as an unmarshal error.
//
// Expected:
//   - sessionsDir is the directory containing .meta.json sidecars.
//   - sessionID is the canonical session id (no path separators).
//
// Returns:
//   - A non-nil *Session and nil error when the sidecar exists and
//     parses cleanly.
//   - nil, nil when the sidecar is absent (a fresh session).
//   - nil and a non-nil error when the file exists but is unreadable
//     or the JSON is malformed.
//
// Side effects:
//   - Reads at most one file from disk.
func LoadSessionMetadata(sessionsDir, sessionID string) (*Session, error) {
	if sessionsDir == "" || sessionID == "" {
		return nil, nil
	}
	path := filepath.Join(sessionsDir, sessionID+metaFileSuffix)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &Session{
		ID:        meta.ID,
		ParentID:  meta.ParentID,
		AgentID:   meta.AgentID,
		Status:    meta.Status,
		CreatedAt: meta.CreatedAt,
	}, nil
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

const eventsFileSuffix = ".events.jsonl"

// PersistSwarmEvents writes SwarmEvent entries to a JSONL file in sessionsDir.
// When events is empty or nil no file is created and any existing events file
// for the session is left untouched.
//
// This is the snapshot-on-save entry point preserved for callers that have
// not yet migrated to the P4 append-on-write WAL. For new call sites use
// AppendSwarmEventToSession (per-event, hot path) instead.
//
// Expected:
//   - sessionsDir is the directory to write the events file into (created if absent).
//   - sessionID is a non-empty string identifying the session.
//   - events may be nil or empty (produces no file).
//
// Returns:
//   - An error if the directory cannot be created or the file cannot be written.
//
// Side effects:
//   - Creates sessionsDir (including parents) when it does not exist.
//   - Writes <sessionsDir>/<sessionID>.events.jsonl to disk via atomic rename + fsync.
func PersistSwarmEvents(sessionsDir string, sessionID string, events []streaming.SwarmEvent) error {
	if len(events) == 0 {
		return nil
	}
	if err := os.MkdirAll(sessionsDir, 0o750); err != nil {
		return err
	}
	return streaming.CompactSwarmEvents(eventsPath(sessionsDir, sessionID), events)
}

// AppendSwarmEventToSession appends a single event to the session's JSONL
// WAL, creating the file if absent. The parent sessionsDir is created on
// first write so callers do not have to pre-create it.
//
// Durability: streaming.AppendSwarmEvent fsyncs before returning, so on
// successful return the event is on stable storage.
//
// Expected:
//   - sessionsDir is the directory containing session files.
//   - sessionID is a non-empty string identifying the session.
//   - ev is a populated SwarmEvent.
//
// Returns:
//   - An error if the directory cannot be created or the append fails.
//
// Side effects:
//   - Appends one JSONL line to <sessionsDir>/<sessionID>.events.jsonl.
//   - Fsyncs the file before returning.
func AppendSwarmEventToSession(sessionsDir string, sessionID string, ev streaming.SwarmEvent) error {
	if err := os.MkdirAll(sessionsDir, 0o750); err != nil {
		return err
	}
	return streaming.AppendSwarmEvent(eventsPath(sessionsDir, sessionID), ev)
}

// eventsPath returns the canonical events-file path for a session.
//
// Expected:
//   - sessionsDir is the directory containing session files.
//   - sessionID is a non-empty string identifying the session.
//
// Returns:
//   - <sessionsDir>/<sessionID>.events.jsonl as a filesystem-joined path.
//
// Side effects:
//   - None.
func eventsPath(sessionsDir string, sessionID string) string {
	return filepath.Join(sessionsDir, sessionID+eventsFileSuffix)
}

// RemoveSwarmEventsForSession deletes the session's JSONL events file and
// any leftover .tmp sibling. Missing files are not an error. The helper
// exists so a future session-delete code path can call a single function
// rather than reach into the streaming package directly.
//
// Expected:
//   - sessionsDir is the directory containing session files.
//   - sessionID is a non-empty string identifying the session.
//
// Returns:
//   - An error only when an unexpected filesystem failure occurs (missing
//     files are tolerated).
//
// Side effects:
//   - Deletes <sessionsDir>/<sessionID>.events.jsonl and
//     <sessionsDir>/<sessionID>.events.jsonl.tmp from disk if they exist.
func RemoveSwarmEventsForSession(sessionsDir string, sessionID string) error {
	return streaming.RemoveSwarmEvents(eventsPath(sessionsDir, sessionID))
}

// CleanupOrphanEventTmpFiles scans sessionsDir for *.events.jsonl.tmp files
// and removes them. A surviving .tmp file is always an orphan — it only
// exists as an intermediate staging file for a compact that crashed
// mid-rename. Run this at startup before any session file reads to keep
// the sessions directory tidy.
//
// Expected:
//   - sessionsDir is the directory containing session files. A non-existent
//     directory is treated as a no-op rather than an error so callers do
//     not need to pre-check.
//
// Returns:
//   - The number of .tmp files successfully removed.
//   - An error only when the directory read or a Remove call fails.
//
// Side effects:
//   - Deletes files from disk.
func CleanupOrphanEventTmpFiles(sessionsDir string) (int, error) {
	return streaming.CleanupOrphanTmpFiles(sessionsDir)
}

// LoadSwarmEvents reads SwarmEvent entries from a JSONL file in sessionsDir.
// When no events file exists for the session an empty slice is returned without
// error, providing backward compatibility with sessions created before event
// persistence was introduced.
//
// Expected:
//   - sessionsDir is the directory containing session files.
//   - sessionID is a non-empty string identifying the session.
//
// Returns:
//   - A slice of SwarmEvent entries (may be empty).
//   - An error only when the file exists but cannot be read.
//
// Side effects:
//   - Reads a file from disk.
func LoadSwarmEvents(sessionsDir string, sessionID string) ([]streaming.SwarmEvent, error) {
	path := filepath.Join(sessionsDir, sessionID+eventsFileSuffix)

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	return streaming.ReadEventsJSONL(f)
}
