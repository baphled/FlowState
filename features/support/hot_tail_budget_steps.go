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
	engine         *engine.Engine
	ctx            context.Context
	compactionBudget int
	hotTailBudget  int
	minResults     int
	messageCost    int
	messageCount   int
	summaryCost    int
	rebuilt        []provider.Message
	rebuildErr     error
}

// stubBudgetCounter is a deterministic TokenCounter whose Count returns
// a fixed per-call cost so scenarios can script exact token loads.
type stubBudgetCounter struct{}

func (stubBudgetCounter) Count(text string) int {
	return len(text)
}

func (stubBudgetCounter) ModelLimit(string) int { return 0 }

func (s *hotTailBudgetState) buildEngine() error {
	fixedCounter := &fixedCostCounter{cost: s.messageCost}
	eng, err := engine.NewForHotTailBudgetTest(engine.Config{
		TokenCounter:     fixedCounter,
		CompactionBudget: s.compactionBudget,
		HotTailBudget:    s.hotTailBudget,
		HotTailMin:       s.minResults,
	})
	if err != nil {
		return err
	}
	s.engine = eng
	return nil
}

func (s *hotTailBudgetState) makeMessages() []provider.Message {
	msgs := make([]provider.Message, s.messageCount)
	for i := range msgs {
		msgs[i] = provider.Message{Role: "user", Content: "m"}
	}
	return msgs
}

// fixedCostCounter counts every non-empty content string as exactly one
// fixed cost, so a message of any length costs the scripted per-message
// token figure. The compaction summary bypasses the counter (its cost
// is scripted directly), so a distinctive marker keeps summary counting
// out of the per-message path.
type fixedCostCounter struct {
	cost int
}

func (c *fixedCostCounter) Count(text string) int {
	if strings.HasPrefix(text, "summary-token-cost:") {
		return len(text) - len("summary-token-cost:")
	}
	return c.cost
}

func (c *fixedCostCounter) ModelLimit(string) int { return 0 }

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
		return state.buildEngine()
	})

	ctx.Step(`^(\d+) recent messages each costing (\d+) tokens$`, func(count, cost int) error {
		state.messageCount = count
		state.messageCost = cost
		return nil
	})

	ctx.Step(`^a compaction summary costing (\d+) tokens$`, func(cost int) error {
		state.summaryCost = cost
		return nil
	})

	ctx.Step(`^the token-bounded hot tail is selected$`, func() error {
		msgs := state.makeMessages()
		state.rebuilt, state.rebuildErr = state.engine.SelectHotTailForTest(state.ctx, msgs)
		return nil
	})

	ctx.Step(`^the post-compaction window is rebuilt from the token-bounded hot tail$`, func() error {
		msgs := state.makeMessages()
		summary := "summary-token-cost:" + strings.Repeat("x", state.summaryCost)
		state.rebuilt, state.rebuildErr = state.engine.RebuildWithHotTailBudgetForTest(state.ctx, msgs, summary)
		return nil
	})

	ctx.Step(`^the hot tail has (\d+) messages$`, func(want int) error {
		if state.rebuildErr != nil {
			return state.rebuildErr
		}
		if len(state.rebuilt) != want {
			return errors.New("hot tail has wrong length")
		}
		return nil
	})

	ctx.Step(`^the hot tail retains the most recent messages$`, func() error {
		if len(state.rebuilt) == 0 || state.messageCount == 0 {
			return errors.New("no messages retained")
		}
		last := state.rebuilt[len(state.rebuilt)-1]
		if last.Content != "m" {
			return errors.New("newest message not retained")
		}
		return nil
	})

	ctx.Step(`^the rebuilt window fits the usable budget$`, func() error {
		if state.rebuildErr != nil {
			return state.rebuildErr
		}
		if state.engine.ContextEstimateOverBudgetForTest(state.ctx, state.rebuilt) {
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
