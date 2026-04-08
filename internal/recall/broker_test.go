package recall_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/recall"
)

type fakeObservationSource struct {
	results []recall.Observation
	err     error
	started *atomic.Int32
	release <-chan struct{}
}

func (f *fakeObservationSource) Query(ctx context.Context, query string, limit int) ([]recall.Observation, error) {
	if f.started != nil {
		f.started.Add(1)
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if limit <= 0 || len(f.results) <= limit {
		return append([]recall.Observation(nil), f.results...), nil
	}
	return append([]recall.Observation(nil), f.results[:limit]...), nil
}

// observation builds a recall observation for test expectations.
func observation(id, source, agent string, at time.Time) recall.Observation {
	return recall.Observation{
		ID:        id,
		Source:    source,
		AgentID:   agent,
		Timestamp: at,
		Content:   id,
	}
}

var _ = Describe("RecallBroker", func() {
	It("merges results from all 4 sources", func() {
		broker := recall.NewRecallBroker(
			&fakeObservationSource{results: []recall.Observation{observation("session", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{results: []recall.Observation{observation("chain", "chain", "agent-a", time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{results: []recall.Observation{observation("hierarchy", "hierarchy", "agent-a", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{results: []recall.Observation{observation("learning", "learning", "agent-a", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC))}},
		)

		results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(4))
		Expect(results).To(ConsistOf(
			observation("session", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)),
			observation("chain", "chain", "agent-a", time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC)),
			observation("hierarchy", "hierarchy", "agent-a", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)),
			observation("learning", "learning", "agent-a", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)),
		))
	})

	It("ranks by freshness (newest first)", func() {
		broker := recall.NewRecallBroker(
			&fakeObservationSource{results: []recall.Observation{observation("oldest", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{results: []recall.Observation{observation("middle", "chain", "agent-a", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{results: []recall.Observation{observation("newest", "hierarchy", "agent-a", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{results: []recall.Observation{observation("recent", "learning", "agent-a", time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC))}},
		)

		results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 3)

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(3))
		Expect(results[0].ID).To(Equal("newest"))
		Expect(results[1].ID).To(Equal("middle"))
		Expect(results[2].ID).To(Equal("recent"))
	})

	It("enforces AgentID scoping", func() {
		broker := recall.NewRecallBroker(
			&fakeObservationSource{results: []recall.Observation{
				observation("match-session", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)),
				observation("other-session", "session", "agent-b", time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC)),
			}},
			&fakeObservationSource{results: []recall.Observation{
				observation("match-chain", "chain", "agent-a", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)),
				observation("other-chain", "chain", "agent-b", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)),
			}},
			&fakeObservationSource{results: []recall.Observation{
				observation("match-hierarchy", "hierarchy", "agent-a", time.Date(2026, 4, 8, 14, 0, 0, 0, time.UTC)),
			}},
			&fakeObservationSource{results: []recall.Observation{
				observation("match-learning", "learning", "agent-a", time.Date(2026, 4, 8, 15, 0, 0, 0, time.UTC)),
			}},
		)

		results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(4))
		for _, result := range results {
			Expect(result.AgentID).To(Equal("agent-a"))
		}
	})

	It("handles source errors gracefully", func() {
		broker := recall.NewRecallBroker(
			&fakeObservationSource{results: []recall.Observation{observation("session", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{err: errors.New("chain unavailable")},
			&fakeObservationSource{results: []recall.Observation{observation("hierarchy", "hierarchy", "agent-a", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{results: []recall.Observation{observation("learning", "learning", "agent-a", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC))}},
		)

		results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(3))
		Expect(results).To(ConsistOf(
			observation("session", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)),
			observation("hierarchy", "hierarchy", "agent-a", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)),
			observation("learning", "learning", "agent-a", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)),
		))
	})

	It("calls sources in parallel", func() {
		started := atomic.Int32{}
		release := make(chan struct{})
		broker := recall.NewRecallBroker(
			&fakeObservationSource{started: &started, release: release, results: []recall.Observation{observation("session", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{started: &started, release: release, results: []recall.Observation{observation("chain", "chain", "agent-a", time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{started: &started, release: release, results: []recall.Observation{observation("hierarchy", "hierarchy", "agent-a", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC))}},
			&fakeObservationSource{started: &started, release: release, results: []recall.Observation{observation("learning", "learning", "agent-a", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC))}},
		)

		resultCh := make(chan []recall.Observation, 1)
		errCh := make(chan error, 1)
		go func() {
			results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- results
		}()

		Eventually(started.Load).Should(Equal(int32(4)))
		close(release)

		Eventually(resultCh).Should(Receive(HaveLen(4)))
		Consistently(errCh).ShouldNot(Receive())
	})
})
