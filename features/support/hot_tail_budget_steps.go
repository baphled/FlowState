//go:build e2e

package support

import (
	"context"
	"errors"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// hotTailBudgetState carries per-scenario state for the @compaction
// Phase 2 hot-tail budget scenarios. The Before hook zeroes it so state
// never leaks between tests.
type hotTailBudgetState struct {
	engine           *engine.Engine
	ctx              context.Context
	compactionBudget int
	hotTailBudget    int
	minResults       int
	messageCost      int
	messageCount     int
	summaryCost      int
	rebuilt          []provider.Message
	rebuildErr       error
}

// hotTailCostCounter counts every non-empty content string as exactly
// one scripted per-message cost so scenarios can pin exact token
// loads. Summary text is encoded as one word per token so the summary
// cost stays inside the same counting rule without a second counter.
type hotTailCostCounter struct {
	cost  int
	limit int
}

func (c hotTailCostCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text)) * c.cost
}

func (c hotTailCostCounter) ModelLimit(string) int { return c.limit }

// newHotTailCounter returns a counter whose ModelLimit resolves the
// scenario's compaction budget and whose per-message content cost is
// one word at cost.
func (s *hotTailBudgetState) newHotTailCounter() hotTailCostCounter {
	return hotTailCostCounter{cost: s.messageCost, limit: s.compactionBudget}
}

func (s *hotTailBudgetState) makeMessages() []provider.Message {
	msgs := make([]provider.Message, s.messageCount)
	for i := range msgs {
		msgs[i] = provider.Message{Role: "user", Content: "m"}
	}
	return msgs
}

func (s *hotTailBudgetState) summaryText() string {
	return strings.Repeat("s ", s.summaryCost)
}

// RegisterHotTailBudgetSteps wires the @compaction Phase 2 hot-tail
// budget scenarios to the production engine rebuild path.
func RegisterHotTailBudgetSteps(ctx *godog.ScenarioContext) {
	state := &hotTailBudgetState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.engine = nil
		state.ctx = c
		state.compactionBudget = 0
		state.hotTailBudget = 0
		state.minResults = 0
		state.messageCost = 0
		state.messageCount = 0
		state.summaryCost = 0
		state.rebuilt = nil
		state.rebuildErr = nil
		return c, nil
	})

	ctx.Step(`^an engine with a token counter and compaction budget of (\d+) tokens$`, func(budget int) error {
		state.compactionBudget = budget
		return nil
	})

	ctx.Step(`^a hot tail budget of (\d+) tokens and a minimum floor of (\d+) recent messages$`, func(budget, floor int) error {
		state.hotTailBudget = budget
		state.minResults = floor
		state.engine = engine.New(engine.Config{
			TokenCounter:            state.newHotTailCounter(),
			SystemPromptBudget:      state.compactionBudget,
			OutputReserveForTests:   16,
			HotTailBudgetForTests:   state.hotTailBudget,
			HotTailMinFloorForTests: state.minResults,
		})
		return nil
	})

	ctx.Step(`^(\d+) recent messages each costing (\d+) tokens$`, func(count, cost int) error {
		state.messageCount = count
		state.messageCost = cost
		state.engine = engine.New(engine.Config{
			TokenCounter:            state.newHotTailCounter(),
			SystemPromptBudget:      state.compactionBudget,
			OutputReserveForTests:   16,
			HotTailBudgetForTests:   state.hotTailBudget,
			HotTailMinFloorForTests: state.minResults,
		})
		return nil
	})

	ctx.Step(`^a compaction summary costing (\d+) tokens$`, func(cost int) error {
		state.summaryCost = cost
		return nil
	})

	ctx.Step(`^the token-bounded hot tail is selected$`, func() error {
		state.rebuilt, state.rebuildErr = state.engine.SelectHotTailBudgetedForTesting(state.ctx, state.makeMessages())
		return nil
	})

	ctx.Step(`^the post-compaction window is rebuilt from the token-bounded hot tail$`, func() error {
		state.rebuilt, state.rebuildErr = state.engine.RebuildHotTailBudgetedForTesting(state.ctx, state.makeMessages(), state.summaryText())
		return nil
	})

	ctx.Step(`^the hot tail has (\d+) messages$`, func(want int) error {
		if state.rebuildErr != nil {
			return state.rebuildErr
		}
		if len(state.rebuilt) != want {
			return errors.New("hot tail length mismatch: got")
		}
		return nil
	})

	ctx.Step(`^the hot tail retains the most recent messages$`, func() error {
		if len(state.rebuilt) == 0 || state.messageCount == 0 {
			return errors.New("no messages retained")
		}
		if state.rebuilt[len(state.rebuilt)-1].Content != "m" {
			return errors.New("newest message not retained")
		}
		return nil
	})

	ctx.Step(`^the rebuilt window fits the usable budget$`, func() error {
		if state.rebuildErr != nil {
			return state.rebuildErr
		}
		if state.engine.ContextEstimateOverBudgetForTesting(state.ctx, state.rebuilt) {
			return errors.New("rebuilt window still over budget")
		}
		return nil
	})

	ctx.Step(`^the rebuilt window retains the newest live message$`, func() error {
		if len(state.rebuilt) == 0 {
			return errors.New("rebuilt window is empty")
		}
		if state.rebuilt[len(state.rebuilt)-1].Content != "m" {
			return errors.New("newest live message not retained")
		}
		return nil
	})

	ctx.Step(`^the rebuild fails loudly with a compaction insufficient error$`, func() error {
		if state.rebuildErr == nil {
			return errors.New("rebuild did not fail")
		}
		if !errors.Is(state.rebuildErr, engine.ErrCompactionInsufficient) {
			return errors.New("rebuild error is not ErrCompactionInsufficient: " + state.rebuildErr.Error())
		}
		return nil
	})
}
