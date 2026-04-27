package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRequiresVaultRoot(t *testing.T) {
	cfg := defaultRuntimeConfig()
	if err := validate(&cfg); err == nil {
		t.Fatal("expected error when vault root is empty")
	}
}

func TestValidateRejectsMissingDirectory(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.VaultRoot = filepath.Join(t.TempDir(), "does-not-exist")
	err := validate(&cfg)
	if err == nil {
		t.Fatal("expected error for missing vault root")
	}
}

func TestValidateRejectsFileAsVaultRoot(t *testing.T) {
	cfg := defaultRuntimeConfig()
	path := filepath.Join(t.TempDir(), "file.md")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.VaultRoot = path
	err := validate(&cfg)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", err)
	}
}

func TestApplyEnvDefaultsRespectsExplicitFlag(t *testing.T) {
	t.Setenv("QDRANT_URL", "http://from-env:6333")
	cfg := defaultRuntimeConfig()
	cfg.QdrantURL = "http://from-flag:6333"
	applyEnvDefaults(&cfg)
	if cfg.QdrantURL != "http://from-flag:6333" {
		t.Fatalf("explicit flag must win, got %s", cfg.QdrantURL)
	}
}

func TestApplyEnvDefaultsFillsUnsetVaultRoot(t *testing.T) {
	t.Setenv("FLOWSTATE_VAULT_ROOT", "/from/env")
	cfg := defaultRuntimeConfig()
	applyEnvDefaults(&cfg)
	if cfg.VaultRoot != "/from/env" {
		t.Fatalf("env vault root must seed empty cfg, got %s", cfg.VaultRoot)
	}
}

func TestNewRootCmdHasExpectedFlags(t *testing.T) {
	cmd := newRootCmd()
	for _, name := range []string{"vault-root", "qdrant-url", "qdrant-collection", "ollama-host", "embedding-model", "chunk-size", "chunk-overlap", "reindex"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag %s", name)
		}
	}
}
