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
		It("creates an intent in normal mode", func() {
			Expect(intent.Mode()).To(Equal("normal"))
		})

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
		It("returns nil command", func() {
			cmd := intent.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("Update", func() {
		Context("in normal mode", func() {
			It("switches to insert mode on 'i' key", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
				Expect(intent.Mode()).To(Equal("insert"))
			})

			It("returns quit command on 'q' key", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
				Expect(cmd).NotTo(BeNil())
			})

			It("returns quit command on Ctrl+C", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
				Expect(cmd).NotTo(BeNil())
			})

			It("ignores other keys", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
				Expect(intent.Mode()).To(Equal("normal"))
			})
		})

		Context("in insert mode", func() {
			BeforeEach(func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
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

			It("switches to normal mode on Escape", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyEscape})
				Expect(intent.Mode()).To(Equal("normal"))
			})

			It("does nothing on Enter with empty input", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(cmd).To(BeNil())
			})
		})

		Context("window resize", func() {
			It("updates dimensions", func() {
				intent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				Expect(intent.Width()).To(Equal(120))
				Expect(intent.Height()).To(Equal(40))
			})
		})
	})

	Describe("View", func() {
		It("shows normal mode indicator", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("[NORMAL]"))
		})

		It("shows insert mode indicator when in insert mode", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			view := intent.View()
			Expect(view).To(ContainSubstring("[INSERT]"))
		})

		It("shows input prompt", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("> "))
		})

		It("shows current input", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t', 'e', 's', 't'}})
			view := intent.View()
			Expect(view).To(ContainSubstring("test"))
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

		It("shows NORMAL mode in StatusBar", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("NORMAL"))
		})

		It("shows INSERT mode after switching", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			view := intent.View()
			Expect(view).To(ContainSubstring("INSERT"))
		})

		It("shows token budget in StatusBar", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("4096"))
		})
	})

	Describe("ToolPermissionMsg handling", func() {
		Context("when a ToolPermissionMsg is received", func() {
			It("switches to permission mode", func() {
				responseChan := make(chan bool, 1)
				intent.Update(chat.ToolPermissionMsg{
					ToolName:  "dangerous_tool",
					Arguments: map[string]interface{}{"file": "/etc/passwd"},
					Response:  responseChan,
				})
				Expect(intent.Mode()).To(Equal("permission"))
			})

			It("shows tool details in the view", func() {
				responseChan := make(chan bool, 1)
				intent.Update(chat.ToolPermissionMsg{
					ToolName:  "dangerous_tool",
					Arguments: map[string]interface{}{"file": "/etc/passwd"},
					Response:  responseChan,
				})
				view := intent.View()
				Expect(view).To(ContainSubstring("dangerous_tool"))
				Expect(view).To(ContainSubstring("y/n"))
			})
		})

		Context("when user approves with 'y'", func() {
			It("sends true on response channel and returns to normal mode", func() {
				responseChan := make(chan bool, 1)
				intent.Update(chat.ToolPermissionMsg{
					ToolName:  "test_tool",
					Arguments: map[string]interface{}{},
					Response:  responseChan,
				})

				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

				Expect(intent.Mode()).To(Equal("normal"))
				Eventually(responseChan).Should(Receive(BeTrue()))
			})
		})

		Context("when user denies with 'n'", func() {
			It("sends false on response channel and returns to normal mode", func() {
				responseChan := make(chan bool, 1)
				intent.Update(chat.ToolPermissionMsg{
					ToolName:  "test_tool",
					Arguments: map[string]interface{}{},
					Response:  responseChan,
				})

				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

				Expect(intent.Mode()).To(Equal("normal"))
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
