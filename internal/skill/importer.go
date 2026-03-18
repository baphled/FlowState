package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	ErrInvalidSkill = errors.New("skill frontmatter must have name and description")
	ErrSkillExists  = errors.New("skill already exists")
)

type Importer struct {
	SkillsDir string
}

func NewImporter(skillsDir string) *Importer {
	return &Importer{SkillsDir: skillsDir}
}

func (imp *Importer) Add(ctx context.Context, ownerRepo string) (Skill, error) {
	repoURL := fmt.Sprintf("https://github.com/%s.git", ownerRepo)

	tempDir, err := os.MkdirTemp("", "skill-import-*")
	if err != nil {
		return Skill{}, fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", repoURL, tempDir) //nolint:gosec // Input validated
	if err := cmd.Run(); err != nil {
		return Skill{}, fmt.Errorf("cloning repository %s: %w", ownerRepo, err)
	}

	return imp.AddFromPath(ctx, tempDir)
}

func (imp *Importer) AddFromPath(ctx context.Context, repoPath string) (Skill, error) {
	skillMDPath, err := findSkillMD(repoPath)
	if err != nil {
		return Skill{}, fmt.Errorf("finding SKILL.md: %w", err)
	}

	data, err := os.ReadFile(skillMDPath)
	if err != nil {
		return Skill{}, fmt.Errorf("reading SKILL.md: %w", err)
	}

	skill, err := parseAndValidateSkill(data, skillMDPath)
	if err != nil {
		return Skill{}, err
	}

	targetDir := filepath.Join(imp.SkillsDir, skill.Name)
	if _, err := os.Stat(targetDir); err == nil {
		return Skill{}, ErrSkillExists
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("creating skill directory: %w", err)
	}

	targetPath := filepath.Join(targetDir, "SKILL.md")
	if err := os.WriteFile(targetPath, data, 0o644); err != nil { //nolint:gosec // Skill files should be readable
		return Skill{}, fmt.Errorf("writing SKILL.md: %w", err)
	}

	skill.FilePath = targetPath
	return skill, nil
}

func findSkillMD(rootPath string) (string, error) {
	var found string
	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "SKILL.md" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return "", err
	}
	if found == "" {
		return "", errors.New("SKILL.md not found in repository")
	}
	return found, nil
}

func parseAndValidateSkill(data []byte, path string) (Skill, error) {
	content := string(data)
	frontmatter, body, err := extractFrontmatter(content)
	if err != nil {
		return Skill{}, fmt.Errorf("extracting frontmatter: %w", err)
	}

	var skill Skill
	if err := yaml.Unmarshal([]byte(frontmatter), &skill); err != nil {
		return Skill{}, fmt.Errorf("parsing frontmatter: %w", err)
	}

	if skill.Name == "" || skill.Description == "" {
		return Skill{}, ErrInvalidSkill
	}

	skill.Content = body
	skill.FilePath = path

	return skill, nil
}
