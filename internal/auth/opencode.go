package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// OpenCodeAuth holds credentials loaded from OpenCode's auth.json.
type OpenCodeAuth struct {
	Anthropic     *ProviderAuth `json:"anthropic,omitempty"`
	GitHubCopilot *ProviderAuth `json:"github-copilot,omitempty"`
	OpenZen       *ProviderAuth `json:"openzen,omitempty"`
	ZAI           *ProviderAuth `json:"zai,omitempty"`
}

// ProviderAuth holds a single provider's authentication credentials.
type ProviderAuth struct {
	Type    string `json:"type"`
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Expires int64  `json:"expires"`
}

var (
	// ErrAuthFileNotFound is returned when the OpenCode auth.json file does not exist.
	ErrAuthFileNotFound = errors.New("opencode auth file not found")
	// ErrNoCredentials is returned when auth.json exists but contains no provider credentials.
	ErrNoCredentials = errors.New("no provider credentials in opencode auth")
)

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
			return nil, ErrAuthFileNotFound
		}
		return nil, fmt.Errorf("reading opencode auth: %w", err)
	}

	var auth OpenCodeAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parsing opencode auth: %w", err)
	}

	if auth.GitHubCopilot == nil && auth.Anthropic == nil && auth.ZAI == nil && auth.OpenZen == nil {
		return nil, ErrNoCredentials
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
