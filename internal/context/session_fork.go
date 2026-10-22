package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/recall"
)

// forkedSessionFile extends the on-disk session shape with fork-lineage
// metadata. New fields are `omitempty` so legacy session files continue to
// round-trip through sessionFile without regression.
//
// Fork metadata captured per P18b:
//
//   - ParentSessionID: the origin session the fork was cloned from.
//   - PivotMessageID: the StoredMessage.ID at which the clone was truncated.
//     Empty when the fork was a full clone (no truncation).
//   - ForkedAt: UTC timestamp of the fork call, so downstream tooling can
//     reason about when the branch happened relative to the origin's
//     activity timeline.
type forkedSessionFile struct {
	sessionFile
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	PivotMessageID  string    `json:"pivot_message_id,omitempty"`
	ForkedAt        time.Time `json:"forked_at,omitempty"`
}

// ErrForkPivotNotFound is returned when the caller asks Fork to truncate at
// a StoredMessage.ID that is not present in the origin session.
var ErrForkPivotNotFound = errors.New("fork pivot message not found in origin session")

// Fork clones the origin session into a new session whose history is a
// copy of the origin's messages up to and including pivotMessageID. The
// new session has a fresh UUID, records the origin's ID under
// parent_session_id, and records pivotMessageID so the lineage is
// discoverable from disk.
//
// Behaviour:
//
//   - When pivotMessageID is empty the fork is a full clone — every
//     message, embedding and event in the origin is copied.
//   - When pivotMessageID matches a StoredMessage.ID in the origin, the
//     fork contains messages[0..pivot] inclusive. Embeddings pointing at
//     copied messages are retained; the remainder are dropped so the fork
//     is not polluted by references to truncated messages.
//   - The origin session's `.events.jsonl` WAL is byte-copied onto the
//     fork. The full-clone approximation is intentional for P18b — see
//     the accompanying scope cliff note in the tests. Subsequent writes
//     to either WAL are fully isolated.
//   - The fork's title is derived from the origin's title (with a
//     "(fork)" suffix) so it is easy to spot in the session browser.
//   - When the origin already carries a ParentSessionID (i.e. a fork of
//     a fork) the new session inherits the original ParentSessionID —
//     fork lineage is linearised rather than nested — and the
//     PivotMessageID points at the new slice boundary. This keeps the
//     parent-pointer semantics single-hop and matches the shape surfaced
//     to the TUI today.
//
// Expected:
//   - originID is a non-empty string matching an existing session file.
//   - pivotMessageID is either empty (full clone) or the ID of a
//     StoredMessage present in the origin's message history.
//
// Returns:
//   - The newly generated session ID on success.
//   - An error when the origin cannot be read, the pivot does not exist,
//     or persistence of the fork fails.
//
// Side effects:
//   - Reads <baseDir>/<originID>.json and, when present,
//     <baseDir>/<originID>.events.jsonl.
//   - Writes <baseDir>/<newID>.json and, when the origin had a WAL,
//     <baseDir>/<newID>.events.jsonl.
func (s *FileSessionStore) Fork(originID, pivotMessageID string) (string, error) {
	origin, err := s.loadSessionFile(originID)
	if err != nil {
		return "", fmt.Errorf("loading origin session: %w", err)
	}

	messages, embeddings, err := truncateAtPivot(origin.Messages, origin.Embeddings, pivotMessageID)
	if err != nil {
		return "", err
	}

	newID := uuid.New().String()
	parent := origin.SessionID
	// Linearise lineage: a fork-of-a-fork still points at the original
	// root rather than nesting parent pointers. This keeps the TUI side
	// simple — it only ever has one hop to traverse.
	if rooted, ok := s.readExistingParent(originID); ok && rooted != "" {
		parent = rooted
	}

	fork := forkedSessionFile{
		sessionFile: sessionFile{
			SessionID:      newID,
			Title:          forkTitle(origin.Title),
			AgentID:        origin.AgentID,
			EmbeddingModel: origin.EmbeddingModel,
			LastActive:     time.Now().UTC(),
			Messages:       messages,
			Embeddings:     embeddings,
			SystemPrompt:   origin.SystemPrompt,
			LoadedSkills:   origin.LoadedSkills,
		},
		ParentSessionID: parent,
		PivotMessageID:  pivotMessageID,
		ForkedAt:        time.Now().UTC(),
	}

	if err := s.writeForkFile(newID, fork); err != nil {
		return "", err
	}

	if err := s.copyEventsWAL(originID, newID); err != nil {
		return "", fmt.Errorf("copying events WAL: %w", err)
	}

	return newID, nil
}

// loadSessionFile reads the raw on-disk session JSON for the given ID.
// Unlike Load it does not hydrate a recall.FileContextStore — Fork needs
// the unmarshalled StoredMessage + EmbeddingEntry slices verbatim so it
// can slice them at the pivot without losing metadata.
//
// Expected:
//   - sessionID identifies an existing <baseDir>/<sessionID>.json file.
//
// Returns:
//   - The parsed sessionFile on success.
//   - An error if the file cannot be read or parsed.
//
// Side effects:
//   - Reads a file from disk.
func (s *FileSessionStore) loadSessionFile(sessionID string) (sessionFile, error) {
	path := filepath.Join(s.baseDir, sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, fmt.Errorf("reading session file: %w", err)
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return sessionFile{}, fmt.Errorf("unmarshalling session: %w", err)
	}
	return sf, nil
}

