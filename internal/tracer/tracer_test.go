package tracer_test

import (
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tracer"
)

var _ = Describe("TracerHook", func() {
	var (
		ctx     context.Context
		request *provider.ChatRequest
	)

	BeforeEach(func() {
		ctx = context.Background()
		request = &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "Hello"},
			},
		}
	})

	Context("when the provider call succeeds", func() {
		It("passes all chunks through unchanged", func() {
			handler := func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 3)
				ch <- provider.StreamChunk{Content: "Hello "}
				ch <- provider.StreamChunk{Content: "world"}
				ch <- provider.StreamChunk{Content: "!", Done: true}
				close(ch)
				return ch, nil
			}

			chain := hook.NewChain(tracer.Hook())
			wrapped := chain.Execute(handler)

			resultChan, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range resultChan {
				chunks = append(chunks, chunk)
			}

			Expect(chunks).To(HaveLen(3))
			Expect(chunks[0].Content).To(Equal("Hello "))
			Expect(chunks[1].Content).To(Equal("world"))
			Expect(chunks[2].Content).To(Equal("!"))
			Expect(chunks[2].Done).To(BeTrue())
		})

		It("logs completion via slog", func() {
			var records []slog.Record
			logHandler := &capturingSlogHandler{records: &records}
			logger := slog.New(logHandler)
			slog.SetDefault(logger)
			DeferCleanup(func() {
				slog.SetDefault(slog.New(slog.NewTextHandler(GinkgoWriter, nil)))
			})

			handler := func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "ok", Done: true}
				close(ch)
				return ch, nil
			}

			chain := hook.NewChain(tracer.Hook())
			wrapped := chain.Execute(handler)

			resultChan, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			for v := range resultChan {
				_ = v
				_ = 0
			}

			Eventually(func() int { return len(*logHandler.records) }).Should(BeNumerically(">=", 1))

			found := false
			for _, r := range *logHandler.records {
				if r.Message == "tracer provider call complete" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})
	})

	Context("when the provider call fails", func() {
		It("returns the error unchanged", func() {
			expectedErr := errors.New("provider unavailable")
			handler := func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				return nil, expectedErr
			}

			chain := hook.NewChain(tracer.Hook())
			wrapped := chain.Execute(handler)

			_, err := wrapped(ctx, request)
			Expect(err).To(MatchError("provider unavailable"))
		})

		It("logs the error via slog", func() {
			var records []slog.Record
			logHandler := &capturingSlogHandler{records: &records}
			logger := slog.New(logHandler)
			slog.SetDefault(logger)
			DeferCleanup(func() {
				slog.SetDefault(slog.New(slog.NewTextHandler(GinkgoWriter, nil)))
			})

			handler := func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				return nil, errors.New("provider down")
			}

			chain := hook.NewChain(tracer.Hook())
			wrapped := chain.Execute(handler)

			_, err := wrapped(ctx, request)
			Expect(err).To(HaveOccurred())

			Eventually(func() int { return len(*logHandler.records) }).Should(BeNumerically(">=", 1))

			found := false
			for _, r := range *logHandler.records {
				if r.Message == "tracer provider call failed" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})
	})
})

var _ = Describe("TracingProvider", func() {
	var (
		ctx      context.Context
		inner    *mockProvider
		recorder *spyRecorder
		tp       *tracer.TracingProvider
	)

	BeforeEach(func() {
		ctx = context.Background()
		inner = &mockProvider{name: "test-provider"}
		recorder = &spyRecorder{}
		tp = tracer.NewTracingProvider(inner, recorder)
	})

	Describe("Name", func() {
		It("delegates to the inner provider", func() {
			Expect(tp.Name()).To(Equal("test-provider"))
		})
	})

	Describe("Stream", func() {
		It("delegates to the inner provider and records latency", func() {
			inner.streamFn = func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "streamed", Done: true}
				close(ch)
				return ch, nil
			}

			ch, err := tp.Stream(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("streamed"))

			Expect(recorder.latencyCalls).To(HaveLen(1))
			Expect(recorder.latencyCalls[0].provider).To(Equal("test-provider"))
			Expect(recorder.latencyCalls[0].method).To(Equal("stream"))
			Expect(recorder.latencyCalls[0].ms).To(BeNumerically(">=", 0))
		})

		It("returns errors unchanged", func() {
			inner.streamFn = func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				return nil, errors.New("stream failed")
			}

			_, err := tp.Stream(ctx, provider.ChatRequest{})
			Expect(err).To(MatchError("stream failed"))
			Expect(recorder.latencyCalls).To(HaveLen(1))
		})
	})

	Describe("Chat", func() {
		It("delegates to the inner provider and records latency", func() {
			inner.chatFn = func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
				return provider.ChatResponse{
					Message: provider.Message{Role: "assistant", Content: "hi"},
				}, nil
			}

			resp, err := tp.Chat(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Message.Content).To(Equal("hi"))

			Expect(recorder.latencyCalls).To(HaveLen(1))
			Expect(recorder.latencyCalls[0].method).To(Equal("chat"))
		})
	})

	Describe("Embed", func() {
		It("delegates to the inner provider and records latency", func() {
			inner.embedFn = func(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
				return []float64{0.1, 0.2, 0.3}, nil
			}

			v, err := tp.Embed(ctx, provider.EmbedRequest{Input: "test"})
			Expect(err).NotTo(HaveOccurred())
			Expect(v).To(Equal([]float64{0.1, 0.2, 0.3}))

			Expect(recorder.latencyCalls).To(HaveLen(1))
			Expect(recorder.latencyCalls[0].method).To(Equal("embed"))
		})
	})

	Describe("Models", func() {
		It("delegates to the inner provider", func() {
			inner.modelsFn = func() ([]provider.Model, error) {
				return []provider.Model{{ID: "model-1"}}, nil
			}

			models, err := tp.Models()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).To(HaveLen(1))
			Expect(models[0].ID).To(Equal("model-1"))

			Expect(recorder.latencyCalls).To(BeEmpty())
		})
	})
})

