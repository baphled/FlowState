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

	It("handles partial source errors gracefully (mixed success/failure)", func() {
		// Pre-existing behaviour pin: one source failing must not poison
		// the broker — callers that have other working sources still get
		// observations and a nil error. The pre-M9 implementation
		// extended this lenience to the *all-sources-failed* case too,
		// which is what M9 fixes (see the dedicated spec below).
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

	// M9 — Bug Hunt Findings (May 2026). When every configured source
	// fails the pre-M9 broker swallowed the errors, logged a warning,
	// and returned (nil, nil). Recall failures (Qdrant down, embedding-
	// model dimension mismatch, network timeout) were thereby
	// indistinguishable from "no relevant data". Callers MUST be able
	// to discriminate the two so the engine can surface the failure on
	// the wire instead of silently degrading. The fix returns a typed
	// sentinel error (recall.ErrAllSourcesFailed) and joins the per-
	// source errors via errors.Join so callers can either react on the
	// sentinel alone (cheap dashboards) or unwrap each underlying
	// cause (operator triage).
	Describe("M9: discriminating total recall failure from zero results", func() {
		It("returns ErrAllSourcesFailed when every configured source errors", func() {
			chainErr := errors.New("qdrant: connection refused")
			hierarchyErr := errors.New("network: i/o timeout")
			learningErr := errors.New("dimension mismatch")
			broker := recall.NewRecallBroker(
				&fakeObservationSource{err: errors.New("session unavailable")},
				&fakeObservationSource{err: chainErr},
				&fakeObservationSource{err: hierarchyErr},
				&fakeObservationSource{err: learningErr},
			)

			results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)

			Expect(results).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, recall.ErrAllSourcesFailed)).To(BeTrue(),
				"all-sources-failed must surface as recall.ErrAllSourcesFailed (got %v)", err)
			// Underlying per-source errors are joined so operators can
			// unwrap individual causes during triage.
			Expect(errors.Is(err, chainErr)).To(BeTrue(),
				"joined error must preserve underlying chain error for triage")
			Expect(errors.Is(err, hierarchyErr)).To(BeTrue(),
				"joined error must preserve underlying hierarchy error for triage")
		})

		It("does not return ErrAllSourcesFailed when at least one source succeeds with results", func() {
			broker := recall.NewRecallBroker(
				&fakeObservationSource{err: errors.New("chain unavailable")},
				&fakeObservationSource{err: errors.New("hierarchy unavailable")},
				&fakeObservationSource{err: errors.New("learning unavailable")},
				&fakeObservationSource{results: []recall.Observation{observation("session", "session", "agent-a", time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC))}},
			)

			results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)

			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
		})

		It("does not return ErrAllSourcesFailed when sources return zero observations without error", func() {
			// Regression guard: an empty corpus is NOT the same as a
			// failure. The whole point of M9 is to keep this case
			// distinct from "broker is broken".
			broker := recall.NewRecallBroker(
				&fakeObservationSource{},
				&fakeObservationSource{},
				&fakeObservationSource{},
				&fakeObservationSource{},
			)

			results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)

			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(BeEmpty())
		})

		It("ignores nil sources when counting failures (treats them as absent, not failed)", func() {
			// nil sources are a supported configuration (broker accepts
			// nil for every positional arg). They must not skew the
			// all-failed determination.
			broker := recall.NewRecallBroker(
				nil,
				nil,
				nil,
				&fakeObservationSource{err: errors.New("learning unavailable")},
			)

			results, err := broker.Query(context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"), "needle", 10)

			Expect(results).To(BeEmpty())
			Expect(err).To(MatchError(recall.ErrAllSourcesFailed),
				"the single configured source failed — that's all-sources-failed, nil sources don't count as successes")
		})
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

	Describe("date-range filtering", func() {
		It("filters observations to only those within the specified range", func() {
			broker := recall.NewRecallBroker(
				&fakeObservationSource{results: []recall.Observation{
					observation("old", "session", "agent-a", time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)),
					observation("recent", "session", "agent-a", time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)),
					observation("future", "session", "agent-a", time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)),
				}},
				nil, nil, nil,
			)

			ctx := recall.WithDateRange(
				context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"),
				recall.DateRange{
					From: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2026, 4, 30, 23, 59, 59, 0, time.UTC),
				},
			)

			results, err := broker.Query(ctx, "needle", 10)

			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ID).To(Equal("recent"))
		})

		It("returns all observations when no date range is set", func() {
			broker := recall.NewRecallBroker(
				&fakeObservationSource{results: []recall.Observation{
					observation("old", "session", "agent-a", time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)),
					observation("recent", "session", "agent-a", time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)),
				}},
				nil, nil, nil,
			)

			ctx := context.WithValue(context.Background(), learning.AgentIDKey, "agent-a")
			results, err := broker.Query(ctx, "needle", 10)

			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(2))
		})

		It("supports open-ended From filter", func() {
			broker := recall.NewRecallBroker(
				&fakeObservationSource{results: []recall.Observation{
					observation("old", "session", "agent-a", time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)),
					observation("recent", "session", "agent-a", time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)),
				}},
				nil, nil, nil,
			)

			ctx := recall.WithDateRange(
				context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"),
				recall.DateRange{From: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
			)

			results, err := broker.Query(ctx, "needle", 10)

			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ID).To(Equal("recent"))
		})

		It("supports open-ended To filter", func() {
			broker := recall.NewRecallBroker(
				&fakeObservationSource{results: []recall.Observation{
					observation("old", "session", "agent-a", time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)),
					observation("recent", "session", "agent-a", time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)),
				}},
				nil, nil, nil,
			)

			ctx := recall.WithDateRange(
				context.WithValue(context.Background(), learning.AgentIDKey, "agent-a"),
				recall.DateRange{To: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
			)

			results, err := broker.Query(ctx, "needle", 10)

			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ID).To(Equal("old"))
		})
	})
})
