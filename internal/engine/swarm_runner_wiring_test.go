package engine_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// retryStreamerWith returns a streaming.Streamer that fails the first
// failures attempts with err and then drains a single Done chunk.
func retryStreamerWith(failures int, err error, calls *atomic.Int32) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		n := calls.Add(1)
		if int(n) <= failures {
			return nil, err
		}
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// panicStreamer returns a streaming.Streamer that panics inside Stream.
// Used to assert the runner maps panics to CategoryTerminal.
func panicStreamer(calls *atomic.Int32) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		calls.Add(1)
		panic("streamer boom")
	})
}

// streamerFunc adapts a func to streaming.Streamer for tests.
type streamerFunc func(ctx context.Context, agentID string, msg string) (<-chan provider.StreamChunk, error)

func (s streamerFunc) Stream(ctx context.Context, agentID string, msg string) (<-chan provider.StreamChunk, error) {
	return s(ctx, agentID, msg)
}

// retryableSwarmErr returns a CategorisedError tagged retryable so the
// swarm runner's retry policy fires.
func retryableSwarmErr() error {
	return &swarm.CategorisedError{Category: swarm.CategoryRetryable, Cause: errors.New("transient")}
}

// terminalSwarmErr returns a CategorisedError tagged terminal so the
// swarm runner short-circuits without retrying.
func terminalSwarmErr() error {
	return &swarm.CategorisedError{Category: swarm.CategoryTerminal, Cause: errors.New("permanent")}
}

// noJitterRetryManifest builds a swarm manifest pinned to fast retries
// so tests don't pay wall-clock backoff.
func noJitterRetryManifest(id string) *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            id,
		Lead:          "lead",
		Members:       []string{"qa-agent"},
		SwarmType:     swarm.SwarmTypeAnalysis,
		Retry: &swarm.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 1 * time.Millisecond,
			MaxBackoff:     1 * time.Millisecond,
			Multiplier:     1.0,
			Jitter:         false,
		},
	}
}

// installSwarmCtxOnEngine wires a swarm.Context into the engine and a
// matching manifest into a registry the DelegateTool can look up.
func installSwarmCtxOnEngine(eng *engine.Engine, m *swarm.Manifest) *swarm.Registry {
	reg := swarm.NewRegistry()
	reg.Register(m)
	swarmCtx := swarm.NewContext(m.ID, m)
	eng.SetSwarmContext(&swarmCtx)
	return reg
}

// buildLeadAndTargetEngines constructs a minimal lead + target engine
// pair the DelegateTool can route between.
func buildLeadAndTargetEngines() (*engine.Engine, *engine.Engine, map[string]*engine.Engine) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	target := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "qa"},
		Manifest: agent.Manifest{
			ID:                "qa-agent",
			Name:              "QA",
			Instructions:      agent.Instructions{SystemPrompt: "qa"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{"lead": lead, "qa-agent": target}
	return lead, target, engines
}

var _ = Describe("SwarmRunnerWiring", func() {
	Context("when the streamer returns a CategoryRetryable error", func() {
		It("retries up to the manifest's MaxAttempts and succeeds", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("retry-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(2, retryableSwarmErr(), calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)

			Expect(err).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(3)))
		})
	})

	Context("when the streamer returns a CategoryTerminal error", func() {
		It("short-circuits at the first attempt and surfaces *swarm.CategorisedError", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("terminal-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(99, terminalSwarmErr(), calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)

			Expect(err).To(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
			var ce *swarm.CategorisedError
			Expect(errors.As(err, &ce)).To(BeTrue())
			Expect(ce.Category).To(Equal(swarm.CategoryTerminal))
		})
	})

	Context("when the streamer panics", func() {
		It("maps the panic to CategoryTerminal without retrying", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("panic-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": panicStreamer(calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)

			Expect(err).To(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
			var ce *swarm.CategorisedError
			Expect(errors.As(err, &ce)).To(BeTrue())
			Expect(ce.Category).To(Equal(swarm.CategoryTerminal))
		})
	})

	Context("when the same swarm context drives multiple delegations", func() {
		It("reuses a single Runner so breaker state accumulates across calls", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("cache-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(0, nil, calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err1 := delegateTool.Execute(context.Background(), input)
			_, err2 := delegateTool.Execute(context.Background(), input)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(delegateTool.RunnerForSwarmIDForTest("cache-swarm")).
				To(BeIdenticalTo(delegateTool.RunnerForSwarmIDForTest("cache-swarm")))
		})
	})
})
