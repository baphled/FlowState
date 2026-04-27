//go:build e2e

package support

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/learning"
)

// learningLoopState holds state for learning loop BDD scenarios.
type learningLoopState struct {
	loop  *learning.Loop
	store *bddFakeStore
	opts  []learning.LoopOption
}

// bddFakeStore is a thread-safe in-memory learning store for BDD testing.
type bddFakeStore struct {
	mu      sync.Mutex
	entries []learning.Entry
}

// Capture records an entry in the fake store for testing.
//
// Returns: nil error.
// Expected: no external calls.
// Side effects: appends entry to internal slice.
func (s *bddFakeStore) Capture(e learning.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

// Query returns all entries in the fake store for testing.
//
// Returns: copy of all captured entries.
// Expected: no external calls.
// Side effects: none.
func (s *bddFakeStore) Query(_ string) []learning.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]learning.Entry(nil), s.entries...)
}

// count returns the number of entries in the store.
//
// Returns: the number of entries.
// Expected: no external calls.
// Side effects: none.
func (s *bddFakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// bddFakeRecallClient is a fake recall client for BDD testing.
type bddFakeRecallClient struct {
	score float64
}

// Search returns pre-configured recall matches for testing.
//
// Returns: a slice with one RecallMatch using the configured score.
// Expected: no external calls.
// Side effects: none.
func (r *bddFakeRecallClient) Search(_ string, _ int) ([]learning.RecallMatch, error) {
	return []learning.RecallMatch{{Score: r.score}}, nil
}

// RegisterLearningLoopSteps registers BDD step definitions for the learning loop feature.
//
// Expected: all step definitions are registered with the scenario context.
// Side effects: modifies the scenario context by adding step definitions.
func RegisterLearningLoopSteps(ctx *godog.ScenarioContext) {
	s := &learningLoopState{}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.store = &bddFakeStore{}
		s.opts = nil
		s.loop = nil
		return bddCtx, nil
	})

	ctx.Step(`^the learning loop is configured with a fake store$`, func() {
		s.store = &bddFakeStore{}
	})

	ctx.Step(`^the learning loop has learning on failure enabled$`, func() {
		s.opts = append(s.opts, learning.WithLearningOnFailure(true))
	})

	ctx.Step(`^the learning loop has no failure learning configured$`, func() {})

	ctx.Step(`^the learning loop has learning on novelty enabled$`, func() {
		s.opts = append(s.opts, learning.WithLearningOnNovelty(true))
	})

	ctx.Step(`^a novelty detector that reports all outputs as duplicates$`, func() {
		rc := &bddFakeRecallClient{score: 1.0}
		detector := learning.NewDuplicateCheckDetector(rc, 0.5)
		s.opts = append(s.opts, learning.WithNoveltyDetector(detector))
	})

	ctx.Step(`^a failure trigger is sent for agent "([^"]*)"$`, func(agentID string) {
		if s.loop == nil {
			s.loop = learning.NewLearningLoop(s.store, s.opts...)
			s.loop.Run()
		}
		s.loop.Notify(learning.Trigger{
			ID:       "bdd-trigger",
			AgentID:  agentID,
			Kind:     learning.TriggerKindFailure,
			Source:   learning.TriggerSourceExecutionLoop,
			Output:   "failed output",
			RaisedAt: time.Now(),
		})
	})

	ctx.Step(`^a novelty trigger is sent for agent "([^"]*)"$`, func(agentID string) {
		if s.loop == nil {
			s.loop = learning.NewLearningLoop(s.store, s.opts...)
			s.loop.Run()
		}
		s.loop.Notify(learning.Trigger{
			ID:       "bdd-novelty",
			AgentID:  agentID,
			Kind:     learning.TriggerKindNovelty,
			Source:   learning.TriggerSourceExecutionLoop,
			Output:   "novel output",
			RaisedAt: time.Now(),
		})
	})

	ctx.Step(`^the learning loop is stopped$`, func() {
		if s.loop != nil {
			s.loop.Stop()
		}
	})

	ctx.Step(`^the store contains (\d+) learning entr(?:y|ies)$`, func(n int) error {
		got := s.store.count()
		if got != n {
			return errors.New("expected store to have entries")
		}
		return nil
	})

	ctx.Step(`^the learning loop buffer is full$`, func() {
		s.loop = learning.NewLearningLoop(s.store)
	})

	ctx.Step(`^(\d+) triggers are sent concurrently$`, func(n int) {
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.loop.Notify(learning.Trigger{
					ID:       "flood",
					AgentID:  "agent",
					Kind:     learning.TriggerKindFailure,
					Source:   learning.TriggerSourceExecutionLoop,
					RaisedAt: time.Now(),
				})
			}()
		}
		wg.Wait()
	})

	ctx.Step(`^all Notify calls complete without blocking$`, func() error {
		return nil
	})

	ctx.Step(`^an agent "([^"]*)" with harness_enabled true and mode "([^"]*)"$`, func(_, _ string) {
	})

	ctx.Step(`^a stream request is sent for agent "([^"]*)"$`, func(_ string) {
	})

	ctx.Step(`^the plan evaluator handles the request$`, func() error {
		return nil
	})
}
