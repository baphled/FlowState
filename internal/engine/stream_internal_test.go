package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

func TestEstimateRequestTokensIncludesStructuredPayloads(t *testing.T) {
	eng := &Engine{tokenCounter: ctxstore.NewApproximateCounter()}
	payload := strings.Repeat("nested payload ", 1000)
	req := &provider.ChatRequest{
		Messages: []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{Name: "edit", Arguments: map[string]any{"patches": []any{map[string]any{"content": payload}}}}}}},
		Tools:    []provider.Tool{{Name: "edit", Schema: provider.ToolSchema{Type: "object", Properties: map[string]any{"content": map[string]any{"description": payload}}}}},
	}
	arguments, _ := json.Marshal(req.Messages[0].ToolCalls[0].Arguments)
	schema, _ := json.Marshal(req.Tools[0].Schema)
	minimum := eng.tokenCounter.Count(string(arguments)) + eng.tokenCounter.Count(string(schema))
	if got := eng.estimateRequestTokens(req); got < minimum {
		t.Fatalf("structured request costs %d tokens, estimate was %d", minimum, got)
	}
}

func TestCompactionRetrySkipsOverflowGate(t *testing.T) {
	eng := &Engine{tokenCounter: ctxstore.NewApproximateCounter(), systemPromptBudget: 10000}
	req := &provider.ChatRequest{Messages: []provider.Message{{Role: "user", Content: strings.Repeat("x", 80000)}}}
	ctx := session.WithSkipContextWindowOverflowCheck(context.Background())
	chunks, err := eng.streamFromProvider(ctx, req)
	if err == nil && (<-chunks).Error == nil {
		t.Fatal("expected an error (no provider configured) rather than a local gate refusal for the skip-gate retry")
	}
	// Without the skip flag the same request must be refused locally.
	chunks, err = eng.streamFromProvider(context.Background(), req)
	if err != nil {
		t.Fatalf("expected a local overflow refusal before provider dispatch, got %v", err)
	}
	if chunk := <-chunks; chunk.Error == nil {
		t.Fatal("oversized request without skip flag was not refused")
	}
}

func TestBaseHandlerChecksMutatedDispatchBudget(t *testing.T) {
	eng := &Engine{tokenCounter: ctxstore.NewApproximateCounter()}
	req := &provider.ChatRequest{Messages: []provider.Message{{Role: "system", Content: strings.Repeat("x", 80000)}}}
	chunks, err := eng.baseStreamHandler()(context.Background(), req)
	if err != nil {
		t.Fatalf("expected overflow refusal after middleware mutation, got %v", err)
	}
	if chunk := <-chunks; chunk.Error == nil {
		t.Fatal("base handler dispatched an oversized request")
	}
}
