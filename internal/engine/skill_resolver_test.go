package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSkillResolver_Resolve_Success(t *testing.T) {
	tmpDir := t.TempDir()

	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("Failed to create skill directory: %v", err)
	}

	skillContent := "# Test Skill\n\nThis is test skill content."
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o600); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	resolver := NewFileSkillResolver(tmpDir)
	content, err := resolver.Resolve("test-skill")

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if content != skillContent {
		t.Errorf("Expected %q, got %q", skillContent, content)
	}
}

func TestFileSkillResolver_Resolve_MissingSkill(t *testing.T) {
	tmpDir := t.TempDir()

	resolver := NewFileSkillResolver(tmpDir)
	_, err := resolver.Resolve("nonexistent-skill")

	if err == nil {
		t.Error("Expected error for missing skill, got nil")
	}

	if !errors.Is(err, ErrSkillNotFound) {
		t.Errorf("Expected ErrSkillNotFound, got %v", err)
	}
}

func TestFileSkillResolver_Resolve_EmptyContent(t *testing.T) {
	tmpDir := t.TempDir()

	skillDir := filepath.Join(tmpDir, "empty-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("Failed to create skill directory: %v", err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(""), 0o600); err != nil {
		t.Fatalf("Failed to write skill file: %v", err)
	}

	resolver := NewFileSkillResolver(tmpDir)
	content, err := resolver.Resolve("empty-skill")

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if content != "" {
		t.Errorf("Expected empty string, got %q", content)
	}
}
