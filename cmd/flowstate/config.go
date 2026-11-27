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
	"strings"
	"unicode"

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
// config_invalid errors, as are skills_paths and skills_default in the
// ambient file.
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
	cfg, perr := decodeConfig([]byte(data), source == configSourceDefault)
	if perr != nil {
		return cliConfig{}, "", perr
	}
	return cfg, source, nil
}

// parseConfig strictly decodes one explicit config document: a secret-key
// scan at any depth runs before the DisallowUnknownFields decode, and value
// validation runs after it.
func parseConfig(data []byte) (cliConfig, error) {
	return decodeConfig(data, false)
}

// decodeConfig is parseConfig with the ambient rule applied: a config discovered as ./flowstate.json, rather
// than named by --config, must not set skills_paths or skills_default (ADR-013 D16), or a repository could choose
// which skills its sessions load. Naming either key, in any spelling the decoder matches, is config_invalid: the
// key scan folds case as encoding/json does, and the decoded fields are checked after the strict decode too.
func decodeConfig(data []byte, ambient bool) (cliConfig, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return cliConfig{}, fmt.Errorf("%w: config is not valid JSON: %v", app.ErrConfigInvalid, err)
	}
	if serr := rejectSecretKeys(raw); serr != nil {
		return cliConfig{}, serr
	}
	if ambient && namesSkillsKeys(raw) {
		return cliConfig{}, errAmbientSkills()
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
	if ambient && (cfg.SkillsPaths != nil || cfg.SkillsDefault != nil) {
		return cliConfig{}, errAmbientSkills()
	}
	if verr := cfg.validate(); verr != nil {
		return cliConfig{}, verr
	}
	return cfg, nil
}

// errAmbientSkills is the config_invalid error for an ambient flowstate.json
// that names a skills key.
func errAmbientSkills() error {
	return fmt.Errorf("%w: the ambient %s cannot set skills_paths or skills_default: "+
		"pass them with --skills-path and --skills-default, or in an explicit --config file", app.ErrConfigInvalid, ambientConfigPath)
}

// namesSkillsKeys reports whether a decoded config document names a skills key at the top level, whatever its
// value. Keys are compared with strings.EqualFold because that is how encoding/json matches them (it folds the
// long s and the Kelvin sign; strings.ToLower does not), so an exact lookup would let those spellings through.
func namesSkillsKeys(raw any) bool {
	top, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for key := range top {
		if strings.EqualFold(key, "skills_paths") || strings.EqualFold(key, "skills_default") {
			return true
		}
	}
	return false
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
// budget, valid compaction values and a non-empty, duplicate-free tool allowlist. Names that
// match no builtin stay valid here and fail at the tool definitions
// gate when the surface activates.
func (c cliConfig) validate() error {
	if c.DefaultBudget < 0 {
		return fmt.Errorf("%w: default_budget must not be negative", app.ErrConfigInvalid)
	}
	if cerr := c.Compaction.Validate(); cerr != nil {
		return cerr
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
	return c.validateSkills()
}

// validateSkills applies the skills key rules (ADR-013 D16): the entry rules
// of checkSkillsPaths and checkSkillsDefault, each a config_invalid here.
func (c cliConfig) validateSkills() error {
	if err := checkSkillsPaths("skills_paths", c.SkillsPaths); err != nil {
		return fmt.Errorf("%w: %v", app.ErrConfigInvalid, err)
	}
	if err := checkSkillsDefault("skills_default", c.SkillsDefault); err != nil {
		return fmt.Errorf("%w: %v", app.ErrConfigInvalid, err)
	}
	return nil
}

// checkSkillsPaths returns the first rule a list of skills directories breaks,
// naming it by label (the config key or the flag): an empty entry, a duplicate,
// or a relative path. A relative path is refused because it would resolve
// against whatever directory the command runs in.
func checkSkillsPaths(label string, paths []string) error {
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p == "" {
			return fmt.Errorf("%s entries must not be empty", label)
		}
		if seen[filepath.Clean(p)] {
			return fmt.Errorf("duplicate %s entry %q", label, p)
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("%s entries must be absolute directories, got %q", label, p)
		}
		seen[filepath.Clean(p)] = true
	}
	return nil
}

// checkSkillsDefault returns the first rule a list of offered skill names or
// `*`-suffixed prefixes breaks, naming it by label: an empty or blank entry,
// inner whitespace, or a duplicate. Whether a name or prefix matches a loaded
// skill is the profile validation's question, not the config's.
func checkSkillsDefault(label string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			return fmt.Errorf("%s entries must not be empty", label)
		}
		if strings.ContainsFunc(n, unicode.IsSpace) {
			return fmt.Errorf("%s entries must not contain whitespace, got %q", label, n)
		}
		if seen[n] {
			return fmt.Errorf("duplicate %s entry %q", label, n)
		}
		seen[n] = true
	}
	return nil
}

