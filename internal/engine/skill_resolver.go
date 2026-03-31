package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrSkillNotFound is returned when a requested skill cannot be found.
var ErrSkillNotFound = errors.New("skill not found")

// SkillResolver provides an interface for resolving skill content by name.
type SkillResolver interface {
	// Resolve retrieves the content of a skill by name.
	Resolve(name string) (string, error)
}

// FileSkillResolver reads skills from the filesystem with an in-memory cache.
//
// Resolved skill content is cached after the first disk read. Subsequent calls
// for the same skill name return the cached value without touching the filesystem.
// Errors (e.g. file-not-found) are NOT cached so that late-arriving skills can
// be discovered on retry.
type FileSkillResolver struct {
	basePath string
	cache    map[string]string
	mu       sync.RWMutex
}

// NewFileSkillResolver creates a new FileSkillResolver with the given base directory.
//
// Expected:
//   - basePath is the directory containing skill subdirectories, each with a SKILL.md file.
//
// Returns:
//   - A configured FileSkillResolver instance with an initialised cache.
//
// Side effects:
//   - None.
func NewFileSkillResolver(basePath string) *FileSkillResolver {
	return &FileSkillResolver{
		basePath: basePath,
		cache:    make(map[string]string),
	}
}

// Resolve loads a skill by name, returning a cached result when available.
//
// Expected:
//   - name is the name of a skill subdirectory under basePath.
//
// Returns:
//   - The contents of {basePath}/{name}/SKILL.md as a string.
//   - ErrSkillNotFound if the file does not exist or cannot be read.
//
// Side effects:
//   - Reads the skill file from disk on the first successful call per name.
//   - Caches successful results; errors are never cached.
func (r *FileSkillResolver) Resolve(name string) (string, error) {
	if cached, ok := r.readCache(name); ok {
		return cached, nil
	}

	skillPath := filepath.Join(r.basePath, name, "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSkillNotFound
		}
		return "", fmt.Errorf("reading skill file: %w", err)
	}

	r.writeCache(name, string(content))
	return string(content), nil
}

// readCache returns the cached skill content for the given name.
//
// Expected:
//   - name is a non-empty skill name string.
//
// Returns:
//   - The cached content and true if present, empty string and false otherwise.
//
// Side effects:
//   - None.
func (r *FileSkillResolver) readCache(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cached, ok := r.cache[name]
	return cached, ok
}

// writeCache stores skill content in the cache for future lookups.
//
// Expected:
//   - name is a non-empty skill name string.
//   - content is the skill content to cache.
//
// Side effects:
//   - Stores content in the in-memory cache under the given name.
func (r *FileSkillResolver) writeCache(name, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[name] = content
}
