package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	pluginpkg "github.com/baphled/flowstate/internal/plugin"
)

// Manifest describes a plugin manifest loaded from disk.
//
// Expected: used to configure plugins.
// Returns: struct with manifest fields.
// Side effects: none.
type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Hooks       []string `json:"hooks"`
	Timeout     int      `json:"timeout,omitempty"`
}

// LoadManifest reads, parses, and validates a manifest from disk.
//
// Expected:
//   - Reads the file at path.
//   - Parses JSON into Manifest struct.
//   - Validates manifest fields.
//
// Returns: pointer to Manifest and error.
// Side effects: reads from disk.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if err := Validate(&m); err != nil {
		return nil, err
	}

	return &m, nil
}

// Validate checks that a manifest contains the required fields and supported hooks.
//
// Expected:
//   - Returns nil if the manifest is valid.
//   - Returns an error describing the first validation failure if invalid.
//
// Returns: error if invalid, nil if valid.
// Side effects: may mutate m.Timeout if zero.
func Validate(m *Manifest) error {
	if m == nil {
		return errors.New("manifest validation: manifest is required")
	}
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("manifest validation: name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("manifest validation: version is required")
	}
	if strings.TrimSpace(m.Command) == "" {
		return errors.New("manifest validation: command is required")
	}
	if len(m.Hooks) == 0 {
		return errors.New("manifest validation: at least one hook is required")
	}

	validHooks := map[pluginpkg.HookType]struct{}{
		pluginpkg.ChatParams:     {},
		pluginpkg.EventType:      {},
		pluginpkg.ToolExecBefore: {},
		pluginpkg.ToolExecAfter:  {},
	}

	for _, hook := range m.Hooks {
		if _, ok := validHooks[pluginpkg.HookType(hook)]; !ok {
			return fmt.Errorf("manifest validation: invalid hook %q; valid types: %s", hook, strings.Join(validHookNames(), ", "))
		}
	}

	if m.Timeout == 0 {
		m.Timeout = 5
	}

	return nil
}

// validHookNames returns the list of valid hook type names for plugin manifests.
//
// Expected: returns all valid hook type names.
// Returns: slice of valid hook names.
// Side effects: none.
func validHookNames() []string {
	hooks := []string{
		string(pluginpkg.ChatParams),
		string(pluginpkg.EventType),
		string(pluginpkg.ToolExecBefore),
		string(pluginpkg.ToolExecAfter),
	}
	sort.Strings(hooks)
	return hooks
}
