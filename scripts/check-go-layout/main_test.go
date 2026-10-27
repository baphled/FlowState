package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureGit(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func fixtureSource(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, "add", path)
	fixtureGit(t, "commit", "-qm", "test: synthetic layout fixture")
}

func TestCommittedLayoutCannotUseWorkingCopyToHideType(t *testing.T) {
	t.Chdir(t.TempDir())
	fixtureGit(t, "init", "-b", "feature/fixture")
	fixtureSource(t, "readme.txt", "synthetic fixture")
	base := fixtureGit(t, "rev-parse", "HEAD")
	fixtureSource(t, "client.go", "package fixture; type Client struct{}\n")
	if err := os.WriteFile("client.go", []byte("package fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkRevision(base, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "move type Client") {
		t.Fatalf("committed violation was not rejected: %v", err)
	}
	fixtureSource(t, "client.go", "package fixture; func Run(){}\n")
	fixtureSource(t, filepath.Join(".", "types.go"), "package fixture; type Client struct{}\n")
	if err := checkRevision(base, &bytes.Buffer{}); err != nil {
		t.Fatalf("separated same-package type should pass: %v", err)
	}
}

func TestMissingOrInvalidBaseFails(t *testing.T) {
	for _, base := range []string{"", "HEAD", "--all", strings.Repeat("a", 40)} {
		if err := checkRevision(base, &bytes.Buffer{}); err == nil {
			t.Fatalf("base %q unexpectedly passed", base)
		}
	}
}

func TestGitTypeChangeCannotHideCommittedGoPath(t *testing.T) {
	t.Chdir(t.TempDir())
	fixtureGit(t, "init", "-b", "feature/fixture")
	fixtureSource(t, "client.go", "package fixture; func Run(){}\n")
	base := fixtureGit(t, "rev-parse", "HEAD")
	command := exec.Command("git", "hash-object", "-w", "--stdin")
	command.Stdin = strings.NewReader("elsewhere.go")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, "update-index", "--cacheinfo", "120000", strings.TrimSpace(string(output)), "client.go")
	fixtureGit(t, "commit", "-qm", "test: synthetic mode change")
	if err := checkRevision(base, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("committed nonregular Go blob was not rejected: %v", err)
	}
}
