package tui_test

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui"
)

type mockProvider struct {
	name         string
	streamChunks []provider.StreamChunk
	streamErr    error
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan provider.StreamChunk, len(m.streamChunks))
	go func() {
		defer close(ch)
		for _, chunk := range m.streamChunks {
			ch <- chunk
		}
	}()
	return ch, nil
}

func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (m *mockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (m *mockProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

var _ = Describe("Chat TUI", func() {
	var (
		mockProv *mockProvider
		eng      *engine.Engine
		model    *tui.Model
	)

	BeforeEach(func() {
		mockProv = &mockProvider{
			name: "test-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Hello"},
				{Content: " from AI", Done: true},
			},
		}

		manifest := agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		eng = engine.New(engine.Config{
			ChatProvider: mockProv,
			Manifest:     manifest,
		})

		model = tui.NewModel(eng, "test-agent", "test-session")
	})

	Describe("NewModel", func() {
		It("creates model in normal mode", func() {
			Expect(model).NotTo(BeNil())
			Expect(model.Mode()).To(Equal("normal"))
		})

		It("initialises with empty input buffer", func() {
			Expect(model.Input()).To(BeEmpty())
		})

		It("initialises with streaming set to false", func() {
			Expect(model.IsStreaming()).To(BeFalse())
		})
	})

	Describe("Init", func() {
		It("returns nil cmd", func() {
			cmd := model.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("View", func() {
		It("contains mode indicator", func() {
			view := model.View()
			Expect(view).To(ContainSubstring("NORMAL"))
		})

		Context("when in insert mode", func() {
			BeforeEach(func() {
				model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			})

			It("shows insert mode indicator", func() {
				view := model.View()
				Expect(view).To(ContainSubstring("INSERT"))
			})
		})
	})

	Describe("Update", func() {
		Describe("mode switching", func() {
			It("switches to insert mode on 'i' key", func() {
				model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
				Expect(model.Mode()).To(Equal("insert"))
			})

			Context("when in insert mode", func() {
				BeforeEach(func() {
					model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
				})

				It("returns to normal mode on Escape", func() {
					model.Update(tea.KeyMsg{Type: tea.KeyEscape})
					Expect(model.Mode()).To(Equal("normal"))
				})

				It("appends runes to input buffer", func() {
					model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
					model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
					Expect(model.Input()).To(Equal("hi"))
				})

				It("handles backspace", func() {
					model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
					model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
					model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
					Expect(model.Input()).To(Equal("a"))
				})
			})
		})

		Describe("quit behaviour", func() {
			It("returns quit cmd on 'q' in normal mode", func() {
				_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
				Expect(cmd).NotTo(BeNil())
			})

			It("returns quit cmd on Ctrl+C", func() {
				_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
				Expect(cmd).NotTo(BeNil())
			})

			Context("when in insert mode", func() {
				BeforeEach(func() {
					model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
				})

				It("does not quit on 'q'", func() {
					_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
					Expect(cmd).To(BeNil())
					Expect(model.Input()).To(Equal("q"))
				})
			})
		})

		Describe("window sizing", func() {
			It("updates dimensions on WindowSizeMsg", func() {
				model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				Expect(model.Width()).To(Equal(120))
				Expect(model.Height()).To(Equal(40))
			})
		})
	})

	Describe("streaming messages", func() {
		It("appends content on ChunkMsg and returns cmd to fetch next", func() {
			chunks := make(chan provider.StreamChunk, 1)
			model.SetChunks(chunks)
			_, cmd := model.Update(tui.ChunkMsg{Content: "Hello"})
			Expect(model.ResponseContent()).To(Equal("Hello"))
			Expect(cmd).NotTo(BeNil())
		})

		It("accumulates multiple chunks", func() {
			chunks := make(chan provider.StreamChunk, 2)
			model.SetChunks(chunks)
			model.Update(tui.ChunkMsg{Content: "Hello"})
			model.Update(tui.ChunkMsg{Content: " World"})
			Expect(model.ResponseContent()).To(Equal("Hello World"))
		})

		It("marks streaming complete on StreamDoneMsg", func() {
			model.Update(tui.ChunkMsg{Content: "test"})
			model.Update(tui.StreamDoneMsg{})
			Expect(model.IsStreaming()).To(BeFalse())
		})

		It("adds completed response to messages on StreamDoneMsg", func() {
			model.Update(tui.ChunkMsg{Content: "AI response"})
			model.Update(tui.StreamDoneMsg{})
			Expect(model.Messages()).To(ContainElement(ContainSubstring("AI response")))
		})

		It("returns nil cmd on StreamDoneMsg", func() {
			model.Update(tui.ChunkMsg{Content: "test"})
			_, cmd := model.Update(tui.StreamDoneMsg{})
			Expect(cmd).To(BeNil())
		})
	})

	Describe("error handling", func() {
		It("stores error on ErrorMsg", func() {
			testErr := &strings.Reader{}
			model.Update(tui.ErrorMsg{Err: context.DeadlineExceeded})
			Expect(model.Error()).To(Equal(context.DeadlineExceeded))
			_ = testErr
		})
	})
})
