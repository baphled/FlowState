package multiedit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/multiedit"
)

func TestMultiEditToolMetadata(t *testing.T) {
	t.Parallel()

	toolUnderTest := multiedit.New()
	if got := toolUnderTest.Name(); got != "multiedit" {
		t.Fatalf("Name() = %q, want %q", got, "multiedit")
	}
	if got := toolUnderTest.Description(); got == "" {
		t.Fatal("Description() = empty, want a non-empty description")
	}
}

func TestMultiEditToolExecute(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp(".", "multiedit-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "example.txt")
	if err := os.WriteFile(filePath, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	toolUnderTest := multiedit.New()
	result, err := toolUnderTest.Execute(context.Background(), tool.Input{
		Name: "multiedit",
		Arguments: map[string]any{
			"file_path": filePath,
			"edits": []any{
				map[string]any{"old_string": "one", "new_string": "1"},
				map[string]any{"old_string": "three", "new_string": "3"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result error = %v, want nil", result.Error)
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(updated), "1\ntwo\n3\n"; got != want {
		t.Fatalf("updated file = %q, want %q", got, want)
	}
}

func TestMultiEditToolRejectsTraversal(t *testing.T) {
	t.Parallel()

	toolUnderTest := multiedit.New()
	result, err := toolUnderTest.Execute(context.Background(), tool.Input{
		Name: "multiedit",
		Arguments: map[string]any{
			"file_path": "../outside.txt",
			"edits":     []any{map[string]any{"old_string": "one", "new_string": "1"}},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error == nil {
		t.Fatal("Execute() result error = nil, want path traversal error")
	}
}
