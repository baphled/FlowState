package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// newVaultToolsTestFS builds an in-memory FS shaped like the embedded
// vault-tools payload so install behaviour can be exercised without
// touching the real embed.
func newVaultToolsTestFS() fs.FS {
	return fstest.MapFS{
		"vault_tools/sync-vault":       &fstest.MapFile{Data: []byte("#!/usr/bin/env python3\nprint('sync')\n")},
		"vault_tools/query-vault":      &fstest.MapFile{Data: []byte("#!/usr/bin/env python3\nprint('query')\n")},
		"vault_tools/requirements.txt": &fstest.MapFile{Data: []byte("requests\n")},
	}
}

func TestInstallVaultToolsCreatesMissingFiles(t *testing.T) {
	dest := t.TempDir()
	report, err := InstallVaultTools(newVaultToolsTestFS(), dest, VaultToolsInstallOptions{})
	if err != nil {
		t.Fatalf("InstallVaultTools returned error: %v", err)
	}
	if len(report) != 3 {
		t.Fatalf("expected 3 report entries, got %d", len(report))
	}
	for _, entry := range report {
		if entry.Status != VaultToolStatusCreated {
			t.Errorf("expected created status for %s, got %s", entry.Name, entry.Status)
		}
		if entry.OldSize != 0 {
			t.Errorf("expected zero OldSize for %s, got %d", entry.Name, entry.OldSize)
		}
	}
	data, err := os.ReadFile(filepath.Join(dest, "sync-vault"))
	if err != nil {
		t.Fatalf("reading materialised script: %v", err)
	}
	if len(data) == 0 {
		t.Error("materialised script is empty")
	}
	info, err := os.Stat(filepath.Join(dest, "sync-vault"))
	if err != nil {
		t.Fatalf("stat materialised script: %v", err)
	}
	if info.Mode().Perm() != vaultToolsExecMode {
		t.Errorf("expected mode %v, got %v", vaultToolsExecMode, info.Mode().Perm())
	}
}

func TestInstallVaultToolsIdempotentRerunReportsUnchanged(t *testing.T) {
	dest := t.TempDir()
	if _, err := InstallVaultTools(newVaultToolsTestFS(), dest, VaultToolsInstallOptions{}); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	report, err := InstallVaultTools(newVaultToolsTestFS(), dest, VaultToolsInstallOptions{})
	if err != nil {
		t.Fatalf("second install failed: %v", err)
	}
	for _, entry := range report {
		if entry.Status != VaultToolStatusUnchanged {
			t.Errorf("expected unchanged status for %s, got %s", entry.Name, entry.Status)
		}
		if entry.OldSize != entry.NewSize {
			t.Errorf("expected matching sizes for %s, old=%d new=%d", entry.Name, entry.OldSize, entry.NewSize)
		}
	}
}

func TestInstallVaultToolsSkipsDifferingFilesWithoutForce(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "sync-vault")
	custom := []byte("#!/bin/sh\n# operator customisation\n")
	if err := os.WriteFile(target, custom, 0o755); err != nil {
		t.Fatalf("seeding custom script: %v", err)
	}
	report, err := InstallVaultTools(newVaultToolsTestFS(), dest, VaultToolsInstallOptions{})
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	var syncEntry *VaultToolsEntry
	for i := range report {
		if report[i].Name == "sync-vault" {
			syncEntry = &report[i]
		}
	}
	if syncEntry == nil {
		t.Fatal("sync-vault missing from report")
	}
	if syncEntry.Status != VaultToolStatusSkipped {
		t.Errorf("expected skipped status, got %s", syncEntry.Status)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading custom script: %v", err)
	}
	if string(got) != string(custom) {
		t.Error("operator customisation was clobbered without force")
	}
}

func TestInstallVaultToolsForceOverwritesDifferingFiles(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "sync-vault")
	if err := os.WriteFile(target, []byte("stale"), 0o755); err != nil {
		t.Fatalf("seeding stale script: %v", err)
	}
	report, err := InstallVaultTools(newVaultToolsTestFS(), dest, VaultToolsInstallOptions{Force: true})
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	for _, entry := range report {
		if entry.Name != "sync-vault" {
			continue
		}
		if entry.Status != VaultToolStatusUpdated {
			t.Errorf("expected updated status, got %s", entry.Status)
		}
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading overwritten script: %v", err)
	}
	if string(got) == "stale" {
		t.Error("force did not overwrite differing file")
	}
}

func TestInstallVaultToolsDryRunPerformsNoWrites(t *testing.T) {
	dest := t.TempDir()
	report, err := InstallVaultTools(newVaultToolsTestFS(), dest, VaultToolsInstallOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run install failed: %v", err)
	}
	for _, entry := range report {
		if entry.Status != VaultToolStatusCreated {
			t.Errorf("expected created classification for %s, got %s", entry.Name, entry.Status)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "sync-vault")); !os.IsNotExist(err) {
		t.Error("dry-run materialised a file")
	}
}

func TestInstallVaultToolsRejectsFSWithoutToolsDir(t *testing.T) {
	empty := fstest.MapFS{}
	if _, err := InstallVaultTools(empty, t.TempDir(), VaultToolsInstallOptions{}); err == nil {
		t.Error("expected error for FS without vault_tools directory")
	}
}

func TestEmbeddedVaultToolsFSShapesPayload(t *testing.T) {
	embedded := EmbeddedVaultToolsFS()
	if _, err := fs.Stat(embedded, VaultToolsEmbedSubdir); err != nil {
		t.Fatalf("embedded FS missing %s: %v", VaultToolsEmbedSubdir, err)
	}
	entries, err := fs.ReadDir(embedded, VaultToolsEmbedSubdir)
	if err != nil {
		t.Fatalf("reading embedded vault_tools: %v", err)
	}
	if len(entries) == 0 {
		t.Error("embedded vault_tools directory is empty")
	}
}

func TestDefaultVaultToolsDirUnderHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory not resolvable in this environment")
	}
	want := filepath.Join(home, ".local", "share", "flowstate", "vault-tools")
	if got := DefaultVaultToolsDir(); got != want {
		t.Errorf("DefaultVaultToolsDir = %q, want %q", got, want)
	}
}
