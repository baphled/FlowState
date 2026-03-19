package chat_test

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("ChatIntent", func() {
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

	Describe("NewIntent", func() {
		It("creates an intent with empty input", func() {
			Expect(intent.Input()).To(BeEmpty())
		})

		It("creates an intent with no messages", func() {
			Expect(intent.Messages()).To(BeEmpty())
		})

		It("creates an intent not streaming", func() {
			Expect(intent.IsStreaming()).To(BeFalse())
		})

		It("creates an intent with default dimensions", func() {
			Expect(intent.Width()).To(Equal(80))
			Expect(intent.Height()).To(Equal(24))
		})
	})

	Describe("Init", func() {
		It("returns a spinner tick command", func() {
			cmd := intent.Init()
			Expect(cmd).NotTo(BeNil())
		})
	})

	Describe("Update", func() {
		It("appends 'i' character to input", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			Expect(intent.Input()).To(Equal("i"))
		})

		It("returns quit command on Ctrl+C", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			Expect(cmd).NotTo(BeNil())
		})

		It("appends characters to input", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			Expect(intent.Input()).To(Equal("hi"))
		})

		It("handles backspace", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			intent.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			Expect(intent.Input()).To(Equal("h"))
		})

		It("does nothing on backspace with empty input", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			Expect(intent.Input()).To(BeEmpty())
		})

		It("does nothing on Enter with empty input", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).To(BeNil())
		})

		It("appends space character to input", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeySpace})
			Expect(intent.Input()).To(Equal(" "))
		})

		Context("window resize", func() {
			It("updates dimensions", func() {
				intent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				Expect(intent.Width()).To(Equal(120))
				Expect(intent.Height()).To(Equal(40))
			})
		})

		Context("spinner tick", func() {
			It("advances tickFrame on SpinnerTickMsg while streaming", func() {
				intent.SetStreamingForTest(true)
				before := intent.TickFrame()
				intent.Update(chat.SpinnerTickMsg{})
				Expect(intent.TickFrame()).To(Equal(before + 1))
			})

			It("does not advance tickFrame when not streaming", func() {
				before := intent.TickFrame()
				intent.Update(chat.SpinnerTickMsg{})
				Expect(intent.TickFrame()).To(Equal(before))
			})
		})

		Context("viewport scrolling", func() {
			BeforeEach(func() {
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			})

			It("scrolls viewport up on PageUp", func() {
				for range 20 {
					intent.Update(chat.StreamChunkMsg{Content: "line\n", Done: false})
				}
				intent.Update(chat.StreamChunkMsg{Content: "last", Done: true})
				intent.Update(tea.KeyMsg{Type: tea.KeyPgUp})
			})

			It("scrolls viewport down on PageDown", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyPgDown})
			})
		})
	})

	Describe("View", func() {
		It("shows input prompt in footer", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("> "))
		})

		It("shows current input in footer", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t', 'e', 's', 't'}})
			view := intent.View()
			Expect(view).To(ContainSubstring("test"))
		})

		It("shows spinner in StatusBar when streaming", func() {
			intent.SetStreamingForTest(true)
			view := intent.View()
			Expect(view).To(ContainSubstring("⠋"))
		})
	})

	Describe("token counting during streaming", func() {
		It("starts with zero token count", func() {
			Expect(intent.TokenCount()).To(Equal(0))
		})

		It("updates token count after stream chunks", func() {
			for range 5 {
				intent.Update(chat.StreamChunkMsg{Content: "hello world "})
			}
			Expect(intent.TokenCount()).To(BeNumerically(">", 0))
		})

		It("accumulates tokens across multiple chunks", func() {
			intent.Update(chat.StreamChunkMsg{Content: "hello world "})
			first := intent.TokenCount()
			intent.Update(chat.StreamChunkMsg{Content: "hello world "})
			second := intent.TokenCount()
			Expect(second).To(BeNumerically(">", first))
		})

		It("renders token count in View after streaming", func() {
			for range 5 {
				intent.Update(chat.StreamChunkMsg{Content: "hello world "})
			}
			view := intent.View()
			Expect(view).To(ContainSubstring(fmt.Sprintf("%d", intent.TokenCount())))
		})
	})

	Describe("StatusBar in View", func() {
		It("renders StatusBar at the bottom of View", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("openai"))
			Expect(view).To(ContainSubstring("gpt-4o"))
		})

		It("shows token budget in StatusBar", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("4096"))
		})
	})

	Describe("ToolPermissionMsg handling", func() {
		Context("when a ToolPermissionMsg is received", func() {
			It("shows permission prompt in the view", func() {
				responseChan := make(chan bool, 1)
				intent.Update(chat.ToolPermissionMsg{
					ToolName:  "dangerous_tool",
					Arguments: map[string]interface{}{"file": "/etc/passwd"},
					Response:  responseChan,
				})
				view := intent.View()
				Expect(view).To(ContainSubstring("PERMISSION"))
				Expect(view).To(ContainSubstring("dangerous_tool"))
				Expect(view).To(ContainSubstring("y/n"))
			})
		})

		Context("when user approves with 'y'", func() {
			It("sends true on response channel", func() {
				responseChan := make(chan bool, 1)
				intent.Update(chat.ToolPermissionMsg{
					ToolName:  "test_tool",
					Arguments: map[string]interface{}{},
					Response:  responseChan,
				})

				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

				Eventually(responseChan).Should(Receive(BeTrue()))
			})
		})

		Context("when user denies with 'n'", func() {
			It("sends false on response channel", func() {
				responseChan := make(chan bool, 1)
				intent.Update(chat.ToolPermissionMsg{
					ToolName:  "test_tool",
					Arguments: map[string]interface{}{},
					Response:  responseChan,
				})

				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

				Eventually(responseChan).Should(Receive(BeFalse()))
			})
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
