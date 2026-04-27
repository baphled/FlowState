package chat_test

import (
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("ChatIntent slash commands", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	Describe("registry wiring", func() {
		It("auto-registers the canonical slash commands", func() {
			reg := intent.SlashRegistryForTest()
			Expect(reg).NotTo(BeNil())
			Expect(reg.Lookup("clear")).NotTo(BeNil())
			Expect(reg.Lookup("help")).NotTo(BeNil())
		})
	})

	Describe("typing /", func() {
		It("opens the slash picker on the first / keystroke", func() {
			typeRune(intent, '/')
			Expect(intent.SlashPickerActiveForTest()).To(BeTrue())
		})

		It("filters the picker as the user types", func() {
			typeRune(intent, '/')
			typeRune(intent, 'c')
			typeRune(intent, 'l')
			typeRune(intent, 'e')
			Expect(intent.Input()).To(Equal("/cle"))
			Expect(intent.SlashPickerActiveForTest()).To(BeTrue())
		})

		It("dismisses the picker when Esc is pressed", func() {
			typeRune(intent, '/')
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(intent.SlashPickerActiveForTest()).To(BeFalse())
			Expect(intent.Input()).To(BeEmpty())
		})

		It("dismisses the picker when the user backspaces past /", func() {
			typeRune(intent, '/')
			intent.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			Expect(intent.SlashPickerActiveForTest()).To(BeFalse())
			Expect(intent.Input()).To(BeEmpty())
		})
	})

	Describe("/clear via the picker", func() {
		It("wipes the chat buffer when Enter is pressed", func() {
			seedAssistantMessage(intent, "first")
			seedAssistantMessage(intent, "second")
			Expect(intent.Messages()).To(HaveLen(2))

			typeRune(intent, '/')
			typeRune(intent, 'c')
			typeRune(intent, 'l')
			typeRune(intent, 'e')
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(intent.Messages()).To(BeEmpty())
			Expect(intent.SlashPickerActiveForTest()).To(BeFalse())
			Expect(intent.Input()).To(BeEmpty())
		})
	})

	Describe("/help via the picker", func() {
		It("opens the help sub-picker on Enter (no longer dispatches through the legacy handler)", func() {
			typeRune(intent, '/')
			typeRune(intent, 'h')
			typeRune(intent, 'e')
			typeRune(intent, 'l')
			typeRune(intent, 'p')
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(intent.SlashPickerActiveForTest()).To(BeTrue())
			Expect(intent.Input()).To(BeEmpty())
		})
	})

	Describe("/agent sub-picker", func() {
		BeforeEach(func() {
			seedAgentRegistry(intent)
		})

		It("opens the sub-picker on Enter", func() {
			openAgentSubPicker(intent)
			Expect(intent.SlashPickerActiveForTest()).To(BeTrue())
			Expect(intent.SubPickerVisibleLabelsForTest()).To(ContainElements("Planner", "Executor"))
		})

		It("filters the sub-picker as the user types", func() {
			openAgentSubPicker(intent)
			typeRune(intent, 'p')

			Expect(intent.SubPickerFilterForTest()).To(Equal("p"))
			Expect(intent.SubPickerVisibleLabelsForTest()).To(ConsistOf("Planner"))
		})

		It("backspaces the sub-picker filter without disturbing chat input", func() {
			openAgentSubPicker(intent)
			typeRune(intent, 'p')
			typeRune(intent, 'l')
			intent.Update(tea.KeyMsg{Type: tea.KeyBackspace})

			Expect(intent.SubPickerFilterForTest()).To(Equal("p"))
			Expect(intent.Input()).To(BeEmpty())
		})

		It("opens the same sub-picker via the /agents alias", func() {
			typeRune(intent, '/')
			for _, r := range "agents" {
				typeRune(intent, r)
			}
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(intent.SlashPickerActiveForTest()).To(BeTrue())
			Expect(intent.SubPickerVisibleLabelsForTest()).To(ContainElements("Planner", "Executor"))
		})

		It("dismisses the sub-picker on Esc", func() {
			openAgentSubPicker(intent)
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(intent.SlashPickerActiveForTest()).To(BeFalse())
		})
	})

	Describe("/swarm wizard", func() {
		var dir string

		BeforeEach(func() {
			dir = GinkgoT().TempDir()
			intent.SetSwarmsDirForTest(dir)
			seedAgentRegistry(intent)
		})

		It("opens the wizard when /swarm is selected", func() {
			openSwarmWizard(intent)
			Expect(intent.WizardActiveForTest()).To(BeTrue())
		})

		It("rolls back partially-written manifests on Esc", func() {
			openSwarmWizard(intent)
			typeWizardText(intent, "ws")
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			path := filepath.Join(dir, "ws.yml")
			_, err := os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())

			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(intent.WizardActiveForTest()).To(BeFalse())
		})
	})
})

func typeRune(intent *chat.Intent, r rune) {
	intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func seedAssistantMessage(intent *chat.Intent, content string) {
	intent.AppendAssistantMessageForTest(content)
}

func seedAgentRegistry(intent *chat.Intent) {
	reg := agent.NewRegistry()
	reg.Register(&agent.Manifest{ID: "planner", Name: "Planner", Mode: "plan"})
	reg.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
	intent.SetAgentRegistryForTest(reg)
}

func openAgentSubPicker(intent *chat.Intent) {
	typeRune(intent, '/')
	for _, r := range "agent" {
		typeRune(intent, r)
	}
	intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func openSwarmWizard(intent *chat.Intent) {
	typeRune(intent, '/')
	typeRune(intent, 's')
	typeRune(intent, 'w')
	typeRune(intent, 'a')
	typeRune(intent, 'r')
	typeRune(intent, 'm')
	intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func typeWizardText(intent *chat.Intent, value string) {
	for _, r := range value {
		typeRune(intent, r)
	}
}
