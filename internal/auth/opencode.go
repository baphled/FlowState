package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// OpenCodeAuth holds credentials loaded from OpenCode's auth.json.
type OpenCodeAuth struct {
	GitHubCopilot *ProviderAuth `json:"github-copilot,omitempty"`
	Anthropic     *ProviderAuth `json:"anthropic,omitempty"`
}

// ProviderAuth holds a single provider's authentication credentials.
type ProviderAuth struct {
	Type    string `json:"type"`
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Expires int64  `json:"expires"`
}

// LoadOpenCodeAuthFrom loads OpenCode credentials from the specified path.
//
// Expected:
//   - path is a valid file path to auth.json or does not exist.
//
// Returns:
//   - Parsed OpenCodeAuth if file exists and is valid JSON.
//   - nil if file does not exist (not an error).
//   - An error if file exists but cannot be read or parsed.
//
// Side effects:
//   - None.
func LoadOpenCodeAuthFrom(path string) (*OpenCodeAuth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			//nolint:nilnil // File not found is not an error for optional config
			return nil, nil
		}
		return nil, fmt.Errorf("reading opencode auth: %w", err)
	}

	var auth OpenCodeAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parsing opencode auth: %w", err)
	}

	if auth.GitHubCopilot == nil && auth.Anthropic == nil {
		//nolint:nilnil // No credentials in file is not an error
		return nil, nil
	}

	return &auth, nil
}

// LoadOpenCodeAuthFromHome loads OpenCode credentials from the default home location.
//
// Expected:
//   - pathResolver is a function that returns the path to auth.json given a home directory.
//
// Returns:
//   - Parsed OpenCodeAuth if file exists and is valid JSON.
//   - nil, nil if file does not exist.
//   - An error if file exists but cannot be read or parsed.
//
// Side effects:
//   - None.
func LoadOpenCodeAuthFromHome(pathResolver func(home string) string) (*OpenCodeAuth, error) {
	path := pathResolver("")
	return LoadOpenCodeAuthFrom(path)
}
