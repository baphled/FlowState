package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// warnIfNoTools emits a slog warning when a loaded manifest declares no
// capabilities.tools. The engine's tool-gating is fail-closed (empty/nil
// Tools yields an empty allowlist), so legacy manifests carried over from
// the implicit-grant era now produce silently-stuck agents. The warning
// surfaces the broken manifest at load time so operators can update it
// rather than discover the issue when an agent fails to do anything.
func warnIfNoTools(m *Manifest, path string) {
	if m == nil || len(m.Capabilities.Tools) > 0 {
		return
	}
	slog.Warn(
		"agent manifest has no capabilities.tools; agent will have no tools available beyond suggest_delegate",
		"path", path,
		"agent_id", m.ID,
	)
}

// LoadAndValidateManifest is the manifest-gate primitive used by the
// autoresearch harness (plan § 5.5 / § 4.7). It composes LoadManifest
// with Manifest.Validate so the harness has a single call site that
// distinguishes parse failure from validation failure: parse errors
// are wrapped by the underlying loader (e.g. "parsing JSON: ...");
// validation failures surface as *ValidationError so callers can
// classify a candidate as `manifest-validate-failed` and revert the
// trial without aborting the run.
//
// Expected:
//   - path is a valid filesystem path to a .json or .md manifest file.
//
// Returns:
//   - The parsed and validated Manifest on success.
//   - nil + a parse error if the file cannot be read or decoded.
//   - nil + a *ValidationError if the parsed manifest fails Validate.
//
// Side effects:
//   - Reads from the filesystem.
func LoadAndValidateManifest(path string) (*Manifest, error) {
	m, err := LoadManifest(path)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// LoadManifest loads an agent manifest from the given path, supporting JSON and Markdown formats.
//
// Expected:
//   - path is a valid filesystem path to a .json or .md file.
//
// Returns:
//   - The parsed Manifest on success.
//   - An error if the file cannot be read or parsed.
//
// Side effects:
//   - Reads from the filesystem.
func LoadManifest(path string) (*Manifest, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return LoadManifestJSON(path)
	case ".md":
		return LoadManifestMarkdown(path)
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
}

// LoadManifestJSON loads an agent manifest from a JSON file.
//
// Expected:
//   - path is a valid filesystem path to a JSON file.
//
// Returns:
//   - The parsed Manifest on success.
//   - An error if the file cannot be read or contains invalid JSON.
//
// Side effects:
//   - Reads from the filesystem.
func LoadManifestJSON(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	if err := validateContextManagement(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	applyDefaults(&m)
	warnIfNoTools(&m, path)
	return &m, nil
}

// LoadManifestMarkdown loads an agent manifest from a Markdown file with YAML frontmatter.
//
// Expected:
//   - path is a valid filesystem path to a Markdown file with YAML frontmatter.
//
// Returns:
//   - The parsed Manifest on success.
//   - An error if the file cannot be read or frontmatter is invalid.
//
// Side effects:
//   - Reads from the filesystem.
func LoadManifestMarkdown(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	content := string(data)
	frontmatter, body, err := extractFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("extracting frontmatter: %w", err)
	}

	var m Manifest
	if err := yaml.Unmarshal([]byte(frontmatter), &m); err != nil {
		return nil, fmt.Errorf("parsing YAML frontmatter: %w", err)
	}

	derivedID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	if m.ID == "" && m.Metadata.Role == "" {
		var mdManifest markdownManifest
		if err := yaml.Unmarshal([]byte(frontmatter), &mdManifest); err == nil && mdManifest.Description != "" {
			m = convertMarkdownManifest(mdManifest, path)
		}
	}

	m.Instructions.SystemPrompt = strings.TrimSpace(body)

	if m.ID == "" {
		m.ID = derivedID
	}
	if m.Name == "" {
		m.Name = derivedID
	}

	if err := validateContextManagement(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	applyDefaults(&m)
	warnIfNoTools(&m, path)
	return &m, nil
}

// markdownManifest is the legacy format used by OpenCode-style agent
// definitions (description, mode, default_skills, permission).
//
// Deprecated: Superseded by direct Manifest unmarshalling from YAML frontmatter.
// Retained as a fallback for .md files that use the legacy format rather than
// the full Manifest schema.
type markdownManifest struct {
	Description   string   `yaml:"description"`
	Mode          string   `yaml:"mode"`
	DefaultSkills []string `yaml:"default_skills"`
	Permission    struct {
		Skill map[string]string `yaml:"skill"`
	} `yaml:"permission"`
}

// convertMarkdownManifest converts the legacy OpenCode-style markdown
// frontmatter into a Manifest.
//
// Expected:
//   - md is a valid markdownManifest with description and default_skills fields.
//   - path is a valid filesystem path from which the manifest ID is derived.
//
// Returns:
//   - A Manifest with schema version "1", derived ID, and capabilities populated from md.
//
// Side effects:
//   - None (pure function).
//
// Deprecated: Used as a fallback when direct Manifest unmarshalling fails
// (e.g., .md files with only description/mode/default_skills fields).
func convertMarkdownManifest(md markdownManifest, path string) Manifest {
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return Manifest{
		SchemaVersion: "1",
		ID:            id,
		Name:          id,
		Complexity:    "standard",
		Metadata: Metadata{
			Role: md.Description,
		},
		Capabilities: Capabilities{
			Skills:             md.DefaultSkills,
			AlwaysActiveSkills: md.DefaultSkills,
		},
	}
}

// extractFrontmatter parses YAML frontmatter from Markdown content delimited by "---" markers.
//
// Expected:
//   - content is a string that may begin with "---" followed by YAML and another "---" delimiter.
//
// Returns:
//   - frontmatter: the YAML block between delimiters (trimmed), or empty string if no frontmatter.
//   - body: the remaining content after frontmatter (trimmed), or the original content if no frontmatter.
//   - error: non-nil if frontmatter is malformed (missing closing delimiter).
//
// Side effects:
//   - None (pure function).
func extractFrontmatter(content string) (string, string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", content, nil
	}
	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return "", content, errors.New("invalid frontmatter format")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

// validateContextManagement enforces the H3 range contract on
// user-supplied ContextManagement fields. Called by both
// LoadManifestJSON and LoadManifestMarkdown before applyDefaults so
// the check targets the operator's raw input, not the default-filled
// value. Zero is allowed — it is the sentinel that means "inherit
// global" at the engine trigger.
//
// Expected:
//   - m is a non-nil pointer to a freshly-unmarshalled Manifest. All
//     string normalisation (ID/Name derivation from the file name)
//     may or may not have run yet; validation is independent of it.
//
// Returns:
//   - nil when every range-checked field is in its legal range.
//   - A sentinel error naming the failing field when any rule fails.
//
// Side effects:
//   - None.
func validateContextManagement(m *Manifest) error {
	t := m.ContextManagement.CompactionThreshold
	// Use strict bounds (0, 1]. Zero is "inherit"; anything negative
	// is a ratio nonsense; anything >1 exceeds the budget. This
	// mirrors the range enforced by ctxstore.CompressionConfig for
	// the global threshold — keeps the contract consistent.
	if t < 0 || t > 1 {
		// Mirror the richer wording used by
		// ctxstore.CompressionConfig.Validate for
		// auto_compaction.threshold: the range alone tells operators
		// nothing about *why* the bound matters. Spell out the two
		// failure modes so the error is self-diagnosing.
		return fmt.Errorf(
			"context_management.compaction_threshold must be in the "+
				"(0.0, 1.0] interval (got %v); values <= 0 never "+
				"trigger compaction, values > 1 trigger every turn",
			t,
		)
	}
	return nil
}

// applyDefaults populates zero-valued fields in a Manifest with sensible defaults.
//
// Expected:
//   - m is a non-nil pointer to a Manifest.
//
// Returns:
//   - N/A (modifies m in place).
//
// Side effects:
//   - Mutates m.ContextManagement fields: sets MaxRecursionDepth to 2, SummaryTier to "quick",
//   - SlidingWindowSize to 10, CompactionThreshold to 0.75, and EmbeddingModel to the
//     package-level default (see SetDefaultEmbeddingModel; falls back to
//     HistoricalDefaultEmbeddingModel when unset).
func applyDefaults(m *Manifest) {
	if m.ContextManagement.MaxRecursionDepth == 0 {
		m.ContextManagement.MaxRecursionDepth = 2
	}
	if m.ContextManagement.SummaryTier == "" {
		m.ContextManagement.SummaryTier = "quick"
	}
	if m.ContextManagement.SlidingWindowSize == 0 {
		m.ContextManagement.SlidingWindowSize = 10
	}
	if m.ContextManagement.CompactionThreshold == 0 {
		m.ContextManagement.CompactionThreshold = 0.75
	}
	if m.ContextManagement.EmbeddingModel == "" {
		m.ContextManagement.EmbeddingModel = defaultEmbeddingModel
	}
}
