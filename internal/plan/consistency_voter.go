package plan

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
func NewConsistencyVoter(config VoterConfig, projectRoot string) *ConsistencyVoter {
	return &ConsistencyVoter{config: config, projectRoot: projectRoot}
}

// Streamer is defined in harness.go, do not redefine here.

// Vote runs the self-consistency voting process.
func (v *ConsistencyVoter) Vote(ctx context.Context, streamer Streamer, req VoteRequest) (*VoteResult, error) {
	result := &VoteResult{
		BestPlan:  req.InitialPlan,
		BestScore: req.InitialScore,
	}

	// If voter is disabled, return initial plan
	if !v.config.Enabled {
		return result, nil
	}

	// If initial score is above threshold, no need to generate variants
	if req.InitialScore >= v.config.Threshold {
		return result, nil
	}

	// Score is below threshold, trigger variant generation
	result.WasTriggered = true

	// Generate variants in parallel
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

	// Close channel when all goroutines finish
	go func() {
		wg.Wait()
		close(variantChan)
	}()

	// Collect variants
	variants := []string{}
	for variant := range variantChan {
		variants = append(variants, variant)
	}

	result.VariantsGenerated = len(variants)

	// Pick the best variant (for now, just pick the first one)
	if len(variants) > 0 {
		result.BestPlan = variants[0]
	}

	return result, nil
}
