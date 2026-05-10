package recall_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// failingSearchChainStore is a ChainContextStore whose Search method
// returns an error. GetByAgent succeeds so the fallback path can
// emit recency-ordered messages — that's the bug we're pinning: the
// pre-M9 ChainSearchTool was unable to distinguish genuine Search
// failure from a zero-result Search, both fell to the same silent
// recency fallback.
type failingSearchChainStore struct {
	searchErr error
	recent    []provider.Message
}

func (f *failingSearchChainStore) Append(_ string, _ provider.Message) error { return nil }
func (f *failingSearchChainStore) Search(_ context.Context, _ string, _ int) ([]recall.SearchResult, error) {
	return nil, f.searchErr
}
func (f *failingSearchChainStore) GetByAgent(_ string, last int) ([]provider.Message, error) {
	if last > 0 && len(f.recent) > last {
		return f.recent[len(f.recent)-last:], nil
	}
	return f.recent, nil
}
func (f *failingSearchChainStore) ChainID() string { return "test-chain" }

// captureBus subscribes to both the success and failure recall chain
// search events and records every published payload so a single
// spec can assert exactly which topic fired.
type captureBus struct {
	bus      *eventbus.EventBus
	mu       sync.Mutex
	searched []events.RecallChainSearchEventData
	failed   []events.RecallChainSearchFailedEventData
}

func newCaptureBus() *captureBus {
	c := &captureBus{bus: eventbus.NewEventBus()}
	c.bus.Subscribe(events.EventRecallChainSearched, func(ev any) {
		if e, ok := ev.(*events.RecallChainSearchEvent); ok {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.searched = append(c.searched, e.Data)
		}
	})
	c.bus.Subscribe(events.EventRecallChainSearchFailed, func(ev any) {
		if e, ok := ev.(*events.RecallChainSearchFailedEvent); ok {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.failed = append(c.failed, e.Data)
		}
	})
	return c
}

func (c *captureBus) snapshotFailed() []events.RecallChainSearchFailedEventData {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]events.RecallChainSearchFailedEventData, len(c.failed))
	copy(out, c.failed)
	return out
}

var _ = Describe("ChainSearchTool — M9 failure discrimination", func() {
	// M9 (Bug Hunt Findings May 2026). Pre-fix, `ChainSearchTool.Execute`
	// fell back to recency on `err != nil || len(results) == 0` —
	// genuine Qdrant / embedding / network failures were
	// indistinguishable from zero-result queries. A long thread
	// could silently degrade to recency-only retrieval with no
	// observable signal. The fix surfaces a typed signal on the
	// bus (recall.chain.search.failed) AND returns the error from
	// Execute so the engine's tool.execute.error path fires.

	Context("when the underlying chain store Search call returns an error", func() {
		It("publishes recall.chain.search.failed with the underlying reason", func() {
			cb := newCaptureBus()
			store := &failingSearchChainStore{
				searchErr: errors.New("qdrant: dial tcp: connection refused"),
				recent:    []provider.Message{{Role: "assistant", Content: "recent msg"}},
			}
			t := recall.NewChainSearchTool(store, nil, nil, cb.bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "ses-9")
			_, err := t.Execute(ctx, tool.Input{
				Name:      "chain_search",
				Arguments: map[string]any{"query": "needle", "agent_id": "agent-a"},
			})

			Expect(err).To(HaveOccurred(),
				"genuine Search failure must surface as a tool execution error so the engine's tool.execute.error path fires")
			failed := cb.snapshotFailed()
			Expect(failed).To(HaveLen(1),
				"a recall.chain.search.failed event must be published on the bus once per genuine Search failure")
			Expect(failed[0].SessionID).To(Equal("ses-9"))
			Expect(failed[0].AgentID).To(Equal("agent-a"))
			Expect(failed[0].Query).To(Equal("needle"))
			Expect(failed[0].Reason).To(ContainSubstring("connection refused"),
				"the underlying error message must be carried so dashboards can classify by failure mode")
		})

		It("still returns recency-fallback content in the result so the model is not left empty-handed", func() {
			cb := newCaptureBus()
			store := &failingSearchChainStore{
				searchErr: errors.New("network timeout"),
				recent:    []provider.Message{{Role: "assistant", Content: "fallback content"}},
			}
			t := recall.NewChainSearchTool(store, nil, nil, cb.bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "ses-9")
			result, err := t.Execute(ctx, tool.Input{
				Name:      "chain_search",
				Arguments: map[string]any{"query": "needle"},
			})

			Expect(err).To(HaveOccurred())
			// Smallest scope: don't break the historical UX. The
			// engine's tool loop absorbs the err as a soft-fail and
			// still hands result.Output to the model. The new
			// signal is on the bus, not in a hard runtime failure.
			Expect(result.Output).To(ContainSubstring("fallback content"))
		})
	})

	Context("when the underlying chain store Search returns zero results without error", func() {
		It("does not publish recall.chain.search.failed (zero results is not a failure)", func() {
			// Regression guard for the central M9 distinction: an
			// empty corpus / no-match query must NOT trigger the
			// new failure signal. Only a true error path does.
			cb := newCaptureBus()
			store := &failingSearchChainStore{
				searchErr: nil,
				recent:    []provider.Message{{Role: "assistant", Content: "recent msg"}},
			}
			t := recall.NewChainSearchTool(store, nil, nil, cb.bus)

			_, err := t.Execute(context.Background(), tool.Input{
				Name:      "chain_search",
				Arguments: map[string]any{"query": "needle"},
			})

			Expect(err).NotTo(HaveOccurred(),
				"zero-result is the historical fallback path — must keep returning nil error")
			Expect(cb.snapshotFailed()).To(BeEmpty(),
				"recall.chain.search.failed must NOT fire on zero results; that's the whole point of the M9 split")
		})
	})

	Context("when no bus is wired", func() {
		It("does not panic and still surfaces the error from Execute", func() {
			// Defence-in-depth: ChainSearchTool tolerates a nil bus
			// (the factory wires one in production but unit tests
			// and headless integration tests pass nil). The error
			// surface MUST still fire — that's the engine-side
			// signal subscribers don't need a bus for.
			store := &failingSearchChainStore{
				searchErr: errors.New("qdrant unreachable"),
				recent:    []provider.Message{{Role: "assistant", Content: "recent"}},
			}
			t := recall.NewChainSearchTool(store, nil, nil, nil)

			_, err := t.Execute(context.Background(), tool.Input{
				Name:      "chain_search",
				Arguments: map[string]any{"query": "needle"},
			})

			Expect(err).To(HaveOccurred())
		})
	})
})
