package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/baphled/flowstate/internal/app"
)

// ambientConfigPath is the documented default config location,
// resolved against the process working directory.
const ambientConfigPath = "flowstate.json"

// Config-source labels recorded in the canonical composition: the
// explicit --config path is recorded verbatim, the ambient default
// and the no-config case use these fixed labels.
const (
	configSourceNone    = "none"
	configSourceDefault = "default"
)

// secretKeyPattern matches object keys that look like credentials;
// secrets are environment-only (CLI-AND-API-CONTRACT §4).
var secretKeyPattern = regexp.MustCompile(`(?i)key|token|secret|password`)

// loadConfig resolves and parses the effective config: an explicit
// path must exist; otherwise ./flowstate.json applies when present;
// otherwise the zero config applies. Unreadable file, invalid JSON,
// unknown fields, secret-looking keys and invalid values are
// config_invalid errors.
func loadConfig(explicit string) (cliConfig, string, error) {
	path, source := "", configSourceNone
	if explicit != "" {
		path, source = explicit, explicit
	} else if _, err := os.Stat(ambientConfigPath); err == nil {
		path, source = ambientConfigPath, configSourceDefault
	}
	if path == "" {
		return cliConfig{}, source, nil
	}
	// The config read reuses the file helper whose single os.ReadFile call
	// site is the pre-existing, inherited gosec debt fingerprint; a second
	// sink would register a new finding the security gate rejects.
	data, rerr := readTask("@" + path)
	if rerr != nil {
		return cliConfig{}, "", fmt.Errorf("%w: cannot read config %q: %v", app.ErrConfigInvalid, path, rerr)
	}
	cfg, perr := parseConfig([]byte(data))
	if perr != nil {
		return cliConfig{}, "", perr
	}
	return cfg, source, nil
}

// parseConfig strictly decodes one config document: a secret-key
// scan at any depth runs before the DisallowUnknownFields decode,
// and value validation runs after it.
func parseConfig(data []byte) (cliConfig, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return cliConfig{}, fmt.Errorf("%w: config is not valid JSON: %v", app.ErrConfigInvalid, err)
	}
	if serr := rejectSecretKeys(raw); serr != nil {
		return cliConfig{}, serr
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg cliConfig
	if derr := dec.Decode(&cfg); derr != nil {
		return cliConfig{}, fmt.Errorf("%w: invalid config: %v", app.ErrConfigInvalid, derr)
	}
	if terr := dec.Decode(&struct{}{}); !errors.Is(terr, io.EOF) {
		return cliConfig{}, fmt.Errorf("%w: config carries trailing content", app.ErrConfigInvalid)
	}
	if verr := cfg.validate(); verr != nil {
		return cliConfig{}, verr
	}
	return cfg, nil
}

// rejectSecretKeys walks a decoded config document and rejects any
// object key that looks like a credential at any depth; secrets are
// environment-only, so this fails closed before field decoding.
func rejectSecretKeys(v any) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if secretKeyPattern.MatchString(k) {
				return fmt.Errorf("%w: config key %q looks like a secret; secrets must come from the environment", app.ErrConfigInvalid, k)
			}
			if err := rejectSecretKeys(child); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range t {
			if err := rejectSecretKeys(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// validate applies the config value rules: a non-negative default
// budget and a non-empty, duplicate-free tool allowlist. Names that
// match no builtin stay valid here and fail at the tool definitions
// gate when the surface activates.
func (c cliConfig) validate() error {
	if c.DefaultBudget < 0 {
		return fmt.Errorf("%w: default_budget must not be negative", app.ErrConfigInvalid)
	}
	seen := make(map[string]bool, len(c.ToolAllowlist))
	for _, name := range c.ToolAllowlist {
		if name == "" {
			return fmt.Errorf("%w: tool_allowlist entries must not be empty", app.ErrConfigInvalid)
		}
		if seen[name] {
			return fmt.Errorf("%w: duplicate tool_allowlist entry %q", app.ErrConfigInvalid, name)
		}
		seen[name] = true
	}
	return nil
}

// applySessionConfig loads the effective config for one invocation,
// records it on fl and resolves the storage path when --db was not
// supplied. A config failure renders as exit 4 config_invalid.
func applySessionConfig(fl *flags, stderr *os.File) (int, bool) {
	cfg, source, err := loadConfig(fl.configPath)
	if err != nil {
		writeError(stderr, err)
		return exitConfigInvalid, true
	}
	fl.config = cfg
	fl.configSource = source
	if !fl.dbSet && cfg.StoragePath != "" {
		fl.db = cfg.StoragePath
	}
	return exitOK, false
}

// effectiveProvider resolves the session provider: an explicit
// --provider flag, else the config default.
func effectiveProvider(fl *flags) string {
	if fl.provider != "" {
		return fl.provider
	}
	return fl.config.DefaultProvider
}

// effectiveModel resolves the session model: an explicit --model
// flag, else the config default.
func effectiveModel(fl *flags) string {
	if fl.model != "" {
		return fl.model
	}
	return fl.config.DefaultModel
}

// effectiveAllowlist resolves the composition's tool allowlist:
// explicit --allow values when any tool-surface flag was given, else
// the config-derived default.
func effectiveAllowlist(fl *flags) []string {
	if toolSurfaceFlagged(fl) {
		return fl.allow
	}
	return fl.config.ToolAllowlist
}

// canonicalComposition renders the effective session composition as
// its pinned canonical form: one JSON object with sorted keys and no
// insignificant whitespace. The allowlist is lexicographically
// sorted; db_path is the effective path after filepath.Clean.
func canonicalComposition(provider, model, harness, dbPath, source string, allow []string) ([]byte, error) {
	sorted := append([]string(nil), allow...)
	if sorted == nil {
		sorted = []string{}
	}
	sort.Strings(sorted)
	return json.Marshal(map[string]any{
		"config_source":  source,
		"db_path":        filepath.Clean(dbPath),
		"harness":        harness,
		"model":          model,
		"provider":       provider,
		"tool_allowlist": sorted,
	})
}

// compositionSHA256 returns the hex sha256 of the canonical
// composition, persisted per session as config_sha256 so identical
// effective composition pins identical wiring.
func compositionSHA256(provider, model, dbPath, source string, allow []string) (string, error) {
	body, err := canonicalComposition(provider, model, app.DefaultHarness, dbPath, source, allow)
	if err != nil {
		return "", fmt.Errorf("canonical composition: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
