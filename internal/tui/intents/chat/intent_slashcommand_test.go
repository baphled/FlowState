package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
})

func typeRune(intent *chat.Intent, r rune) {
	intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func seedAssistantMessage(intent *chat.Intent, content string) {
	intent.AppendAssistantMessageForTest(content)
}
