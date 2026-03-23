package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentsFileLoader_Load(t *testing.T) {
	t.Run("config dir only", func(t *testing.T) {
		configDir := t.TempDir()
		workingDir := t.TempDir()

		content := "Global agent instructions"
		if err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing AGENTS.md: %v", err)
		}

		loader := NewAgentsFileLoader(configDir, workingDir)
		result := loader.Load()

		if result != content {
			t.Errorf("expected %q, got %q", content, result)
		}
	})

	t.Run("working dir only", func(t *testing.T) {
		configDir := t.TempDir()
		workingDir := t.TempDir()

		content := "Project-specific instructions"
		if err := os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing AGENTS.md: %v", err)
		}

		loader := NewAgentsFileLoader(configDir, workingDir)
		result := loader.Load()

		if result != content {
			t.Errorf("expected %q, got %q", content, result)
		}
	})

	t.Run("both files merged", func(t *testing.T) {
		configDir := t.TempDir()
		workingDir := t.TempDir()

		globalContent := "Global instructions"
		localContent := "Local instructions"
		if err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte(globalContent), 0o600); err != nil {
			t.Fatalf("writing global AGENTS.md: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(localContent), 0o600); err != nil {
			t.Fatalf("writing local AGENTS.md: %v", err)
		}

		loader := NewAgentsFileLoader(configDir, workingDir)
		result := loader.Load()

		expected := globalContent + "\n\n---\n\n" + localContent
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("neither file exists", func(t *testing.T) {
		configDir := t.TempDir()
		workingDir := t.TempDir()

		loader := NewAgentsFileLoader(configDir, workingDir)
		result := loader.Load()

		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("empty config dir path", func(t *testing.T) {
		workingDir := t.TempDir()

		content := "Working dir content"
		if err := os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing AGENTS.md: %v", err)
		}

		loader := NewAgentsFileLoader("", workingDir)
		result := loader.Load()

		if result != content {
			t.Errorf("expected %q, got %q", content, result)
		}
	})

	t.Run("same directory for both", func(t *testing.T) {
		dir := t.TempDir()

		content := "Shared instructions"
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing AGENTS.md: %v", err)
		}

		loader := NewAgentsFileLoader(dir, dir)
		result := loader.Load()

		if result != content {
			t.Errorf("expected %q (no duplication), got %q", content, result)
		}
	})
}
