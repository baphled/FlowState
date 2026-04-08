package invalid_test

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/invalid"
)

func TestInvalidToolMetadata(t *testing.T) {
	t.Parallel()

	toolUnderTest := invalid.New()
	if got := toolUnderTest.Name(); got != "invalid" {
		t.Fatalf("Name() = %q, want %q", got, "invalid")
	}
}

func TestInvalidToolExecute(t *testing.T) {
	t.Parallel()

	result, err := invalid.New().Execute(context.Background(), tool.Input{Name: "invalid"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error == nil {
		t.Fatal("Execute() result error = nil, want non-nil")
	}
}
