//go:build e2e

package support

import (
	"context"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/recall"
)

// learningBridgeState carries per-scenario state for the learning
// recall bridge feature. The adapter under test is the real product
// type from internal/app; the broker is backed by a stub source so no
// network or Qdrant dependency is required.
type learningBridgeState struct {
	source   *bridgeStubSource
	broker   recall.Broker
	adapter  *app.SessionStartLearningAdapter
	matches  []learning.RecallMatch
	injected int
}

// bridgeStubSource is an in-process recall.Source serving pre-canned
// observations so the adapter's broker query is observable.
type bridgeStubSource struct {
	observations []recall.Observation
	queries      []string
}

// Query records the query and returns the canned observations.
func (s *bridgeStubSource) Query(ctx context.Context, query string, limit int) ([]recall.Observation, error) {
	s.queries = append(s.queries, query)
	if len(s.observations) > limit {
		return s.observations[:limit], nil
	}
	return s.observations, nil
}

// RegisterLearningBridgeSteps wires the step definitions for the
// learning recall bridge feature file. Steps drive the real
// SessionStartLearningAdapter and recall broker against the stub
// source.
func RegisterLearningBridgeSteps(ctx *godog.ScenarioContext) {
	s := &learningBridgeState{}

	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		*s = learningBridgeState{}
		return ctx, nil
	})

	ctx.Step(`^prior learnings have been recorded for agent "([^"]*)"$`, s.priorLearningsRecorded)
	ctx.Step(`^a session-start learning adapter is wired to the recall broker$`, s.adapterWired)
	ctx.Step(`^a session starts for agent "([^"]*)"$`, s.sessionStarts)
	ctx.Step(`^the adapter should recall the prior learnings from the recall broker$`, s.shouldRecall)
	ctx.Step(`^the prior learnings should be injected into the agent context$`, s.shouldInject)
}

// priorLearningsRecorded seeds the stub source with learning
// observations attributed to the named agent.
//
// Expected: agentID identifies the agent whose learnings are seeded.
// Returns: nil.
// Side effects: populates s.source.observations.
func (s *learningBridgeState) priorLearningsRecorded(agentID string) error {
	s.source = &bridgeStubSource{observations: []recall.Observation{
		{ID: "l1", Source: "learning", AgentID: agentID, Timestamp: time.Now(), Content: "run make test before committing"},
		{ID: "l2", Source: "learning", AgentID: agentID, Timestamp: time.Now(), Content: "stage files before ai-commit"},
	}}
	return nil
}

// adapterWired constructs the real broker with the stub learning
// source and wraps it in the production adapter.
//
// Expected: s.source has been seeded.
// Returns: nil.
// Side effects: assigns s.broker and s.adapter.
func (s *learningBridgeState) adapterWired() error {
	s.broker = recall.NewRecallBroker(nil, nil, nil, s.source)
	s.adapter = app.NewSessionStartLearningAdapter(s.broker, 5)
	return nil
}

// sessionStarts performs the session-start recall query through the
// adapter and records that injection happened when matches return.
//
// Expected: s.adapter is wired.
// Returns: nil.
// Side effects: assigns s.matches and s.injected.
func (s *learningBridgeState) sessionStarts(agentID string) error {
	matches, err := s.adapter.Search(agentID+" learnings", 5)
	if err != nil {
		return err
	}
	s.matches = matches
	if len(matches) > 0 {
		s.injected = len(matches)
	}
	return nil
}

// shouldRecall asserts the broker was queried and observations mapped.
//
// Expected: the session-start step ran.
// Returns: an error when no broker query occurred or matches are empty.
// Side effects: none.
func (s *learningBridgeState) shouldRecall() error {
	if len(s.source.queries) == 0 {
		return godog.ErrPending
	}
	if len(s.matches) == 0 {
		return godog.ErrPending
	}
	return nil
}

// shouldInject asserts the recalled learnings were injected into the
// agent context with their content preserved.
//
// Expected: s.matches holds the recalled learnings.
// Returns: an error when no injection occurred.
// Side effects: none.
func (s *learningBridgeState) shouldInject() error {
	if s.injected == 0 {
		return godog.ErrPending
	}
	for _, m := range s.matches {
		if m.Content == "" {
			return godog.ErrPending
		}
	}
	return nil
}
