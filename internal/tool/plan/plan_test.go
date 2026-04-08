package plan_test

import (
	"context"
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
