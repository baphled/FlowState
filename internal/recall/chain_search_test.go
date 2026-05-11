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

// captureBus subscribes to the success recall chain search event and
// installs a wildcard recorder that catches every published topic so a
// single spec can assert what fired AND assert that the F4-removed
// `recall.chain.search.failed` topic stays unpublished.
type captureBus struct {
	bus      *eventbus.EventBus
	mu       sync.Mutex
	searched []events.RecallChainSearchEventData
	failed   []string // captured topic strings for the (removed) failure event — must stay empty (F4)
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
	// F4: subscribe to the legacy topic string directly so the
	// regression assertion survives the removal of the typed event
	// (RecallChainSearchFailedEvent + EventRecallChainSearchFailed
	// constant are gone — but we can still observe a raw publish to
	// "recall.chain.search.failed" if anything re-adds it).
	c.bus.Subscribe("recall.chain.search.failed", func(_ any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.failed = append(c.failed, "recall.chain.search.failed")
	})
	return c
}

func (c *captureBus) snapshotFailed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.failed))
	copy(out, c.failed)
	return out
}

var _ = Describe("ChainSearchTool — M9 failure discrimination", func() {
	// M9 (Bug Hunt Findings May 2026). Pre-M9, `ChainSearchTool.Execute`
	// fell back to recency on `err != nil || len(results) == 0` —
	// genuine Qdrant / embedding / network failures were
	// indistinguishable from zero-result queries. A long thread
	// could silently degrade to recency-only retrieval with no
	// observable signal.
	//
	// F4 (Bug Hunt Findings May 11 2026) follow-up: the M9 fix
	// shipped a dedicated `recall.chain.search.failed` bus event
	// alongside the typed error return. The event had ZERO
	// non-test subscribers — published-but-unsubscribed observability
	// is dead surface area. F4 removes the publish; the typed
	// `ErrAllSourcesFailed` sentinel + the engine's existing
	// `tool.execute.error` propagation (via `result.Error = err`)
	// remain the canonical failure signals. The success-side
	// `recall.chain.searched` event is unchanged.

	Context("when the underlying chain store Search call returns an error", func() {
		It("returns the error from Execute so the engine's tool.execute.error path fires", func() {
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
			Expect(err.Error()).To(ContainSubstring("connection refused"),
				"the underlying error message must remain attached so the engine's tool.execute.error payload carries it")
		})

		It("does not publish a dead recall.chain.search.failed bus event (F4)", func() {
			// Regression guard: the M9 implementation published a
			// dedicated bus event with zero non-test subscribers in
			// the entire tree. The canonical failure signal is the
			// returned error (engine maps it onto tool.execute.error
			// via result.Error = err at engine.go:3825). Re-adding
			// the publish would re-introduce dead surface area.
			cb := newCaptureBus()
			store := &failingSearchChainStore{
				searchErr: errors.New("qdrant unreachable"),
				recent:    []provider.Message{{Role: "assistant", Content: "recent"}},
			}
			t := recall.NewChainSearchTool(store, nil, nil, cb.bus)

			_, err := t.Execute(context.Background(), tool.Input{
				Name:      "chain_search",
				Arguments: map[string]any{"query": "needle"},
			})

			Expect(err).To(HaveOccurred())
			Expect(cb.snapshotFailed()).To(BeEmpty(),
				"F4: recall.chain.search.failed must not be published — typed error + tool.execute.error are the canonical failure signals")
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
			// still hands result.Output to the model. The signal is
			// in the returned error; the recency content keeps the
			// model from being left empty-handed.
			Expect(result.Output).To(ContainSubstring("fallback content"))
		})
	})

	Context("when the underlying chain store Search returns zero results without error", func() {
		It("does not return an error (zero results is not a failure)", func() {
			// Regression guard for the central M9 distinction: an
			// empty corpus / no-match query must NOT surface as an
			// error. Only a true error path does.
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
				"the failure topic must not fire on zero results either")
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
