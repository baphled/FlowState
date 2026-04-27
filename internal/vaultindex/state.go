package vaultindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SidecarFilename is the filename used for the per-vault index sidecar.
const SidecarFilename = ".flowstate-vault-state.json"

// FileState records the indexed state of a single vault file.
//
// Mtime is the file's modification time at the moment the indexer last
// embedded its chunks. ChunkCount is the number of chunks emitted for that
// pass. LastIndexed is the wall-clock time the indexer wrote the row.
type FileState struct {
	Mtime       time.Time `json:"mtime"`
	ChunkCount  int       `json:"chunk_count"`
	LastIndexed time.Time `json:"last_indexed"`
}

// State is the in-memory view of the on-disk sidecar.
//
// State is safe for concurrent reads and writes; mutators take an internal
// mutex so the indexer can fan out file work over multiple goroutines later
// without re-engineering the type.
type State struct {
	mu    sync.Mutex
	path  string
	files map[string]FileState
}

// LoadState reads the sidecar at the supplied path.
//
// Expected:
//   - path points at the sidecar file. The parent directory must exist.
//
// Returns:
//   - A *State whose files map reflects the sidecar contents (empty when
//     the sidecar is missing).
//   - An error when the sidecar exists but cannot be read or parsed.
//
// Side effects:
//   - Reads from the filesystem.
func LoadState(path string) (*State, error) {
	s := &State{path: path, files: map[string]FileState{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading sidecar %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var wire stateWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("decoding sidecar %s: %w", path, err)
	}
	if wire.Files != nil {
		s.files = wire.Files
	}
	return s, nil
}

// SidecarPath returns the canonical sidecar path for the supplied vault root.
func SidecarPath(vaultRoot string) string {
	return filepath.Join(vaultRoot, SidecarFilename)
}

// Path returns the on-disk path the State was loaded from.
func (s *State) Path() string { return s.path }

// Get returns the recorded FileState for relPath, or false when absent.
//
// Expected:
//   - relPath is the file path relative to the vault root.
//
// Returns:
//   - The recorded FileState and true when present.
//   - The zero FileState and false when absent.
//
// Side effects:
//   - None.
func (s *State) Get(relPath string) (FileState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.files[relPath]
	return v, ok
}

// NeedsReindex reports whether relPath should be re-embedded.
//
// Expected:
//   - relPath is a vault-root-relative file path.
//   - mtime is the file's current modification time.
//
// Returns:
//   - true when no entry exists or when the recorded mtime predates mtime.
//   - false when the recorded entry's mtime is at or after mtime.
//
// Side effects:
//   - None.
func (s *State) NeedsReindex(relPath string, mtime time.Time) bool {
	prev, ok := s.Get(relPath)
	if !ok {
		return true
	}
	return prev.Mtime.Before(mtime)
}

// Update records a fresh indexing pass for relPath.
//
// Expected:
//   - relPath is a vault-root-relative file path.
//   - mtime is the file's modification time when the chunks were embedded.
//   - chunkCount is the number of chunks produced for relPath.
//
// Side effects:
//   - Mutates the in-memory map; call Save to persist.
func (s *State) Update(relPath string, mtime time.Time, chunkCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[relPath] = FileState{
		Mtime:       mtime,
		ChunkCount:  chunkCount,
		LastIndexed: time.Now(),
	}
}

// Save writes the in-memory map back to the sidecar atomically.
//
// Expected:
//   - The sidecar's parent directory exists and is writable.
//
// Returns:
//   - nil on success.
//   - A wrapped error when the sidecar cannot be written or renamed.
//
// Side effects:
//   - Writes to a temporary file in the parent directory and renames it
//     over the sidecar.
func (s *State) Save() error {
	s.mu.Lock()
	wire := stateWire{Files: s.files}
	s.mu.Unlock()

	data, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding sidecar: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".flowstate-vault-state.*.json")
	if err != nil {
		return fmt.Errorf("creating temp sidecar in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp sidecar %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp sidecar %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming sidecar %s -> %s: %w", tmpName, s.path, err)
	}
	return nil
}

// stateWire is the on-disk wrapper around the files map.
type stateWire struct {
	Files map[string]FileState `json:"files"`
}