var _ = Describe("NoopRecorder", func() {
	It("accepts all method calls without panicking", func() {
		rec := &tracer.NoopRecorder{}
		Expect(func() { rec.RecordRetry("agent-1") }).NotTo(Panic())
		Expect(func() { rec.RecordValidationScore("agent-1", 0.95) }).NotTo(Panic())
		Expect(func() { rec.RecordCriticResult("agent-1", true) }).NotTo(Panic())
		Expect(func() { rec.RecordProviderLatency("ollama", "stream", 42.5) }).NotTo(Panic())
	})
})

var _ tracer.Recorder = (*tracer.NoopRecorder)(nil)

type capturingSlogHandler struct {
	records *[]slog.Record
}

func (h *capturingSlogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *capturingSlogHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r)
	return nil
}

func (h *capturingSlogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *capturingSlogHandler) WithGroup(_ string) slog.Handler { return h }

type mockProvider struct {
	name     string
	streamFn func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error)
	chatFn   func(context.Context, provider.ChatRequest) (provider.ChatResponse, error)
	embedFn  func(context.Context, provider.EmbedRequest) ([]float64, error)
	modelsFn func() ([]provider.Model, error)
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (m *mockProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, req)
	}
	return provider.ChatResponse{}, nil
}

func (m *mockProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, req)
	}
	return nil, nil
}

func (m *mockProvider) Models() ([]provider.Model, error) {
	if m.modelsFn != nil {
		return m.modelsFn()
	}
	return nil, nil
}

var _ provider.Provider = (*mockProvider)(nil)

type latencyCall struct {
	provider string
	method   string
	ms       float64
}

type spyRecorder struct {
	retryCalls       []string
	scoreCalls       []float64
	criticCalls      []bool
	latencyCalls     []latencyCall
	contextWindowObs []contextWindowCall
	tokensSavedCalls []tokensSavedCall
	overheadCalls    []tokensSavedCall
}

type contextWindowCall struct {
	agentID string
	tokens  int
}

type tokensSavedCall struct {
	agentID     string
	tokensSaved int
}

func (s *spyRecorder) RecordRetry(agentID string) {
	s.retryCalls = append(s.retryCalls, agentID)
}

func (s *spyRecorder) RecordValidationScore(_ string, score float64) {
	s.scoreCalls = append(s.scoreCalls, score)
}

func (s *spyRecorder) RecordCriticResult(_ string, passed bool) {
	s.criticCalls = append(s.criticCalls, passed)
}

func (s *spyRecorder) RecordProviderLatency(prov, method string, ms float64) {
	s.latencyCalls = append(s.latencyCalls, latencyCall{provider: prov, method: method, ms: ms})
}

func (s *spyRecorder) RecordContextWindowTokens(agentID string, tokens int) {
	s.contextWindowObs = append(s.contextWindowObs, contextWindowCall{agentID: agentID, tokens: tokens})
}

func (s *spyRecorder) RecordCompressionTokensSaved(agentID string, tokensSaved int) {
	s.tokensSavedCalls = append(s.tokensSavedCalls, tokensSavedCall{agentID: agentID, tokensSaved: tokensSaved})
}

func (s *spyRecorder) RecordCompressionOverheadTokens(agentID string, overheadTokens int) {
	s.overheadCalls = append(s.overheadCalls, tokensSavedCall{agentID: agentID, tokensSaved: overheadTokens})
}

var _ tracer.Recorder = (*spyRecorder)(nil)
