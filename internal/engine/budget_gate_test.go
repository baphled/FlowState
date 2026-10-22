package engine

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/provider"
)

type stubCounter struct{}

func (stubCounter) Count(text string) int { return len(text) / 4 }

func (stubCounter) ModelLimit(string) int { return 0 }

func msg(role, content string) provider.Message {
	return provider.Message{Role: role, Content: content}
}

func TestBudgetGateNoTrimWhenFits(t *testing.T) {
	msgs := []provider.Message{msg("system", "prefix"), msg("user", "hello")}
	got, trimmed := TrimForDispatchBudget(context.Background(), msgs, stubCounter{}, 1000, 0)
	if trimmed {
		t.Fatalf("expected no trim")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
}

func TestBudgetGateTrimsOldestNonSystem(t *testing.T) {
	// Each message is 100 chars -> 25 tokens; budget small enough that the
	// oldest user messages must go.
	big := make([]byte, 100)
	for i := range big {
		big[i] = 'a'
	}
	msgs := []provider.Message{
		msg("system", string(big)),
		msg("user", string(big)+"1"),
		msg("user", string(big)+"2"),
		msg("user", string(big)+"3"),
		msg("user", string(big)+"4"),
	}
	// total = 5*(25+8) = 165; reserve 4096 dominates contextLimit 4200 ->
	// budget = 105. System block costs 33; keeping one user message = 66,
	// two = 91 (fits), three = 124 (over) -> drop two oldest user messages.
	got, trimmed := TrimForDispatchBudget(context.Background(), msgs, stubCounter{}, 4200, 0)
	if !trimmed {
		t.Fatalf("expected trim")
	}
	if got[0].Role != "system" {
		t.Fatalf("system head must be preserved")
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages after trim, got %d", len(got))
	}
	if got[1].Content == msgs[1].Content {
		t.Fatalf("oldest non-system message should have been dropped")
	}
}

func TestBudgetGateTinyLimitForcesTrim(t *testing.T) {
	big := make([]byte, 400)
	for i := range big {
		big[i] = 'a'
	}
	// 100 tokens + 8 overhead = 108; budget = 100/2 = 50 -> cannot fit,
	// but a single non-system message has nothing older to drop, so the
	// gate returns it unchanged (untrimmable) — verify with two messages.
	msgs := []provider.Message{msg("user", string(big)), msg("user", "hi")}
	got, trimmed := TrimForDispatchBudget(context.Background(), msgs, stubCounter{}, 100, 0)
	if !trimmed {
		t.Fatalf("tiny limit should force trimming")
	}
	if len(got) != 1 || got[0].Content != "hi" {
		t.Fatalf("expected only the newest message to survive, got %+v", got)
	}
}

func TestBudgetGatePreservesFinalUserUnderTightBudget(t *testing.T) {
	big := make([]byte, 4000)
	for i := range big {
		big[i] = 'a'
	}
	msgs := []provider.Message{
		msg("system", string(big)),
		msg("user", string(big)+"-old"),
		msg("user", "final pending turn"),
	}
	got, trimmed := TrimForDispatchBudget(context.Background(), msgs, stubCounter{}, 1000, 0)
	if !trimmed {
		t.Fatalf("tight budget should force trimming")
	}
	if got[0].Role != "system" {
		t.Fatalf("system head must be preserved")
	}
	if len(got) != 2 || got[len(got)-1].Content != "final pending turn" {
		t.Fatalf("final user message must survive the floor, got %+v", got)
	}
}

func TestBudgetGateNilCounterOrEmpty(t *testing.T) {
	msgs := []provider.Message{msg("user", "hi")}
	if _, trimmed := TrimForDispatchBudget(context.TODO(), msgs, nil, 100, 0); trimmed {
		t.Fatalf("nil counter disables gate")
	}
	if _, trimmed := TrimForDispatchBudget(context.TODO(), nil, stubCounter{}, 100, 0); trimmed {
		t.Fatalf("empty window is never trimmed")
	}
}

func TestBudgetGateSystemOnlyNeverTrimmed(t *testing.T) {
	msgs := []provider.Message{msg("system", "very long system prompt")}
	_, trimmed := TrimForDispatchBudget(context.Background(), msgs, stubCounter{}, 10, 0)
	if trimmed {
		t.Fatalf("system-only window must not be trimmed")
	}
}
