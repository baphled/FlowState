package batch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/batch"
)

type fakeTool struct {
	name    string
	output  string
	err     error
	started chan<- string
	release <-chan struct{}
}

func (f fakeTool) Name() string { return f.name }

func (f fakeTool) Description() string { return "fake" }

func (f fakeTool) Schema() tool.Schema { return tool.Schema{Type: "object"} }

func (f fakeTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	if f.started != nil {
		f.started <- f.name
	}
	if f.release != nil {
		<-f.release
	}
	if f.err != nil {
		return tool.Result{}, f.err
	}
	return tool.Result{Output: f.output}, nil
}

func TestToolMetadata(t *testing.T) {
	t.Parallel()

	toolUnderTest := batch.New(tool.NewRegistry())

	if got := toolUnderTest.Name(); got != "batch" {
		t.Fatalf("Name() = %q, want %q", got, "batch")
	}

	if got := toolUnderTest.Description(); got == "" {
		t.Fatal("Description() = empty, want a non-empty description")
	}

	if got := toolUnderTest.Schema(); got.Type != "object" {
		t.Fatalf("Schema().Type = %q, want %q", got.Type, "object")
	}

	result, err := toolUnderTest.Execute(context.Background(), tool.Input{Name: "batch", Arguments: map[string]any{"tools": []any{}}})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result error = %v, want nil", result.Error)
	}
}

func TestToolExecuteRunsCallsConcurrently(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	release := make(chan struct{})
	registry := tool.NewRegistry()
	registry.Register(fakeTool{name: "first", output: "one", started: started, release: release})
	registry.Register(fakeTool{name: "second", output: "two", started: started, release: release})

	toolUnderTest := batch.New(registry)
	done := make(chan struct {
		result tool.Result
		err    error
	}, 1)

	go func() {
		result, err := toolUnderTest.Execute(context.Background(), tool.Input{
			Name: "batch",
			Arguments: map[string]any{
				"tools": []any{
					map[string]any{"name": "first"},
					map[string]any{"name": "second"},
				},
			},
		})
		done <- struct {
			result tool.Result
			err    error
		}{result: result, err: err}
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("expected both tools to start concurrently")
		}
	}

	close(release)

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("Execute() error = %v, want nil", outcome.err)
		}
		if outcome.result.Error != nil {
			t.Fatalf("Execute() result error = %v, want nil", outcome.result.Error)
		}
		var payload []map[string]any
		if err := json.Unmarshal([]byte(outcome.result.Output), &payload); err != nil {
			t.Fatalf("Execute() output is not valid JSON: %v", err)
		}
		if len(payload) != 2 {
			t.Fatalf("Execute() output length = %d, want 2", len(payload))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("batch execution timed out")
	}
}

func TestToolExecutePreservesPartialFailures(t *testing.T) {
	t.Parallel()

	registry := tool.NewRegistry()
	registry.Register(fakeTool{name: "ok", output: "done"})
	registry.Register(fakeTool{name: "fail", err: errors.New("boom")})

	toolUnderTest := batch.New(registry)
	result, err := toolUnderTest.Execute(context.Background(), tool.Input{
		Name: "batch",
		Arguments: map[string]any{
			"tools": []any{
				map[string]any{"name": "ok"},
				map[string]any{"name": "fail"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error == nil {
		t.Fatal("Execute() result error = nil, want non-nil")
	}
	if got := result.Output; got == "" {
		t.Fatal("Execute() output = empty, want aggregated results")
	}
	if got := fmt.Sprint(result.Error); got == "" {
		t.Fatal("Execute() result error string = empty, want detail")
	}
}
