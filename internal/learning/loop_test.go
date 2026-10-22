package learning_test

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/learning"
)

// fakeStore captures entries written to it.
type fakeStore struct {
	mu      sync.Mutex
	entries []learning.Entry
	err     error
}

func (s *fakeStore) Capture(e learning.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, e)
	return nil
}

func (s *fakeStore) Query(_ string) []learning.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]learning.Entry(nil), s.entries...)
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// fakeRecallClient simulates recall search for the detector.
type fakeRecallClient struct {
	matches []learning.RecallMatch
}

func (r *fakeRecallClient) Search(_ string, _ int) ([]learning.RecallMatch, error) {
	return r.matches, nil
}

var _ = Describe("LearningLoop", func() {
	Describe("Notify", func() {
		It("is non-blocking when the buffer is full", func() {
			store := &fakeStore{}
			loop := learning.NewLearningLoop(store)

			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 200 {
					loop.Notify(learning.Trigger{
						ID:       "t",
						AgentID:  "agent",
						Kind:     learning.TriggerKindFailure,
						Source:   learning.TriggerSourceExecutionLoop,
						RaisedAt: time.Now(),
					})
				}
			}()
			Eventually(done, "2s").Should(BeClosed())
		})
	})

	Describe("Run and Stop", func() {
		It("processes failure triggers when LearningOnFailure is enabled", func() {
			store := &fakeStore{}
			loop := learning.NewLearningLoop(store,
				learning.WithLearningOnFailure(true),
			)
			loop.Run()

			loop.Notify(learning.Trigger{
				ID:       "trigger-1",
				AgentID:  "agent-x",
				Kind:     learning.TriggerKindFailure,
				Source:   learning.TriggerSourceExecutionLoop,
				Output:   "failed output",
				RaisedAt: time.Now(),
			})
			loop.Stop()

			Expect(store.count()).To(Equal(1))
		})

		It("ignores failure triggers when LearningOnFailure is disabled", func() {
			store := &fakeStore{}
			loop := learning.NewLearningLoop(store)
			loop.Run()

			loop.Notify(learning.Trigger{
				ID:       "trigger-1",
				AgentID:  "agent-x",
				Kind:     learning.TriggerKindFailure,
				Source:   learning.TriggerSourceExecutionLoop,
				RaisedAt: time.Now(),
			})
			loop.Stop()

			Expect(store.count()).To(Equal(0))
		})

		It("processes novelty triggers when LearningOnNovelty is enabled", func() {
			store := &fakeStore{}
			loop := learning.NewLearningLoop(store,
				learning.WithLearningOnNovelty(true),
			)
			loop.Run()

			loop.Notify(learning.Trigger{
				ID:       "trigger-2",
				AgentID:  "agent-y",
				Kind:     learning.TriggerKindNovelty,
				Source:   learning.TriggerSourceExecutionLoop,
				Output:   "novel output",
				RaisedAt: time.Now(),
			})
			loop.Stop()

			Expect(store.count()).To(Equal(1))
		})

		It("skips novelty triggers when the detector reports a duplicate", func() {
			store := &fakeStore{}
			rc := &fakeRecallClient{
				matches: []learning.RecallMatch{{Score: 0.99}},
			}
			detector := learning.NewDuplicateCheckDetector(rc, 0.9)
			loop := learning.NewLearningLoop(store,
				learning.WithLearningOnNovelty(true),
				learning.WithNoveltyDetector(detector),
			)
			loop.Run()

			loop.Notify(learning.Trigger{
				ID:       "trigger-3",
				AgentID:  "agent-z",
				Kind:     learning.TriggerKindNovelty,
				Source:   learning.TriggerSourceExecutionLoop,
				Output:   "already known",
				RaisedAt: time.Now(),
			})
			loop.Stop()

			Expect(store.count()).To(Equal(0))
		})

		It("captures novelty triggers when the detector reports no duplicate", func() {
			store := &fakeStore{}
			rc := &fakeRecallClient{
				matches: []learning.RecallMatch{{Score: 0.1}},
			}
			detector := learning.NewDuplicateCheckDetector(rc, 0.9)
			loop := learning.NewLearningLoop(store,
				learning.WithLearningOnNovelty(true),
				learning.WithNoveltyDetector(detector),
			)
			loop.Run()

			loop.Notify(learning.Trigger{
				ID:       "trigger-4",
				AgentID:  "agent-w",
				Kind:     learning.TriggerKindNovelty,
				Source:   learning.TriggerSourceExecutionLoop,
				Output:   "truly novel",
				RaisedAt: time.Now(),
			})
			loop.Stop()

			Expect(store.count()).To(Equal(1))
		})
	})

	Describe("DuplicateCheckDetector", func() {
		It("reports novel when recall returns a low score", func() {
			rc := &fakeRecallClient{
				matches: []learning.RecallMatch{{Score: 0.3}},
			}
			detector := learning.NewDuplicateCheckDetector(rc, 0.8)
			Expect(detector.IsNovel("some output")).To(BeTrue())
		})

		It("reports not novel when recall returns a high score", func() {
			rc := &fakeRecallClient{
				matches: []learning.RecallMatch{{Score: 0.95}},
			}
			detector := learning.NewDuplicateCheckDetector(rc, 0.8)
			Expect(detector.IsNovel("some output")).To(BeFalse())
		})

		It("reports novel when recall returns no matches", func() {
			rc := &fakeRecallClient{matches: nil}
			detector := learning.NewDuplicateCheckDetector(rc, 0.8)
			Expect(detector.IsNovel("some output")).To(BeTrue())
		})
	})
})