// readExistingParent peeks at a session's parent_session_id field without
// failing when the field is absent. It exists so Fork can linearise
// fork-of-fork lineage against the original root.
//
// Expected:
//   - sessionID identifies a session file on disk.
//
// Returns:
//   - The stored parent_session_id and true when a non-empty pointer is
//     found; ("", false) otherwise.
//
// Side effects:
//   - Reads a file from disk.
func (s *FileSessionStore) readExistingParent(sessionID string) (string, bool) {
	path := filepath.Join(s.baseDir, sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var raw struct {
		ParentSessionID string `json:"parent_session_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", false
	}
	return raw.ParentSessionID, raw.ParentSessionID != ""
}

// writeForkFile marshals the fork payload and atomically places it under
// <baseDir>/<newID>.json.
//
// Expected:
//   - newID is a fresh UUID string for the fork session.
//   - fork carries a fully populated forkedSessionFile.
//
// Returns:
//   - An error if marshalling or file I/O fails.
//
// Side effects:
//   - Writes a JSON file via temp + rename atomically.
func (s *FileSessionStore) writeForkFile(newID string, fork forkedSessionFile) error {
	data, err := json.MarshalIndent(fork, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling fork session: %w", err)
	}
	sessionPath := filepath.Join(s.baseDir, newID+".json")
	tmpPath := sessionPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// truncateAtPivot returns the message/embedding slices for a fork given
// the caller-supplied pivotMessageID. An empty pivot is a full clone; an
// unknown pivot returns ErrForkPivotNotFound so the caller can surface
// the error to the UI.
//
// Expected:
//   - messages is the origin session's full StoredMessage history.
//   - embeddings is the origin session's full EmbeddingEntry list.
//   - pivotMessageID is either empty or a StoredMessage.ID present in messages.
//
// Returns:
//   - The truncated message slice (up to and including the pivot).
//   - The embedding slice filtered to only cover retained messages.
//   - ErrForkPivotNotFound when the pivot is non-empty and not present.
//
// Side effects:
//   - None. Returns fresh slices so the caller can mutate without
//     aliasing into origin state.
func truncateAtPivot(
	messages []recall.StoredMessage,
	embeddings []recall.EmbeddingEntry,
	pivotMessageID string,
) ([]recall.StoredMessage, []recall.EmbeddingEntry, error) {
	if pivotMessageID == "" {
		return cloneMessages(messages), cloneEmbeddings(embeddings), nil
	}
	pivotIdx := -1
	for i := range messages {
		if messages[i].ID == pivotMessageID {
			pivotIdx = i
			break
		}
	}
	if pivotIdx < 0 {
		return nil, nil, ErrForkPivotNotFound
	}
	truncated := cloneMessages(messages[:pivotIdx+1])

	retained := make(map[string]struct{}, len(truncated))
	for i := range truncated {
		retained[truncated[i].ID] = struct{}{}
	}
	filtered := make([]recall.EmbeddingEntry, 0, len(embeddings))
	for i := range embeddings {
		if _, ok := retained[embeddings[i].MsgID]; ok {
			filtered = append(filtered, embeddings[i])
		}
	}
	return truncated, filtered, nil
}

// cloneMessages returns a defensive copy of the given StoredMessage slice
// so mutations on the fork cannot leak back into the origin's in-memory
// state when both are held by the same process.
//
// Expected:
//   - src may be nil or empty; both produce an empty destination slice.
//
// Returns:
//   - A new slice with the same length and element values as src.
//
// Side effects:
//   - None.
func cloneMessages(src []recall.StoredMessage) []recall.StoredMessage {
	dst := make([]recall.StoredMessage, len(src))
	copy(dst, src)
	return dst
}

// cloneEmbeddings returns a defensive copy of the given EmbeddingEntry
// slice so the fork's embedding list is independent of the origin's.
//
// Expected:
//   - src may be nil or empty.
//
// Returns:
//   - A new slice with the same length and element values as src.
//
// Side effects:
//   - None.
func cloneEmbeddings(src []recall.EmbeddingEntry) []recall.EmbeddingEntry {
	dst := make([]recall.EmbeddingEntry, len(src))
	copy(dst, src)
	return dst
}

// forkTitle decorates a title with a "(fork)" suffix so a fork is easy
// to spot alongside its origin in the browser. An empty title is kept
// empty so existingTitle's fallback remains intact on a later save.
//
// Expected:
//   - original may be empty.
//
// Returns:
//   - The decorated title, or "" if original was empty.
//
// Side effects:
//   - None.
func forkTitle(original string) string {
	if original == "" {
		return ""
	}
	return original + " (fork)"
}

// copyEventsWAL byte-copies the origin session's events file onto the
// fork's events path. Missing origin WALs are tolerated — not every
// session has generated activity events — so the fork simply ends up
// without a WAL and LoadEvents returns an empty slice.
//
// Expected:
//   - originID identifies the origin session.
//   - newID is the target fork session ID.
//
// Returns:
//   - An error only when the origin WAL exists but cannot be copied or
//     persisted onto the fork.
//
// Side effects:
//   - Reads <baseDir>/<originID>.events.jsonl when present.
//   - Writes <baseDir>/<newID>.events.jsonl with the same contents via
//     temp + rename.
func (s *FileSessionStore) copyEventsWAL(originID, newID string) error {
	srcPath := filepath.Join(s.baseDir, originID+".events.jsonl")
	src, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("opening origin events WAL: %w", err)
	}
	defer src.Close()

	dstPath := filepath.Join(s.baseDir, newID+".events.jsonl")
	tmpPath := dstPath + ".tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening fork events WAL temp: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("copying events WAL: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync fork events WAL: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing fork events WAL temp: %w", err)
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return fmt.Errorf("renaming fork events WAL: %w", err)
	}
	return nil
}
