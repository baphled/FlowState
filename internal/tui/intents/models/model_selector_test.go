package models_test

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/models"
)

var errProviderNotFound = errors.New("provider not found")

var _ = Describe("ModelSelectorIntent", func() {
	var (
		mockRegistry *MockProviderRegistry
		intent       *models.Intent
	)

	BeforeEach(func() {
		mockRegistry = NewMockProviderRegistry()
		intent = models.NewIntent(models.IntentConfig{
			ProviderRegistry: mockRegistry,
			OnSelect:         nil,
		})
	})

	Describe("NewIntent", func() {
		It("creates an intent with groups loaded from registry", func() {
			Expect(intent).NotTo(BeNil())
			groups := intent.Groups()
			Expect(groups).NotTo(BeEmpty())
		})

		It("creates an intent with no group expanded by default", func() {
			Expect(intent.IsExpanded()).To(BeFalse())
		})

		It("creates an intent with no selection by default", func() {
			Expect(intent.SelectedGroup()).To(Equal(0))
			Expect(intent.SelectedModel()).To(Equal(0))
		})
	})

	Describe("View", func() {
		It("shows Select Model title", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("Select Model"))
		})

		It("shows navigation help text", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("navigate"))
			Expect(view).To(ContainSubstring("select"))
			Expect(view).To(ContainSubstring("add provider"))
			Expect(view).To(ContainSubstring("cancel"))
		})

		It("shows collapsed groups with indicator", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("▶"))
		})

		It("shows provider names", func() {
			view := intent.View()
			groups := intent.Groups()
			for _, g := range groups {
				Expect(view).To(ContainSubstring(g.ProviderName))
			}
		})
	})

	Describe("navigation", func() {
		Context("when a group is expanded", func() {
			BeforeEach(func() {
				intent.Expand()
			})

			It("shows expanded indicator", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("▼"))
			})

			It("shows model names under expanded group", func() {
				view := intent.View()
				groups := intent.Groups()
				for _, m := range groups[0].Models {
					Expect(view).To(ContainSubstring(m.ID))
				}
			})
		})

		Context("Down arrow navigation", func() {
			BeforeEach(func() {
				intent.Expand()
			})

			It("moves selection down within expanded group", func() {
				initial := intent.SelectedModel()
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedModel()).To(Equal(initial + 1))
			})

			It("does not move beyond last model", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedModel()).To(Equal(2))
			})
		})

		Context("Up arrow navigation", func() {
			BeforeEach(func() {
				intent.Expand()
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			})

			It("moves selection up within expanded group", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedModel()).To(Equal(1))
			})

			It("does not move above first model", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedModel()).To(Equal(0))
			})
		})

		Context("when groups are collapsed", func() {
			It("moves to next group on Down arrow", func() {
				Expect(intent.IsExpanded()).To(BeFalse())
				Expect(intent.SelectedGroup()).To(Equal(0))
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedGroup()).To(Equal(1))
			})

			It("moves to previous group on Up arrow", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedGroup()).To(Equal(1))
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedGroup()).To(Equal(0))
			})

			It("does not move past last group on Down arrow", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedGroup()).To(Equal(1))
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedGroup()).To(Equal(1))
			})

			It("does not move before first group on Up arrow", func() {
				Expect(intent.SelectedGroup()).To(Equal(0))
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedGroup()).To(Equal(0))
			})
		})
	})

	Describe("selecting from non-default provider", func() {
		var selectedProvider, selectedModel string

		BeforeEach(func() {
			selectedProvider = ""
			selectedModel = ""
			intent = models.NewIntent(models.IntentConfig{
				ProviderRegistry: mockRegistry,
				OnSelect: func(p, m string) {
					selectedProvider = p
					selectedModel = m
				},
			})
		})

		It("navigating to second provider and expanding shows its models", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			Expect(intent.SelectedGroup()).To(Equal(1))
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(intent.IsExpanded()).To(BeTrue())
			view := intent.View()
			Expect(view).To(ContainSubstring("gpt-4o"))
			Expect(view).To(ContainSubstring("gpt-4o-mini"))
		})

		It("selecting model from second provider calls OnSelect with correct provider and model", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(selectedProvider).To(Equal("openai"))
			Expect(selectedModel).To(Equal("gpt-4o"))
		})
	})

	Describe("group expansion", func() {
		It("expands group on Enter", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(intent.IsExpanded()).To(BeTrue())
		})

		It("collapses group when navigating past last model and pressing Enter", func() {
			intent.Expand()
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(intent.IsExpanded()).To(BeFalse())
		})
	})

	Describe("model selection", func() {
		var selectedProvider, selectedModel string

		BeforeEach(func() {
			selectedProvider = ""
			selectedModel = ""
			intent = models.NewIntent(models.IntentConfig{
				ProviderRegistry: mockRegistry,
				OnSelect: func(p, m string) {
					selectedProvider = p
					selectedModel = m
				},
			})
			intent.Expand()
		})

		It("calls OnSelect with provider and model on Enter", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(selectedProvider).To(Equal("ollama"))
			Expect(selectedModel).To(Equal("llama3.2"))
		})
	})

	Describe("Escape key", func() {
		It("returns without calling OnSelect", func() {
			var called bool
			mockRegistry.OnSelect = func(p, m string) {
				called = true
			}
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(called).To(BeFalse())
		})
	})
	Describe("'a' key for provider setup", func() {
		It("emits ShowModalMsg to display provider setup", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
			var msg tea.Msg
			if cmd != nil {
				msg = cmd()
			}
			_, ok := msg.(tuiintents.ShowModalMsg)
			Expect(ok).To(BeTrue())
		})
	})

	Describe("WindowSizeMsg", func() {
		It("updates dimensions", func() {
			intent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			Expect(intent.Width()).To(Equal(120))
			Expect(intent.Height()).To(Equal(40))
		})
	})

	Describe("Intent interface compliance", func() {
		It("satisfies app.Intent interface", func() {
			var _ interface {
				Init() tea.Cmd
				Update(tea.Msg) tea.Cmd
				View() string
			} = intent
		})
	})
})

type MockProviderRegistry struct {
	OnSelect func(p, m string)
}

func NewMockProviderRegistry() *MockProviderRegistry {
	return &MockProviderRegistry{}
}

func (r *MockProviderRegistry) List() []string {
	return []string{"ollama", "openai"}
}

func (r *MockProviderRegistry) Get(name string) (provider.Provider, error) {
	switch name {
	case "ollama":
		return &mockProvider{name: "ollama", models: []provider.Model{
			{ID: "llama3.2", Provider: "ollama", ContextLength: 8192},
			{ID: "mistral", Provider: "ollama", ContextLength: 8192},
		}}, nil
	case "openai":
		return &mockProvider{name: "openai", models: []provider.Model{
			{ID: "gpt-4o", Provider: "openai", ContextLength: 128000},
			{ID: "gpt-4o-mini", Provider: "openai", ContextLength: 128000},
		}}, nil
	default:
		return nil, errProviderNotFound
	}
}

type mockProvider struct {
	name   string
	models []provider.Model
}

func (p *mockProvider) Name() string { return p.name }

func (p *mockProvider) Models() ([]provider.Model, error) {
	return p.models, nil
}

func (p *mockProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *mockProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	go func() { close(ch) }()
	return ch, nil
}

func (p *mockProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
