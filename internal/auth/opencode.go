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
	// ZAICodingPlan is the OpenCode alias for Z.AI's coding-plan subscription.
	// Newer OpenCode versions write this top-level key with a `key` field
	// instead of `access`. Use ZAI-preferred normalisation in
	// LoadOpenCodeAuthFrom so callers see a single ZAI entry.
	ZAICodingPlan *ProviderAuth `json:"zai-coding-plan,omitempty"`
}

// ProviderAuth holds a single provider's authentication credentials.
//
// `Access` is the canonical access-token/API-key field. Some OpenCode
// auth.json entries (notably `zai-coding-plan`) write the token under `key`
// rather than `access`; both are accepted so the Access field is populated
// whichever the upstream writer chose.
type ProviderAuth struct {
	Type    string `json:"type"`
	Access  string `json:"access"`
	Key     string `json:"key,omitempty"`
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

	normaliseProviderAuth(auth.Anthropic)
	normaliseProviderAuth(auth.GitHubCopilot)
	normaliseProviderAuth(auth.OpenZen)
	normaliseProviderAuth(auth.ZAI)
	normaliseProviderAuth(auth.ZAICodingPlan)

	// Fall back to the OpenCode `zai-coding-plan` alias when no canonical
	// `zai` entry is present (or it has no usable token).
	if (auth.ZAI == nil || auth.ZAI.Access == "") && auth.ZAICodingPlan != nil && auth.ZAICodingPlan.Access != "" {
		auth.ZAI = auth.ZAICodingPlan
	}

	if auth.GitHubCopilot == nil && auth.Anthropic == nil && auth.ZAI == nil && auth.OpenZen == nil && auth.ZAICodingPlan == nil {
		return nil, ErrNoCredentials
	}

	return &auth, nil
}

// normaliseProviderAuth fills Access from Key when Access is empty. Some
// OpenCode auth entries (e.g. `zai-coding-plan`) use `key` instead of
// `access`; callers should see a single canonical field.
//
// Expected:
//   - pa may be nil; the function no-ops in that case.
//
// Side effects:
//   - Mutates the pointed-to ProviderAuth when Access is empty and Key is set.
func normaliseProviderAuth(pa *ProviderAuth) {
	if pa == nil {
		return
	}
	if pa.Access == "" && pa.Key != "" {
		pa.Access = pa.Key
	}
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
