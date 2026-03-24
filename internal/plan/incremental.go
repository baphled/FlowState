package plan

import (
	"context"
	"fmt"
	"strings"
)

// GenerationPhase represents a phase of incremental plan generation.
type GenerationPhase string

const (
	// PhaseRationale is the rationale generation phase.
	PhaseRationale GenerationPhase = "Rationale"
	// PhaseTasks is the tasks generation phase.
	PhaseTasks GenerationPhase = "Tasks"
	// PhaseWaves is the waves generation phase.
	PhaseWaves GenerationPhase = "Waves"
	// PhaseSuccessCriteria is the success criteria generation phase.
	PhaseSuccessCriteria GenerationPhase = "SuccessCriteria"
	// PhaseRisks is the risks generation phase.
	PhaseRisks GenerationPhase = "Risks"
)

// AllPhases is the ordered list of all generation phases.
var AllPhases = []GenerationPhase{
	PhaseRationale,
	PhaseTasks,
	PhaseWaves,
	PhaseSuccessCriteria,
	PhaseRisks,
}

// PhaseResult holds the output of generating a single phase.
type PhaseResult struct {
	Phase  GenerationPhase
	Output string
}

// IncrementalResult holds the aggregated result of all phases.
type IncrementalResult struct {
	PhaseResults []PhaseResult
	FullPlan     string
}

// IncrementalGenerator generates a plan incrementally, phase by phase.
type IncrementalGenerator struct {
	Streamer   Streamer
	MaxRetries int
}

// Generate produces a plan by generating each phase sequentially.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - agentID identifies the planner agent.
//   - baseMessage is the base planning prompt to extend per phase.
//
// Returns:
//   - An IncrementalResult containing phase outputs and the assembled full plan.
//   - An error if any phase produces empty output or the context is cancelled.
//
// Side effects:
//   - Streams responses from the LLM for each phase, retrying up to MaxRetries times.
func (g *IncrementalGenerator) Generate(ctx context.Context, agentID, baseMessage string) (*IncrementalResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	maxRetries := g.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}

	var results []PhaseResult
	for _, phase := range AllPhases {
		prompt := baseMessage + "\n\nGenerate ONLY the " + string(phase) + " section of the plan."

		var output string
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			ch, err := g.Streamer.Stream(ctx, agentID, prompt)
			if err != nil {
				return nil, err
			}

			var builder strings.Builder
			for chunk := range ch {
				builder.WriteString(chunk.Content)
			}

			output = strings.TrimSpace(builder.String())
			if output != "" {
				break
			}
		}

		if output == "" {
			return nil, fmt.Errorf("phase %s produced empty output after %d attempts", phase, maxRetries)
		}

		results = append(results, PhaseResult{Phase: phase, Output: output})
	}

	outputs := make([]string, len(results))
	for i, r := range results {
		outputs[i] = r.Output
	}

	return &IncrementalResult{
		PhaseResults: results,
		FullPlan:     strings.Join(outputs, "\n\n"),
	}, nil
}
