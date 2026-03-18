package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type FileSkillLoader struct {
	basePath string
}

func NewFileSkillLoader(basePath string) *FileSkillLoader {
	return &FileSkillLoader{basePath: basePath}
}

func (l *FileSkillLoader) LoadAll() ([]Skill, error) {
	var skills []Skill
	entries, err := os.ReadDir(l.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return skills, nil
		}
		return nil, fmt.Errorf("reading skills directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(l.basePath, entry.Name(), "SKILL.md")
		skill, err := l.LoadSkill(skillPath)
		if err != nil {
			continue
		}
		skills = append(skills, *skill)
	}
	return skills, nil
}

func (l *FileSkillLoader) LoadSkill(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading skill file: %w", err)
	}

	content := string(data)
	frontmatter, body, err := extractFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("extracting frontmatter: %w", err)
	}

	var skill Skill
	if err := yaml.Unmarshal([]byte(frontmatter), &skill); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	skill.Content = body
	skill.FilePath = path

	if skill.Name == "" {
		skill.Name = filepath.Base(filepath.Dir(path))
	}

	return &skill, nil
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
