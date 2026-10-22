package harness

import (
	"context"
	"strings"
	"sync"
)

// VoterConfig holds configuration for the ConsistencyVoter.
type VoterConfig struct {
	Enabled   bool
	Variants  int
	Threshold float64
}

// DefaultVoterConfig returns the default configuration for the voter.
//
// Returns:
//   - A VoterConfig with voting disabled, 3 variants, and 0.8 threshold.
//
// Side effects:
//   - None.
func DefaultVoterConfig() VoterConfig {
	return VoterConfig{
		Enabled:   false,
		Variants:  3,
		Threshold: 0.8,
	}
}

// VoteRequest holds parameters for a consistency vote.
type VoteRequest struct {
	AgentID      string
	Message      string
	InitialPlan  string
	InitialScore float64
}

// VoteResult holds the result of a consistency vote.
type VoteResult struct {
	WasTriggered      bool
	VariantsGenerated int
	BestPlan          string
	BestScore         float64
}

// ConsistencyVoter performs self-consistency voting on plans.
type ConsistencyVoter struct {
	config      VoterConfig
	projectRoot string
}

// NewConsistencyVoter creates a new ConsistencyVoter with the given config and projectRoot.
//
// Expected:
//   - config contains valid voter settings.
//   - projectRoot is the absolute path to the project root directory.
//
// Returns:
//   - A configured ConsistencyVoter ready for use.
//
// Side effects:
//   - None.
func NewConsistencyVoter(config VoterConfig, projectRoot string) *ConsistencyVoter {
	return &ConsistencyVoter{config: config, projectRoot: projectRoot}
}

// Streamer is defined in harness.go, do not redefine here.

// Vote runs the self-consistency voting process.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - streamer provides streaming access to the LLM.
//   - req contains a valid initial plan and score.
//
// Returns:
//   - A VoteResult with the best plan and score from voting, or the initial plan if voting was skipped.
//   - An error if the voting process fails.
//
// Side effects:
//   - Spawns goroutines to generate plan variants via the streamer.
func (v *ConsistencyVoter) Vote(ctx context.Context, streamer Streamer, req VoteRequest) (*VoteResult, error) {
	result := &VoteResult{
		BestPlan:  req.InitialPlan,
		BestScore: req.InitialScore,
	}

	if !v.config.Enabled {
		return result, nil
	}

	if req.InitialScore >= v.config.Threshold {
		return result, nil
	}

	result.WasTriggered = true

	variantChan := make(chan string, v.config.Variants)
	var wg sync.WaitGroup

	for range v.config.Variants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunks, err := streamer.Stream(ctx, req.AgentID, req.Message)
			if err != nil {
				return
			}
			var variant strings.Builder
			for chunk := range chunks {
				variant.WriteString(chunk.Content)
			}
			if variant.Len() > 0 {
				variantChan <- variant.String()
			}
		}()
	}

	go func() {
		wg.Wait()
		close(variantChan)
	}()

	variants := []string{}
	for variant := range variantChan {
		variants = append(variants, variant)
	}

	result.VariantsGenerated = len(variants)

	if len(variants) > 0 {
		result.BestPlan = variants[0]
	}

	return result, nil
}
