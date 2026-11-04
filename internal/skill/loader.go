package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadSkill reads one SKILL.md-style file: `---` frontmatter
// (name, description) then the markdown body.
func LoadSkill(path string) (Skill, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Skill{}, fmt.Errorf("skill: read %q: %w", path, err)
	}
	return ParseSkill(raw)
}

// ParseSkill splits frontmatter from body and decodes the
// frontmatter fields the loader understands.
func ParseSkill(raw []byte) (Skill, error) {
	text := string(raw)
	if !strings.HasPrefix(text, "---") {
		return Skill{}, fmt.Errorf("skill: missing frontmatter")
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "---"), "---\n", 2)
	if len(parts) != 2 {
		return Skill{}, fmt.Errorf("skill: unterminated frontmatter")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(parts[0]), &fm); err != nil {
		return Skill{}, fmt.Errorf("skill: parse frontmatter: %w", err)
	}
	s := Skill{Name: fm.Name, Description: fm.Description, Body: strings.TrimSpace(parts[1])}
	if s.Name == "" {
		return Skill{}, fmt.Errorf("skill: frontmatter name is required")
	}
	return s, nil
}

// LoadDir loads every *.md skill in dir; a missing dir is an
// error, an empty one yields no skills.
func LoadDir(dir string) (map[string]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skill: read dir %q: %w", dir, err)
	}
	skills := map[string]Skill{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		s, err := LoadSkill(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		skills[s.Name] = s
	}
	return skills, nil
}
