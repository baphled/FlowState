package slashcommand_test

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat/slashcommand"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("Builtins", func() {
	var reg *slashcommand.Registry

	BeforeEach(func() {
		reg = slashcommand.NewRegistry()
		slashcommand.RegisterBuiltins(reg)
	})

	Describe("RegisterBuiltins", func() {
		It("registers every shipped command", func() {
			names := commandNames(reg)
			Expect(names).To(ContainElements(
				"clear", "help", "exit", "quit",
				"sessions", "plans", "agent", "model",
			))
		})
	})

	Describe("/clear", func() {
		It("invokes the message wiper", func() {
			cmd := lookup(reg, "clear")
			wiper := newWiper()

			cmd.Handler(slashcommand.CommandContext{MessageWiper: wiper}, nil)
			Expect(wiper.calls).To(Equal(1))
		})

		It("does not request a sub-picker", func() {
			cmd := lookup(reg, "clear")
			Expect(cmd.ItemsForPicker).To(BeNil())
		})
	})

	Describe("/help", func() {
		It("opens a sub-picker over every registered command", func() {
			cmd := lookup(reg, "help")
			items := cmd.ItemsForPicker(slashcommand.CommandContext{Registry: reg})
			Expect(len(items)).To(BeNumerically(">=", 7))
		})

		It("dumps help for the chosen command into the chat", func() {
			cmd := lookup(reg, "help")
			writer := newWriter()
			arg := widgets.Item{Value: *lookup(reg, "clear")}

			cmd.Handler(slashcommand.CommandContext{SystemMessageWriter: writer}, &arg)
			Expect(writer.lastMessage).To(ContainSubstring("/clear"))
			Expect(writer.lastMessage).To(ContainSubstring("Wipe the chat buffer"))
		})
	})

	Describe("/exit and /quit", func() {
		It("returns tea.Quit on /exit", func() {
			cmd := lookup(reg, "exit")
			Expect(cmd.Handler(slashcommand.CommandContext{}, nil)).NotTo(BeNil())
		})

		It("returns tea.Quit on /quit", func() {
			cmd := lookup(reg, "quit")
			Expect(cmd.Handler(slashcommand.CommandContext{}, nil)).NotTo(BeNil())
		})
	})

	Describe("/sessions", func() {
		It("opens a sub-picker over the listed sessions", func() {
			cmd := lookup(reg, "sessions")
			lister := &stubSessionLister{
				sessions: []contextpkg.SessionInfo{
					{ID: "abc12345", Title: "Chat one", MessageCount: 4},
					{ID: "def67890", Title: "", LastActive: time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)},
				},
			}
			items := cmd.ItemsForPicker(slashcommand.CommandContext{SessionLister: lister})
			Expect(items).To(HaveLen(2))
			Expect(items[0].Label).To(Equal("Chat one"))
		})

		It("resumes the chosen session", func() {
			cmd := lookup(reg, "sessions")
			resumer := &stubResumer{}
			arg := widgets.Item{Value: "abc12345"}

			cmd.Handler(slashcommand.CommandContext{SessionResumer: resumer}, &arg)
			Expect(resumer.resumed).To(Equal("abc12345"))
		})
	})

	Describe("/plans", func() {
		It("opens a sub-picker over plan summaries", func() {
			cmd := lookup(reg, "plans")
			lister := &stubPlanLister{
				summaries: []plan.Summary{
					{ID: "plan-1", Title: "First plan", Status: "draft"},
				},
			}
			items := cmd.ItemsForPicker(slashcommand.CommandContext{PlanLister: lister})
			Expect(items).To(HaveLen(1))
			Expect(items[0].Label).To(Equal("First plan"))
			Expect(items[0].Description).To(Equal("draft"))
		})

		It("dumps the selected plan into the chat", func() {
			cmd := lookup(reg, "plans")
			writer := newWriter()
			fetcher := &stubPlanFetcher{
				file: &plan.File{ID: "plan-1", Title: "First plan", Status: "draft", TLDR: "Body"},
			}
			arg := widgets.Item{Value: "plan-1"}

			cmd.Handler(slashcommand.CommandContext{
				SystemMessageWriter: writer,
				PlanFetcher:         fetcher,
			}, &arg)
			Expect(writer.lastMessage).To(ContainSubstring("First plan"))
			Expect(writer.lastMessage).To(ContainSubstring("Body"))
		})

		It("surfaces fetch errors as a system message", func() {
			cmd := lookup(reg, "plans")
			writer := newWriter()
			fetcher := &stubPlanFetcher{err: errors.New("boom")}
			arg := widgets.Item{Value: "plan-1"}

			cmd.Handler(slashcommand.CommandContext{
				SystemMessageWriter: writer,
				PlanFetcher:         fetcher,
			}, &arg)
			Expect(writer.lastMessage).To(ContainSubstring("Failed to load plan"))
		})
	})

	Describe("/agent", func() {
		It("opens a sub-picker over the agent registry", func() {
			cmd := lookup(reg, "agent")
			areg := newAgentRegistry()
			items := cmd.ItemsForPicker(slashcommand.CommandContext{AgentRegistry: areg})
			Expect(items).To(HaveLen(2))
			labels := []string{items[0].Label, items[1].Label}
			Expect(labels).To(ContainElements("Planner", "Executor"))
		})

		It("applies the selected manifest to the engine", func() {
			cmd := lookup(reg, "agent")
			areg := newAgentRegistry()
			switcher := &stubAgentSwitcher{}
			arg := widgets.Item{Value: "planner-id"}

			cmd.Handler(slashcommand.CommandContext{
				AgentRegistry: areg,
				AgentSwitcher: switcher,
			}, &arg)
			Expect(switcher.applied).NotTo(BeNil())
			Expect(switcher.applied.ID).To(Equal("planner-id"))
		})
	})

	Describe("/model", func() {
		It("opens a sub-picker over every provider's models", func() {
			cmd := lookup(reg, "model")
			lister := newProviderLister()
			items := cmd.ItemsForPicker(slashcommand.CommandContext{ProviderLister: lister})
			Expect(items).To(HaveLen(3))
			Expect(items[0].Label).To(Equal("openai/gpt-4o"))
		})

		It("applies the selected model preference", func() {
			cmd := lookup(reg, "model")
			switcher := &stubModelSwitcher{}
			arg := widgets.Item{Value: modelChoiceForTest("openai", "gpt-4o")}

			cmd.Handler(slashcommand.CommandContext{ModelSwitcher: switcher}, &arg)
			Expect(switcher.providerName).To(Equal("openai"))
			Expect(switcher.modelName).To(Equal("gpt-4o"))
		})
	})
})

