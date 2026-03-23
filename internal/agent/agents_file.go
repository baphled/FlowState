package agent

import (
	"os"
	"path/filepath"
	"strings"
)

const agentsFileName = "AGENTS.md"

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
	var parts []string

	configContent := l.readFile(l.configDir)
	if configContent != "" {
		parts = append(parts, configContent)
	}

	if l.isSameDirectory() {
		return strings.Join(parts, "")
	}

	workingContent := l.readFile(l.workingDir)
	if workingContent != "" {
		parts = append(parts, workingContent)
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// readFile reads the AGENTS.md file from the given directory.
//
// Expected:
//   - dir is a directory path; may be empty.
//
// Returns:
//   - The file content as a string, or empty string if the file does not exist or dir is empty.
//
// Side effects:
//   - Reads from the filesystem.
func (l *AgentsFileLoader) readFile(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, agentsFileName))
	if err != nil {
		return ""
	}
	return string(data)
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
