package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadManifest(path string) (*AgentManifest, error) {
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

func LoadManifestJSON(path string) (*AgentManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	var m AgentManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	applyDefaults(&m)
	return &m, nil
}

func LoadManifestMarkdown(path string) (*AgentManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	content := string(data)
	frontmatter, _, err := extractFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("extracting frontmatter: %w", err)
	}
	var mdManifest markdownManifest
	if err := yaml.Unmarshal([]byte(frontmatter), &mdManifest); err != nil {
		return nil, fmt.Errorf("parsing YAML frontmatter: %w", err)
	}
	m := convertMarkdownManifest(mdManifest, path)
	applyDefaults(&m)
	return &m, nil
}

type markdownManifest struct {
	Description   string   `yaml:"description"`
	Mode          string   `yaml:"mode"`
	DefaultSkills []string `yaml:"default_skills"`
	Permission    struct {
		Skill map[string]string `yaml:"skill"`
	} `yaml:"permission"`
}

func convertMarkdownManifest(md markdownManifest, path string) AgentManifest {
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return AgentManifest{
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

func applyDefaults(m *AgentManifest) {
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
		m.ContextManagement.EmbeddingModel = "nomic-embed-text"
	}
}