type stubMessageWiper struct {
	calls int
}

func newWiper() *stubMessageWiper {
	return &stubMessageWiper{}
}

func (s *stubMessageWiper) ClearMessages() {
	s.calls++
}

type stubSystemWriter struct {
	lastMessage string
	calls       int
}

func newWriter() *stubSystemWriter {
	return &stubSystemWriter{}
}

func (s *stubSystemWriter) AddSystemMessage(content string) {
	s.lastMessage = content
	s.calls++
}

type stubResumer struct {
	resumed string
}

func (s *stubResumer) ResumeSession(id string) {
	s.resumed = id
}

type stubSessionLister struct {
	sessions []contextpkg.SessionInfo
}

func (s *stubSessionLister) List() []contextpkg.SessionInfo {
	return s.sessions
}

type stubPlanLister struct {
	summaries []plan.Summary
	err       error
}

func (s *stubPlanLister) List() ([]plan.Summary, error) {
	return s.summaries, s.err
}

type stubPlanFetcher struct {
	file *plan.File
	err  error
}

func (s *stubPlanFetcher) Get(_ string) (*plan.File, error) {
	return s.file, s.err
}

type stubAgentSwitcher struct {
	applied *agent.Manifest
}

func (s *stubAgentSwitcher) SetManifest(manifest agent.Manifest) {
	clone := manifest
	s.applied = &clone
}

type stubModelSwitcher struct {
	providerName string
	modelName    string
}

func (s *stubModelSwitcher) SetModelPreference(provider, model string) {
	s.providerName = provider
	s.modelName = model
}

type stubProviderLister struct {
	providers map[string]*stubProvider
	order     []string
}

func newProviderLister() *stubProviderLister {
	return &stubProviderLister{
		order: []string{"openai", "anthropic"},
		providers: map[string]*stubProvider{
			"openai": {
				name: "openai",
				models: []provider.Model{
					{ID: "gpt-4o", Provider: "openai", ContextLength: 128_000},
					{ID: "gpt-4-mini", Provider: "openai", ContextLength: 64_000},
				},
			},
			"anthropic": {
				name: "anthropic",
				models: []provider.Model{
					{ID: "claude-opus-4", Provider: "anthropic", ContextLength: 200_000},
				},
			},
		},
	}
}

func (s *stubProviderLister) List() []string {
	return s.order
}

func (s *stubProviderLister) Get(name string) (provider.Provider, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, errors.New("unknown provider")
	}
	return p, nil
}

type stubProvider struct {
	name   string
	models []provider.Model
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("not implemented")
}
func (s *stubProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("not implemented")
}
func (s *stubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errors.New("not implemented")
}
func (s *stubProvider) Models() ([]provider.Model, error) {
	return s.models, nil
}

func newAgentRegistry() *agent.Registry {
	reg := agent.NewRegistry()
	reg.Register(&agent.Manifest{ID: "planner-id", Name: "Planner"})
	reg.Register(&agent.Manifest{ID: "executor-id", Name: "Executor"})
	return reg
}

func lookup(reg *slashcommand.Registry, name string) *slashcommand.Command {
	cmd := reg.Lookup(name)
	Expect(cmd).NotTo(BeNil())
	return cmd
}

func commandNames(reg *slashcommand.Registry) []string {
	cmds := reg.All()
	out := make([]string, len(cmds))
	for idx, cmd := range cmds {
		out[idx] = cmd.Name
	}
	return out
}

// modelChoiceForTest mirrors the unexported modelChoice payload so tests
// can construct the value the /model handler expects without exporting
// the type.
func modelChoiceForTest(providerName, modelName string) any {
	return slashcommand.NewModelChoiceForTest(providerName, modelName)
}

// Ensure tea import remains used.
var _ tea.Cmd = nil
