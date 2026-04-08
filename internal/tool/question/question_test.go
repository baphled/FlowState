package question_test

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/question"
)

func TestQuestionToolMetadata(t *testing.T) {
	t.Parallel()

	toolUnderTest := question.New()
	if got := toolUnderTest.Name(); got != "question" {
		t.Fatalf("Name() = %q, want %q", got, "question")
	}
	if got := toolUnderTest.Description(); got == "" {
		t.Fatal("Description() = empty, want a non-empty description")
	}
	if got := toolUnderTest.Schema(); got.Type != "object" {
		t.Fatalf("Schema().Type = %q, want %q", got.Type, "object")
	}
}

func TestQuestionToolExecute(t *testing.T) {
	t.Parallel()

	toolUnderTest := question.New()
	result, err := toolUnderTest.Execute(context.Background(), tool.Input{
		Name: "question",
		Arguments: map[string]any{
			"question":       "What should I do next?",
			"options":        []any{"Plan", "Build"},
			"allow_multiple": true,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result error = %v, want nil", result.Error)
	}
	if result.Title != "Question" {
		t.Fatalf("Execute() title = %q, want %q", result.Title, "Question")
	}
	if result.Metadata["question"] != "What should I do next?" {
		t.Fatalf("Execute() metadata question = %v, want question text", result.Metadata["question"])
	}
	if result.Metadata["allow_multiple"] != true {
		t.Fatalf("Execute() metadata allow_multiple = %v, want true", result.Metadata["allow_multiple"])
	}
}
