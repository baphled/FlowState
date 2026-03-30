package chat_test

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

func drainChunksViaNext(intent *chat.Intent, lastProcessed chat.StreamChunkMsg) {
	current := lastProcessed
	for range 50 {
		if current.Done || current.Next == nil {
			return
		}
		msg := current.Next()
		next, ok := msg.(chat.StreamChunkMsg)
		if !ok {
			return
		}
		intent.Update(next)
		current = next
	}
}

func drainChunksViaReadNext(intent *chat.Intent) {
	for range 50 {
		msg := intent.ReadNextChunkForTest()
		chunkMsg, ok := msg.(chat.StreamChunkMsg)
		if !ok {
			return
		}
		intent.Update(chunkMsg)
		if chunkMsg.Done {
			return
		}
	}
}

var _ = Describe("Chat Intent Integration", Label("integration"), func() {
	Describe("full streaming flow from engine to intent", func() {
		var (
			eng        *engine.Engine
			reg        *provider.Registry
			testIntent *chat.Intent
		)

		BeforeEach(func() {
			reg = provider.NewRegistry()
			reg.Register(&multiChunkProvider{
				providerName: "test-provider",
				chunks: []provider.StreamChunk{
					{Content: "Hello "},
					{Content: "beautiful "},
					{Content: "world"},
					{Done: true},
				},
			})

			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: integrationManifest("test-provider", "test-model"),
			})

			testIntent = chat.NewIntent(chat.IntentConfig{
				Engine:       eng,
				Streamer:     eng,
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
		})

		It("delivers the first chunk from engine through sendMessage", func() {
			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())

			firstMsg := cmd()
			chunk1, ok := firstMsg.(chat.StreamChunkMsg)
			Expect(ok).To(BeTrue())
			Expect(chunk1.Content).To(Equal("Hello "))
			Expect(chunk1.Done).To(BeFalse())
			Expect(testIntent.Response()).To(Equal(""))
		})

		It("accumulates content incrementally across chunks", func() {
			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())

			firstMsg := cmd()
			chunk1 := firstMsg.(chat.StreamChunkMsg)
			testIntent.Update(chunk1)
			Expect(testIntent.Response()).To(Equal("Hello "))

			secondMsg := chunk1.Next()
			chunk2 := secondMsg.(chat.StreamChunkMsg)
			testIntent.Update(chunk2)
			Expect(testIntent.Response()).To(Equal("Hello beautiful "))

			thirdMsg := chunk2.Next()
			chunk3 := thirdMsg.(chat.StreamChunkMsg)
			testIntent.Update(chunk3)
			Expect(testIntent.Response()).To(Equal("Hello beautiful world"))
		})

		It("finalizes accumulated content into messages on Done", func() {
			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			firstMsg := cmd()
			chunk := firstMsg.(chat.StreamChunkMsg)
			testIntent.Update(chunk)

			drainChunksViaNext(testIntent, chunk)

			messages := testIntent.Messages()
			Expect(messages).To(HaveLen(1))
			Expect(messages[0].Role).To(Equal("assistant"))
			Expect(messages[0].Content).To(Equal("Hello beautiful world"))
		})

		It("stops streaming after final chunk", func() {
			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			firstMsg := cmd()
			testIntent.Update(firstMsg)

			drainChunksViaReadNext(testIntent)

			Expect(testIntent.IsStreaming()).To(BeFalse())
			Expect(testIntent.Response()).To(BeEmpty())
		})
	})

	Describe("model selection followed by streaming", func() {
		var (
			eng        *engine.Engine
			reg        *provider.Registry
			testIntent *chat.Intent
		)

		BeforeEach(func() {
			reg = provider.NewRegistry()
			reg.Register(&multiChunkProvider{
				providerName: "provider-a",
				chunks: []provider.StreamChunk{
					{Content: "from A"},
					{Done: true},
				},
			})
			reg.Register(&multiChunkProvider{
				providerName: "provider-b",
				chunks: []provider.StreamChunk{
					{Content: "from B"},
					{Done: true},
				},
			})

			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: integrationManifest("provider-a", "model-a"),
			})

			testIntent = chat.NewIntent(chat.IntentConfig{
				App:          &integrationAppShell{registry: reg},
				Engine:       eng,
				Streamer:     eng,
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "provider-a",
				ModelName:    "model-a",
				TokenBudget:  4096,
			})
		})

		It("uses the newly selected model for subsequent streaming", func() {
			eng.SetModelPreference("provider-b", "model-b")

			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())

			firstMsg := cmd()
			chunk, ok := firstMsg.(chat.StreamChunkMsg)
			Expect(ok).To(BeTrue())
			Expect(chunk.Content).To(Equal("from B"))

			Expect(eng.LastProvider()).To(Equal("provider-b"))
			Expect(eng.LastModel()).To(Equal("model-b"))
		})

		It("completes streaming through updated provider after preference change", func() {
			eng.SetModelPreference("provider-b", "model-b")

			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			firstMsg := cmd()
			testIntent.Update(firstMsg)

			drainChunksViaReadNext(testIntent)

			messages := testIntent.Messages()
			Expect(messages).To(HaveLen(1))
			Expect(messages[0].Content).To(Equal("from B"))
		})
	})

	Describe("error propagation from engine to formatted message", func() {
		Context("when engine.Stream returns a synchronous error", func() {
			var testIntent *chat.Intent

			BeforeEach(func() {
				reg := provider.NewRegistry()
				reg.Register(&syncErrorProvider{
					providerName: "failing-provider",
				})

				eng := engine.New(engine.Config{
					Registry: reg,
					Manifest: integrationManifest("failing-provider", "bad-model"),
				})

				testIntent = chat.NewIntent(chat.IntentConfig{
					Engine:       eng,
					Streamer:     eng,
					AgentID:      "test-agent",
					SessionID:    "test-session",
					ProviderName: "failing-provider",
					ModelName:    "bad-model",
					TokenBudget:  4096,
				})
			})

			It("displays a formatted error message in chat messages", func() {
				testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
				cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(cmd).NotTo(BeNil())

				errMsg := cmd()
				chunkMsg, ok := errMsg.(chat.StreamChunkMsg)
				Expect(ok).To(BeTrue())
				Expect(chunkMsg.Error).To(HaveOccurred())
				Expect(chunkMsg.Done).To(BeTrue())

				testIntent.Update(errMsg)

				messages := testIntent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Role).To(Equal("assistant"))
				Expect(messages[0].Content).To(ContainSubstring("Error"))
			})
		})

		Context("when provider sends async error chunk after partial content", func() {
			var testIntent *chat.Intent

			BeforeEach(func() {
				reg := provider.NewRegistry()
				reg.Register(&multiChunkProvider{
					providerName: "partial-provider",
					chunks: []provider.StreamChunk{
						{Content: "Partial response "},
						{Error: errors.New("connection lost"), Done: true},
					},
				})

				eng := engine.New(engine.Config{
					Registry: reg,
					Manifest: integrationManifest("partial-provider", "some-model"),
				})

				testIntent = chat.NewIntent(chat.IntentConfig{
					Engine:       eng,
					Streamer:     eng,
					AgentID:      "test-agent",
					SessionID:    "test-session",
					ProviderName: "partial-provider",
					ModelName:    "some-model",
					TokenBudget:  4096,
				})
			})

			It("preserves partial content and appends formatted error", func() {
				testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
				cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				firstMsg := cmd()
				testIntent.Update(firstMsg)

				drainChunksViaReadNext(testIntent)

				messages := testIntent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Content).To(ContainSubstring("Partial response"))
				Expect(messages[0].Content).To(ContainSubstring("Error"))
				Expect(messages[0].Content).To(ContainSubstring("connection lost"))
			})
		})

		Context("when error is an HTTP error with structured fields", func() {
			var testIntent *chat.Intent

			BeforeEach(func() {
				httpErr := fmt.Errorf(
					`POST "https://api.anthropic.com/v1/messages": 404 Not Found {"type":"error","error":{"type":"not_found_error","message":"model: claude-unknown is not found"}}`,
				)
				reg := provider.NewRegistry()
				reg.Register(&multiChunkProvider{
					providerName: "anthropic",
					chunks: []provider.StreamChunk{
						{Content: ""},
						{Error: httpErr, Done: true},
					},
				})

				eng := engine.New(engine.Config{
					Registry: reg,
					Manifest: integrationManifest("anthropic", "claude-unknown"),
				})

				testIntent = chat.NewIntent(chat.IntentConfig{
					Engine:       eng,
					Streamer:     eng,
					AgentID:      "test-agent",
					SessionID:    "test-session",
					ProviderName: "anthropic",
					ModelName:    "claude-unknown",
					TokenBudget:  4096,
				})
			})

			It("displays structured error with provider and status code", func() {
				testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
				cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				firstMsg := cmd()
				testIntent.Update(firstMsg)

				drainChunksViaReadNext(testIntent)

				messages := testIntent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Content).To(ContainSubstring("API Error"))
				Expect(messages[0].Content).To(ContainSubstring("404"))
				Expect(messages[0].Content).To(ContainSubstring("anthropic"))
			})
		})
	})

	Describe("tick commands during streaming", func() {
		var testIntent *chat.Intent

		BeforeEach(func() {
			reg := provider.NewRegistry()
			reg.Register(&multiChunkProvider{
				providerName: "test-provider",
				chunks: []provider.StreamChunk{
					{Content: "chunk1 "},
					{Content: "chunk2 "},
					{Done: true},
				},
			})

			eng := engine.New(engine.Config{
				Registry: reg,
				Manifest: integrationManifest("test-provider", "test-model"),
			})

			testIntent = chat.NewIntent(chat.IntentConfig{
				Engine:       eng,
				Streamer:     eng,
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
		})

		It("returns a batch command containing both chunk reader and tick on non-Done chunk", func() {
			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())

			firstMsg := cmd()
			batchCmd := testIntent.Update(firstMsg)
			Expect(batchCmd).NotTo(BeNil())

			batchResult := batchCmd()
			batchMsg, ok := batchResult.(tea.BatchMsg)
			Expect(ok).To(BeTrue())
			Expect(batchMsg).To(HaveLen(2))
		})

		It("advances tick frame when SpinnerTickMsg is processed during streaming", func() {
			testIntent.SetStreamingForTest(true)
			before := testIntent.TickFrame()

			testIntent.Update(chat.SpinnerTickMsg{})
			Expect(testIntent.TickFrame()).To(Equal(before + 1))

			testIntent.Update(chat.SpinnerTickMsg{})
			Expect(testIntent.TickFrame()).To(Equal(before + 2))
		})

		It("processes SpinnerTickMsg between chunk arrivals", func() {
			testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			cmd := testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())

			firstMsg := cmd()
			chunkMsg, ok := firstMsg.(chat.StreamChunkMsg)
			Expect(ok).To(BeTrue())
			Expect(chunkMsg.Done).To(BeFalse())

			testIntent.Update(firstMsg)
			Expect(testIntent.IsStreaming()).To(BeTrue())

			tickBefore := testIntent.TickFrame()
			testIntent.Update(chat.SpinnerTickMsg{})
			Expect(testIntent.TickFrame()).To(Equal(tickBefore + 1))
		})
	})
})

