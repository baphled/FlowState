package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrSkillNotFound is returned when a requested skill cannot be found.
var ErrSkillNotFound = errors.New("skill not found")

// SkillResolver provides an interface for resolving skill content by name.
type SkillResolver interface {
	// Resolve retrieves the content of a skill by name.
	Resolve(name string) (string, error)
}

// FileSkillResolver reads skills from the filesystem.
type FileSkillResolver struct {
	basePath string
}

// NewFileSkillResolver creates a new FileSkillResolver with the given base directory.
//
// Expected:
//   - basePath is the directory containing skill subdirectories, each with a SKILL.md file.
//
// Returns:
//   - A configured FileSkillResolver instance.
//
// Side effects:
//   - None.
func NewFileSkillResolver(basePath string) *FileSkillResolver {
	return &FileSkillResolver{basePath: basePath}
}

// Resolve loads a skill by name from the filesystem.
//
// Expected:
//   - name is the name of a skill subdirectory under basePath.
//
// Returns:
//   - The contents of {basePath}/{name}/SKILL.md as a string.
//   - ErrSkillNotFound if the file does not exist or cannot be read.
//
// Side effects:
//   - Reads the skill file from disk.
func (r *FileSkillResolver) Resolve(name string) (string, error) {
	skillPath := filepath.Join(r.basePath, name, "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSkillNotFound
		}
		return "", fmt.Errorf("reading skill file: %w", err)
	}
	return string(content), nil
}
