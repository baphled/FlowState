package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// hotTailUnitCounter counts one token per word so unit tests can pin
// exact per-message costs.
type hotTailUnitCounter struct{}

func (c hotTailUnitCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (hotTailUnitCounter) ModelLimit(string) int { return 0 }

func hotTailMessages(n int) []provider.Message {
	msgs := make([]provider.Message, n)
	for i := range msgs {
		msgs[i] = provider.Message{Role: "user", Content: "w"}
	}
	return msgs
}

// TestSelectHotTailBudgetedGrowsToBudget pins the floor-then-grow rule:
// the tail starts at the minimum floor and grows backwards only while
// the cumulative cost stays within the budget.
func TestSelectHotTailBudgetedGrowsToBudget(t *testing.T) {
	eng := engine.New(engine.Config{
		TokenCounter:            hotTailUnitCounter{},
		HotTailBudgetForTests:   4,
		HotTailMinFloorForTests: 2,
	})
	got, err := eng.SelectHotTailBudgetedForTesting(context.Background(), hotTailMessages(6))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4-message tail, got %d", len(got))
	}
	if got[len(got)-1].Content != "w" {
		t.Fatal("tail must be anchored at the newest message")
	}
}

// TestSelectHotTailBudgetedFloorOverflowKeepsNewest pins the clamp
// rule: when even the floor exceeds the budget the tail keeps the
// most recent messages that fit, always at least one.
func TestSelectHotTailBudgetedFloorOverflowKeepsNewest(t *testing.T) {
	eng := engine.New(engine.Config{
		TokenCounter:            hotTailUnitCounter{},
		HotTailBudgetForTests:   2,
		HotTailMinFloorForTests: 3,
	})
	got, err := eng.SelectHotTailBudgetedForTesting(context.Background(), hotTailMessages(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2-message clamped tail, got %d", len(got))
	}
	if got[len(got)-1].Content != "w" {
		t.Fatal("tail must be anchored at the newest message")
	}
}

// TestSelectHotTailBudgetedEmptySliceErrors pins the loud empty-input
// failure so callers never receive a nil window silently.
func TestSelectHotTailBudgetedEmptySliceErrors(t *testing.T) {
	eng := engine.New(engine.Config{TokenCounter: hotTailUnitCounter{}})
	if _, err := eng.SelectHotTailBudgetedForTesting(context.Background(), nil); err == nil {
		t.Fatal("expected an error for an empty live slice")
	}
}

// TestRebuildHotTailBudgetedDropOldestRetries pins the drop-oldest
// overflow retry: an over-budget window sheds its oldest tail messages
// one at a time until the estimate fits, and never dispatches an
// oversized context.
func TestRebuildHotTailBudgetedDropOldestRetries(t *testing.T) {
	eng := engine.New(engine.Config{
		TokenCounter:            hotTailUnitCounter{},
		SystemPromptBudget:      80,
		OutputReserveForTests:   2,
		HotTailBudgetForTests:   6,
		HotTailMinFloorForTests: 3,
	})
	got, err := eng.RebuildHotTailBudgetedForTesting(context.Background(), hotTailMessages(6), "s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng.ContextEstimateOverBudgetForTesting(context.Background(), got) {
		t.Fatal("rebuilt window still exceeds the usable budget")
	}
	if got[len(got)-1].Content != "w" {
		t.Fatal("newest live message must survive the retry")
	}
}

// TestRebuildHotTailBudgetedFailsLoudly pins the terminal path: when a
// single-message window still exceeds the budget the rebuild returns
// an ErrCompactionInsufficient error instead of dispatching.
func TestRebuildHotTailBudgetedFailsLoudly(t *testing.T) {
	eng := engine.New(engine.Config{
		TokenCounter:            hotTailUnitCounter{},
		SystemPromptBudget:      10,
		OutputReserveForTests:   2,
		HotTailBudgetForTests:   6,
		HotTailMinFloorForTests: 3,
	})
	_, err := eng.RebuildHotTailBudgetedForTesting(context.Background(), hotTailMessages(4), "s")
	if err == nil {
		t.Fatal("expected a loud failure")
	}
	if !errors.Is(err, engine.ErrCompactionInsufficient) {
		t.Fatalf("expected ErrCompactionInsufficient, got %v", err)
	}
}
