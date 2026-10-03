package engine_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// streakPrimaryProvider always answers tool_use with zero tool calls.
type streakPrimaryProvider struct {
	name  string
	calls int
}

func (p *streakPrimaryProvider) Name() string { return p.name }

func (p *streakPrimaryProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.calls++
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "Let me check.", Done: true, StopReason: "tool_use"}
	}()
	return ch, nil
}

func (p *streakPrimaryProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *streakPrimaryProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

func (p *streakPrimaryProvider) Models() ([]provider.Model, error) { return nil, nil }

// streakFallbackProvider answers a clean end_turn.
type streakFallbackProvider struct {
	name  string
	calls int
}

func (p *streakFallbackProvider) Name() string { return p.name }

func (p *streakFallbackProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.calls++
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "Recovered.", Done: true, StopReason: "end_turn"}
	}()
	return ch, nil
}

func (p *streakFallbackProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *streakFallbackProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

func (p *streakFallbackProvider) Models() ([]provider.Model, error) { return nil, nil }

// newStreakProviderRegistry registers both streak providers so the
// engine can route the failover retry to the next candidate.
func newStreakProviderRegistry(primary, fallback provider.Provider) *provider.Registry {
	registry := provider.NewRegistry()
	registry.Register(primary)
	registry.Register(fallback)
	return registry
}

// newStreakFailoverManager wires a two-candidate failover chain whose head
// is the pathological primary and whose tail is the healthy fallback.
func newStreakFailoverManager(primary, fallback provider.Provider) *failover.Manager {
	registry := newStreakProviderRegistry(primary, fallback)
	fm := failover.NewManager(registry, failover.NewHealthManager(), time.Minute)
	fm.SetBasePreferences([]provider.ModelPreference{
		{Provider: primary.Name(), Model: "primary-model"},
		{Provider: fallback.Name(), Model: "fallback-model"},
	})
	return fm
}

var _ = Describe("ToolUseNoCalls streak-thresholded failover", func() {
	var (
		manifest agent.Manifest
		testTool *executableMockTool
	)

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:                "test-agent",
			Name:              "Test Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are a helpful assistant."},
			ContextManagement: agent.DefaultContextManagement(),
			Capabilities:      agent.Capabilities{Tools: []string{"test_tool"}},
		}

		testTool = &executableMockTool{
			name:        "test_tool",
			description: "A test tool",
			execResult:  tool.Result{Output: "ok"},
		}
	})

	It("fails over to the next candidate after a streak of two tool_use_no_calls rounds", func() {
		primary := &streakPrimaryProvider{name: "streak-primary"}
		fallback := &streakFallbackProvider{name: "streak-fallback"}

		eng := engine.New(engine.Config{
			ChatProvider:    primary,
			FailoverManager: newStreakFailoverManager(primary, fallback),
			Registry:        newStreakProviderRegistry(primary, fallback),
			EventBus:        eventbus.NewEventBus(),
			Manifest:        manifest,
			Tools:           []tool.Tool{testTool},
		})

		chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
		Expect(err).NotTo(HaveOccurred())

		var content string
		for chunk := range chunks {
			content += chunk.Content
		}

		Expect(content).To(ContainSubstring("Recovered."),
			"the fallback provider must complete the turn after the primary's streak")
		Expect(fallback.calls).To(BeNumerically(">=", 1),
			"failover must route at least one request to the next candidate")
		Expect(primary.calls).To(BeNumerically("<=", 2),
			"the primary must not be retried in-session beyond the streak threshold of two")
	})

	It("keeps a single tool_use_no_calls round on the same provider", func() {
		primaryOnce := &streamSequenceProvider{
			name: "streak-primary",
			sequences: [][]provider.StreamChunk{
				{{Content: "checking", Done: true, StopReason: "tool_use"}},
				{{Content: "Fine.", Done: true, StopReason: "end_turn"}},
			},
		}
		fallback := &streakFallbackProvider{name: "streak-fallback"}

		eng := engine.New(engine.Config{
			ChatProvider:    primaryOnce,
			FailoverManager: newStreakFailoverManager(primaryOnce, fallback),
			EventBus:        eventbus.NewEventBus(),
			Manifest:        manifest,
			Tools:           []tool.Tool{testTool},
		})

		chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
		Expect(err).NotTo(HaveOccurred())
		var content string
		for chunk := range chunks {
			content += chunk.Content
		}
		Expect(content).To(ContainSubstring("Fine."),
			"streak of one is a tolerated false positive — the same provider's retry succeeds")
		Expect(fallback.calls).To(BeZero(),
			"a single tool_use_no_calls must not trip failover")
	})
})
