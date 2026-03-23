package agent

import (
	"os"
	"path/filepath"
	"strings"
)

const agentsFileName = "AGENTS.md"

// InstructionFile holds the absolute path and content of a loaded AGENTS.md file.
type InstructionFile struct {
	Path    string
	Content string
}

// AgentsFileLoader loads and merges AGENTS.md files from global config and working directories.
type AgentsFileLoader struct {
	configDir  string
	workingDir string
}

// NewAgentsFileLoader creates a new AgentsFileLoader that loads AGENTS.md from the given directories.
//
// Expected:
//   - configDir is the global configuration directory path (may be empty).
//   - workingDir is the current working directory path (may be empty).
//
// Returns:
//   - A configured AgentsFileLoader instance.
//
// Side effects:
//   - None.
func NewAgentsFileLoader(configDir, workingDir string) *AgentsFileLoader {
	return &AgentsFileLoader{configDir: configDir, workingDir: workingDir}
}

// LoadFiles returns one InstructionFile per found AGENTS.md file, config dir first then working dir.
//
// Returns:
//   - A slice of InstructionFile values, each with an absolute path and content.
//   - An empty slice if neither file exists.
//
// Side effects:
//   - Reads from the filesystem.
func (l *AgentsFileLoader) LoadFiles() []InstructionFile {
	var files []InstructionFile

	if absPath, content := l.readFileWithPath(l.configDir); content != "" {
		files = append(files, InstructionFile{Path: absPath, Content: content})
	}

	if l.isSameDirectory() {
		return files
	}

	if absPath, content := l.readFileWithPath(l.workingDir); content != "" {
		files = append(files, InstructionFile{Path: absPath, Content: content})
	}

	return files
}

// Load reads and merges AGENTS.md content from config and working directories.
//
// Returns:
//   - Merged content from both files separated by "\n\n---\n\n".
//   - Content from a single file if only one exists.
//   - Empty string if neither file exists.
//
// Side effects:
//   - Reads from the filesystem.
func (l *AgentsFileLoader) Load() string {
	files := l.LoadFiles()
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, f.Content)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// readFileWithPath reads the AGENTS.md file from the given directory, returning its absolute path and content.
//
// Expected:
//   - dir is a directory path; may be empty.
//
// Returns:
//   - The absolute file path and content as strings.
//   - Empty strings for both if the file does not exist or dir is empty.
//
// Side effects:
//   - Reads from the filesystem.
func (l *AgentsFileLoader) readFileWithPath(dir string) (string, string) {
	if dir == "" {
		return "", ""
	}
	filePath := filepath.Join(dir, agentsFileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", ""
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", ""
	}
	return absPath, string(data)
}

// isSameDirectory reports whether the config and working directories resolve to the same absolute path.
//
// Returns:
//   - True if both directories exist and resolve to the same path.
//
// Side effects:
//   - None.
func (l *AgentsFileLoader) isSameDirectory() bool {
	if l.configDir == "" || l.workingDir == "" {
		return false
	}
	absConfig, err1 := filepath.Abs(l.configDir)
	absWorking, err2 := filepath.Abs(l.workingDir)
	if err1 != nil || err2 != nil {
		return false
	}
	return absConfig == absWorking
}