// effectiveSkills resolves the skills settings (ADR-013 D16): each flag
// replaces the config key it names, as ADR-005 orders flags over config; a key
// with no flag keeps its config value. --skills itself selects no paths.
func effectiveSkills(fl *flags) skillsSettings {
	sk := skillsSettings{Paths: fl.config.SkillsPaths, Default: fl.config.SkillsDefault}
	if len(fl.skillsPaths) > 0 {
		sk.Paths = fl.skillsPaths
	}
	if len(fl.skillsDefault) > 0 {
		sk.Default = fl.skillsDefault
	}
	return sk
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
// explicit --allow values when an allowlist flag (--allow or
// --allow-file) was given, which replaces the config list, else the
// config-derived default. --workdir only says where the tools operate
// and never selects an allowlist.
func effectiveAllowlist(fl *flags) []string {
	if allowlistFlagged(fl) {
		return fl.allow
	}
	return fl.config.ToolAllowlist
}

// canonicalComposition renders the effective session composition as
// its pinned canonical form: one JSON object with sorted keys and no
// insignificant whitespace. The allowlist is lexicographically
// sorted; db_path is the effective path after filepath.Clean. The skills
// settings join when given and non-empty only (ADR-013 D16): skills_paths
// cleaned then sorted, skills_default sorted, so every hash recorded before
// the keys existed is still reproduced.
func canonicalComposition(provider, model, harness, dbPath, source string, allow []string, skills ...skillsSettings) ([]byte, error) {
	sorted := append([]string(nil), allow...)
	if sorted == nil {
		sorted = []string{}
	}
	sort.Strings(sorted)
	composition := map[string]any{
		"config_source":  source,
		"db_path":        filepath.Clean(dbPath),
		"harness":        harness,
		"model":          model,
		"provider":       provider,
		"tool_allowlist": sorted,
	}
	if len(skills) > 0 {
		addSkillsComposition(composition, skills[0])
	}
	return json.Marshal(composition)
}

// addSkillsComposition adds each skills key to the composition when its list
// is non-empty.
func addSkillsComposition(composition map[string]any, sk skillsSettings) {
	if len(sk.Paths) > 0 {
		paths := make([]string, len(sk.Paths))
		for i, p := range sk.Paths {
			paths[i] = filepath.Clean(p)
		}
		sort.Strings(paths)
		composition["skills_paths"] = paths
	}
	if len(sk.Default) > 0 {
		names := append([]string(nil), sk.Default...)
		sort.Strings(names)
		composition["skills_default"] = names
	}
}

// compositionSHA256 returns the hex sha256 of the canonical
// composition, persisted per session as config_sha256 so identical
// effective composition pins identical wiring.
func compositionSHA256(provider, model, dbPath, source string, allow []string, skills ...skillsSettings) (string, error) {
	body, err := canonicalComposition(provider, model, app.DefaultHarness, dbPath, source, allow, skills...)
	if err != nil {
		return "", fmt.Errorf("canonical composition: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
