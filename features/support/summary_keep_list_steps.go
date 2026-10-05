//go:build e2e

package support

import (
	"context"
	"strings"

	"github.com/cucumber/godog"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// summaryKeepListState carries per-scenario state for the Phase 3 summary
// prompt keep-list scenarios.
type summaryKeepListState struct {
	prompt string
}

// RegisterSummaryKeepListSteps wires the Phase 3 summary keep-list
// scenarios to the production summary prompt renderer.
func RegisterSummaryKeepListSteps(ctx *godog.ScenarioContext) {
	state := &summaryKeepListState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.prompt = ""
		return c, nil
	})

	ctx.Step(`^the compaction summary prompt is rendered$`, func() error {
		msgs := []provider.Message{{Role: "user", Content: "summarise me"}}
		prompt, err := flowctx.RenderSummaryPrompt(msgs)
		if err != nil {
			return err
		}
		state.prompt = prompt + "\n" + flowctx.SummaryPromptSystem
		return nil
	})

	ctx.Step(`^the prompt instructs the model to capture the session intent$`, func() error {
		return keepListContains(state.prompt, "intent")
	})

	ctx.Step(`^the prompt instructs the model to capture decisions made$`, func() error {
		return keepListContains(state.prompt, "decision")
	})

	ctx.Step(`^the prompt instructs the model to capture errors encountered and their fixes$`, func() error {
		return keepListContains(state.prompt, "error")
	})

	ctx.Step(`^the prompt instructs the model to capture pending work$`, func() error {
		return keepListContains(state.prompt, "pending work")
	})

	ctx.Step(`^the prompt instructs the model to capture critical data$`, func() error {
		return keepListContains(state.prompt, "critical data")
	})
}

// keepListContains asserts the rendered prompt carries a keep-list term.
func keepListContains(prompt, term string) error {
	if strings.Contains(strings.ToLower(prompt), strings.ToLower(term)) {
		return nil
	}
	return &keepListError{term: term}
}

// keepListError describes a missing keep-list section.
type keepListError struct{ term string }

// Error implements the error interface.
func (e *keepListError) Error() string {
	return "summary prompt missing keep-list section: " + e.term
}