type multiChunkProvider struct {
	providerName string
	chunks       []provider.StreamChunk
}

func (p *multiChunkProvider) Name() string { return p.providerName }

func (p *multiChunkProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "test-model", Provider: p.providerName}}, nil
}

func (p *multiChunkProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *multiChunkProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(p.chunks))
	for _, chunk := range p.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (p *multiChunkProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

type syncErrorProvider struct {
	providerName string
}

func (p *syncErrorProvider) Name() string { return p.providerName }

func (p *syncErrorProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "bad-model", Provider: p.providerName}}, nil
}

func (p *syncErrorProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("sync error")
}

func (p *syncErrorProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("provider connection refused")
}

func (p *syncErrorProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

type integrationAppShell struct {
	registry *provider.Registry
}

func (s *integrationAppShell) WriteConfig(_ *config.AppConfig) error { return nil }
func (s *integrationAppShell) List() []string                        { return s.registry.List() }

func (s *integrationAppShell) Get(name string) (provider.Provider, error) {
	return s.registry.Get(name)
}

func integrationManifest(providerName, model string) agent.Manifest {
	return agent.Manifest{
		ID:         "test-agent",
		Name:       "Test Agent",
		Complexity: "standard",
		ModelPreferences: map[string][]agent.ModelPref{
			"standard": {
				{Provider: providerName, Model: model},
			},
		},
	}
}
