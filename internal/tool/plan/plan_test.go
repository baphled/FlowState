package plan_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/plan"
)

func TestPlanToolsMetadata(t *testing.T) {
	t.Parallel()

	if got := plan.NewEnter().Name(); got != "plan_enter" {
		t.Fatalf("Enter Name() = %q, want %q", got, "plan_enter")
	}
	if got := plan.NewExit().Name(); got != "plan_exit" {
		t.Fatalf("Exit Name() = %q, want %q", got, "plan_exit")
	}
}

func TestPlanEnterExecute(t *testing.T) {
	t.Parallel()

	result, err := plan.NewEnter().Execute(context.Background(), tool.Input{Name: "plan_enter"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result error = %v, want nil", result.Error)
	}
	if result.Metadata["action"] != "enter" {
		t.Fatalf("Execute() metadata action = %v, want enter", result.Metadata["action"])
	}
}

// writePlanFile is a test helper that writes a minimal plan markdown file
// with YAML frontmatter to the given directory.
//
// Expected:
//   - dir exists and is writable.
//   - id is filename-safe.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Creates {dir}/{id}.md on disk and fails the test on write error.
func writePlanFile(t *testing.T, dir, id, title, body string) {
	t.Helper()
	content := "---\n" +
		"id: " + id + "\n" +
		"title: " + title + "\n" +
		"status: draft\n" +
		"---\n\n" +
		body
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing plan file: %v", err)
	}
}

func TestPlanListMetadata(t *testing.T) {
	t.Parallel()

	lister := plan.NewList(t.TempDir())
	if got := lister.Name(); got != "plan_list" {
		t.Fatalf("List Name() = %q, want %q", got, "plan_list")
	}
	if got := lister.Description(); got == "" {
		t.Fatal("List Description() = empty, want non-empty")
	}
	schema := lister.Schema()
	if schema.Type != "object" {
		t.Fatalf("List Schema().Type = %q, want object", schema.Type)
	}
	if len(schema.Required) != 0 {
		t.Fatalf("List Schema().Required = %v, want empty", schema.Required)
	}
}

func TestPlanListExecuteEmptyDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	result, err := plan.NewList(dir).Execute(context.Background(), tool.Input{Name: "plan_list"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result error = %v, want nil", result.Error)
	}
	if !strings.Contains(result.Output, "No plans") {
		t.Fatalf("Execute() output = %q, want to contain 'No plans'", result.Output)
	}
}

func TestPlanListExecuteReturnsPlans(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writePlanFile(t, dir, "alpha", "Alpha Plan", "# Alpha\n")
	writePlanFile(t, dir, "beta", "Beta Plan", "# Beta\n")

	result, err := plan.NewList(dir).Execute(context.Background(), tool.Input{Name: "plan_list"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result error = %v, want nil", result.Error)
	}
	if !strings.Contains(result.Output, "alpha") {
		t.Fatalf("Execute() output = %q, want to contain 'alpha'", result.Output)
	}
	if !strings.Contains(result.Output, "Alpha Plan") {
		t.Fatalf("Execute() output = %q, want to contain 'Alpha Plan'", result.Output)
	}
	if !strings.Contains(result.Output, "beta") {
		t.Fatalf("Execute() output = %q, want to contain 'beta'", result.Output)
	}
}

func TestPlanReadMetadata(t *testing.T) {
	t.Parallel()

	reader := plan.NewRead(t.TempDir())
	if got := reader.Name(); got != "plan_read" {
		t.Fatalf("Read Name() = %q, want %q", got, "plan_read")
	}
	if got := reader.Description(); got == "" {
		t.Fatal("Read Description() = empty, want non-empty")
	}
	schema := reader.Schema()
	if schema.Type != "object" {
		t.Fatalf("Read Schema().Type = %q, want object", schema.Type)
	}
	if _, ok := schema.Properties["id"]; !ok {
		t.Fatalf("Read Schema().Properties missing 'id': %v", schema.Properties)
	}
	found := false
	for _, req := range schema.Required {
		if req == "id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Read Schema().Required = %v, want to include 'id'", schema.Required)
	}
}

func TestPlanReadExecuteReturnsContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := "# Alpha Plan\n\nSome prose.\n"
	writePlanFile(t, dir, "alpha", "Alpha Plan", body)

	result, err := plan.NewRead(dir).Execute(context.Background(), tool.Input{
		Name:      "plan_read",
		Arguments: map[string]interface{}{"id": "alpha"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result error = %v, want nil", result.Error)
	}
	if !strings.Contains(result.Output, "Alpha Plan") {
		t.Fatalf("Execute() output = %q, want to contain 'Alpha Plan'", result.Output)
	}
	if !strings.Contains(result.Output, "Some prose.") {
		t.Fatalf("Execute() output = %q, want to contain 'Some prose.'", result.Output)
	}
}

func TestPlanReadExecuteMissingIDArgument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	result, err := plan.NewRead(dir).Execute(context.Background(), tool.Input{
		Name:      "plan_read",
		Arguments: map[string]interface{}{},
	})
	if err == nil && result.Error == nil {
		t.Fatal("Execute() expected error for missing id, got none")
	}
}

func TestPlanReadExecuteNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	result, err := plan.NewRead(dir).Execute(context.Background(), tool.Input{
		Name:      "plan_read",
		Arguments: map[string]interface{}{"id": "nonexistent"},
	})
	if err == nil && result.Error == nil {
		t.Fatal("Execute() expected error for missing plan, got none")
	}
	// The error should mention the requested id and the plans dir path.
	var msg string
	switch {
	case err != nil:
		msg = err.Error()
	case result.Error != nil:
		msg = result.Error.Error()
	}
	if !strings.Contains(msg, "nonexistent") {
		t.Fatalf("error = %q, want to contain requested id 'nonexistent'", msg)
	}
	if !strings.Contains(msg, dir) {
		t.Fatalf("error = %q, want to contain plans dir %q", msg, dir)
	}
}
