package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDualLoad(t *testing.T) {
	tmpDir := t.TempDir()

	jsonPath := filepath.Join(tmpDir, "test-agent.json")
	jsonContent := `{
		"schema_version": "1",
		"id": "test-agent",
		"name": "Test Agent",
		"complexity": "standard",
		"metadata": {
			"role": "Test role"
		}
	}`
	if err := os.WriteFile(jsonPath, []byte(jsonContent), 0o600); err != nil {
		t.Fatalf("failed to write JSON file: %v", err)
	}

	mdPath := filepath.Join(tmpDir, "md-agent.md")
	mdContent := `---
description: Markdown agent
mode: subagent
default_skills:
  - skill1
  - skill2
---
# Agent Instructions
`
	if err := os.WriteFile(mdPath, []byte(mdContent), 0o600); err != nil {
		t.Fatalf("failed to write MD file: %v", err)
	}

	t.Run("LoadJSON", func(t *testing.T) {
		m, err := LoadManifest(jsonPath)
		if err != nil {
			t.Fatalf("LoadManifest failed: %v", err)
		}
		if m.ID != "test-agent" {
			t.Errorf("expected ID 'test-agent', got %q", m.ID)
		}
		if m.Name != "Test Agent" {
			t.Errorf("expected Name 'Test Agent', got %q", m.Name)
		}
		if m.ContextManagement.MaxRecursionDepth != 2 {
			t.Errorf("expected default MaxRecursionDepth 2, got %d", m.ContextManagement.MaxRecursionDepth)
		}
	})

	t.Run("LoadMarkdown", func(t *testing.T) {
		m, err := LoadManifest(mdPath)
		if err != nil {
			t.Fatalf("LoadManifest failed: %v", err)
		}
		if m.ID != "md-agent" {
			t.Errorf("expected ID 'md-agent', got %q", m.ID)
		}
		if m.Metadata.Role != "Markdown agent" {
			t.Errorf("expected Role 'Markdown agent', got %q", m.Metadata.Role)
		}
		if len(m.Capabilities.Skills) != 2 {
			t.Errorf("expected 2 skills, got %d", len(m.Capabilities.Skills))
		}
	})
}
