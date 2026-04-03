package chat_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
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

		It("loads prior session messages when a session store is configured", func() {
			chat.SetRunningInTestsForTest(true)
			DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

			reg := provider.NewRegistry()
			reg.Register(&streamingStubProvider{providerName: "test-provider", chunks: []provider.StreamChunk{}})

			eng := engine.New(engine.Config{
				Registry: reg,
				Manifest: stubManifestWithProvider("test-provider", "test-model"),
			})

			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "user", Content: "hello"})
			store.Append(provider.Message{Role: "assistant", Content: "hi there"})

			sessionStore := &stubSessionLister{loadStore: store}
			startupIntent := chat.NewIntent(chat.IntentConfig{
				Engine:        eng,
				Streamer:      eng,
				AgentID:       "test-agent",
				SessionID:     "session-123",
				ProviderName:  "test-provider",
				ModelName:     "test-model",
				TokenBudget:   4096,
				SessionStore:  sessionStore,
				ModelResolver: eng.FailoverManager(),
			})

			Expect(startupIntent.Messages()).To(BeEmpty())
			Expect(startupIntent.Init()).To(BeNil())

			messages := startupIntent.AllViewMessagesForTest()
			Expect(messages).To(HaveLen(2))
			Expect(messages[0].Role).To(Equal("user"))
			Expect(messages[0].Content).To(Equal("hello"))
			Expect(messages[1].Role).To(Equal("assistant"))
			Expect(messages[1].Content).To(Equal("hi there"))
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

			It("viewport GotoBottom is called on content refresh", func() {
				// This test verifies that GotoBottom() is correctly called
				// in refreshViewport() to auto-scroll the viewport to show latest messages.
				// The regression was in commit f320246 which removed this call.
				vp := intent.ViewportForTest()
				Expect(vp).NotTo(BeNil(), "viewport should be initialized")

				// Set content with enough lines to require scrolling
				vp.SetContent("line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20")

				// After SetContent, YOffset is 0 (viewport doesn't auto-scroll)
				Expect(vp.YOffset).To(Equal(0), "YOffset should be 0 after SetContent")

				// Call GotoBottom - this is what refreshViewport() does
				vp.GotoBottom()

				// Now YOffset should be > 0 (scrolled to show latest content)
				Expect(vp.YOffset).To(BeNumerically(">", 0), "GotoBottom() should set YOffset > 0 to show latest content")
			})

			It("initializes with atBottom=true to auto-scroll new content", func() {
				Expect(intent.AtBottomForTest()).To(BeTrue(), "atBottom should start as true for auto-scroll")
			})

			It("atBottom field is reset to true when sending a message", func() {
				testIntent := chat.NewIntent(chat.IntentConfig{
					AgentID:      "test-agent",
					SessionID:    "test-session",
					ProviderName: "openai",
					ModelName:    "gpt-4o",
					TokenBudget:  4096,
				})
				testIntent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

				initialState := testIntent.AtBottomForTest()
				Expect(initialState).To(BeTrue())

				testIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
				testIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})

				Expect(testIntent.AtBottomForTest()).To(BeTrue(), "atBottom should be true after sending message")
			})

			It("preserves viewport offset when atBottom is false during streaming", func() {
				vp := intent.ViewportForTest()
				content := strings.Repeat("line\n", 30)
				vp.SetContent(content)
				vp.GotoBottom()

				initialAtBottom := intent.AtBottomForTest()
				Expect(initialAtBottom).To(BeTrue())

				chunk := chat.StreamChunkMsg{Content: "new chunk\n", Done: false}
				intent.HandleStreamChunkForTest(chunk)

				stillAtBottom := intent.AtBottomForTest()
				Expect(stillAtBottom).To(BeTrue(), "should stay at bottom if we started there")
			})
		})

		Context("multiline input", func() {
			It("inserts a newline on Alt+Enter without sending", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
				Expect(intent.Input()).To(Equal("hi\n"))
				Expect(intent.MessagesForTest()).To(BeEmpty())
			})

			It("appends text after the newline", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
				Expect(intent.Input()).To(Equal("a\nb"))
			})

			It("removes the newline on Backspace", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
				intent.Update(tea.KeyMsg{Type: tea.KeyBackspace})
				Expect(intent.Input()).To(Equal("a"))
			})

			It("sends the full multiline message on Enter", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f', 'i', 'r', 's', 't'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s', 'e', 'c', 'o', 'n', 'd'}})
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(cmd).NotTo(BeNil())
				Expect(intent.Input()).To(BeEmpty())
			})

			It("renders multiline input with continuation indent in View", func() {
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f', 'i', 'r', 's', 't'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s', 'e', 'c', 'o', 'n', 'd'}})
				view := intent.View()
				Expect(view).To(ContainSubstring("> first"))
				Expect(view).To(ContainSubstring("  second"))
			})

			It("reduces viewport height by 1 for each newline in input", func() {
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				baseHeight := intent.ViewportHeight()
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
				Expect(intent.ViewportHeight()).To(Equal(baseHeight - 1))
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
				Expect(intent.ViewportHeight()).To(Equal(baseHeight - 2))
			})
		})

		Context("slash commands", func() {
			Context("/agent command", func() {
				var (
					eng         *engine.Engine
					provReg     *provider.Registry
					agentReg    *agent.Registry
					agentIntent *chat.Intent
				)

				BeforeEach(func() {
					provReg = provider.NewRegistry()
					provReg.Register(&streamingStubProvider{
						providerName: "test-provider",
						chunks:       []provider.StreamChunk{},
					})

					agentReg = agent.NewRegistry()
					agentReg.Register(&agent.Manifest{
						ID:   "planner",
						Name: "Planner",
						Instructions: agent.Instructions{
							SystemPrompt: "You are a planner.",
						},
					})

					eng = engine.New(engine.Config{
						Registry: provReg,
						Manifest: stubManifestWithProvider("test-provider", "test-model"),
					})

					agentIntent = chat.NewIntent(chat.IntentConfig{
						Engine:        eng,
						Streamer:      eng,
						AgentID:       "test-agent",
						SessionID:     "test-session",
						ProviderName:  "test-provider",
						ModelName:     "test-model",
						TokenBudget:   4096,
						AgentRegistry: agentReg,
						ModelResolver: eng.FailoverManager(),
					})
				})

				It("switches to a known agent", func() {
					for _, r := range "/agent planner" {
						agentIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					}
					cmd := agentIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
					Expect(cmd).NotTo(BeNil())
					cmd()
					messages := agentIntent.MessagesForTest()
					Expect(messages).NotTo(BeEmpty())
					lastMsg := messages[len(messages)-1]
					Expect(lastMsg.Role).To(Equal("system"))
					Expect(lastMsg.Content).To(ContainSubstring("Switched to agent: planner"))
				})

				It("reports error for unknown agent", func() {
					for _, r := range "/agent unknown" {
						agentIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					}
					cmd := agentIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
					Expect(cmd).NotTo(BeNil())
					cmd()
					messages := agentIntent.MessagesForTest()
					Expect(messages).NotTo(BeEmpty())
					lastMsg := messages[len(messages)-1]
					Expect(lastMsg.Content).To(ContainSubstring("Unknown agent"))
				})

				It("shows usage when no agent ID provided", func() {
					for _, r := range "/agent" {
						agentIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					}
					cmd := agentIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
					Expect(cmd).NotTo(BeNil())
					cmd()
					messages := agentIntent.MessagesForTest()
					Expect(messages).NotTo(BeEmpty())
					lastMsg := messages[len(messages)-1]
					Expect(lastMsg.Content).To(ContainSubstring("Usage: /agent"))
				})
			})

			Context("/agents command", func() {
				It("lists available agents", func() {
					agentReg := agent.NewRegistry()
					agentReg.Register(&agent.Manifest{ID: "planner", Name: "Planner"})
					agentReg.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
					intent.SetAgentRegistryForTest(agentReg)

					for _, r := range "/agents" {
						intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					}
					cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
					Expect(cmd).NotTo(BeNil())
					cmd()
					messages := intent.MessagesForTest()
					Expect(messages).NotTo(BeEmpty())
					lastMsg := messages[len(messages)-1]
					Expect(lastMsg.Content).To(ContainSubstring("planner"))
					Expect(lastMsg.Content).To(ContainSubstring("executor"))
				})

				It("shows message when no agents available", func() {
					agentReg := agent.NewRegistry()
					intent.SetAgentRegistryForTest(agentReg)

					for _, r := range "/agents" {
						intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					}
					cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
					Expect(cmd).NotTo(BeNil())
					cmd()
					messages := intent.MessagesForTest()
					Expect(messages).NotTo(BeEmpty())
					lastMsg := messages[len(messages)-1]
					Expect(lastMsg.Content).To(ContainSubstring("No agents available"))
				})
			})

			Describe("toolCallSummary", func() {
				It("extracts bash command and caps at 80 chars", func() {
					args := map[string]interface{}{
						"command": "ls -la /home/user/very/long/path/that/exceeds/eighty/characters/and/should/be/truncated",
					}
					result := chat.ToolCallSummaryForTest("bash", args)
					Expect(result).To(Equal("bash: ls -la /home/user/very/long/path/that/exceeds/eighty/characters/and/should/be/tr..."))
				})

				It("returns bash command under 80 chars without truncation", func() {
					args := map[string]interface{}{
						"command": "ls -la",
					}
					result := chat.ToolCallSummaryForTest("bash", args)
					Expect(result).To(Equal("bash: ls -la"))
				})

				It("returns just tool name when bash command is empty", func() {
					args := map[string]interface{}{
						"command": "",
					}
					result := chat.ToolCallSummaryForTest("bash", args)
					Expect(result).To(Equal("bash"))
				})

				It("extracts filePath for read tool", func() {
					args := map[string]interface{}{
						"filePath": "/home/user/file.go",
					}
					result := chat.ToolCallSummaryForTest("read", args)
					Expect(result).To(Equal("read: /home/user/file.go"))
				})

				It("extracts filePath for write tool", func() {
					args := map[string]interface{}{
						"filePath": "/home/user/file.go",
					}
					result := chat.ToolCallSummaryForTest("write", args)
					Expect(result).To(Equal("write: /home/user/file.go"))
				})

				It("extracts filePath for edit tool", func() {
					args := map[string]interface{}{
						"filePath": "/home/user/file.go",
					}
					result := chat.ToolCallSummaryForTest("edit", args)
					Expect(result).To(Equal("edit: /home/user/file.go"))
				})

				It("extracts pattern for glob tool", func() {
					args := map[string]interface{}{
						"pattern": "**/*.go",
					}
					result := chat.ToolCallSummaryForTest("glob", args)
					Expect(result).To(Equal("glob: **/*.go"))
				})

				It("extracts pattern for grep tool", func() {
					args := map[string]interface{}{
						"pattern": "func.*Test",
					}
					result := chat.ToolCallSummaryForTest("grep", args)
					Expect(result).To(Equal("grep: func.*Test"))
				})

				It("returns just tool name for unimplemented task tool", func() {
					args := map[string]interface{}{
						"description": "Run tests",
					}
					result := chat.ToolCallSummaryForTest("task", args)
					Expect(result).To(Equal("task"))
				})

				It("returns just tool name for unimplemented call_omo_agent tool", func() {
					args := map[string]interface{}{
						"description": "Investigate codebase",
					}
					result := chat.ToolCallSummaryForTest("call_omo_agent", args)
					Expect(result).To(Equal("call_omo_agent"))
				})

				It("extracts name for skill_load tool", func() {
					args := map[string]interface{}{
						"name": "golang",
					}
					result := chat.ToolCallSummaryForTest("skill_load", args)
					Expect(result).To(Equal("skill_load: golang"))
				})

				It("returns just tool name for unknown tool", func() {
					args := map[string]interface{}{
						"someArg": "value",
					}
					result := chat.ToolCallSummaryForTest("unknown_tool", args)
					Expect(result).To(Equal("unknown_tool"))
				})

				It("returns just tool name when args is empty", func() {
					args := map[string]interface{}{}
					result := chat.ToolCallSummaryForTest("bash", args)
					Expect(result).To(Equal("bash"))
				})

				It("returns just tool name when type assertion fails", func() {
					args := map[string]interface{}{
						"command": 123,
					}
					result := chat.ToolCallSummaryForTest("bash", args)
					Expect(result).To(Equal("bash"))
				})
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

		It("stays at zero during streaming chunks", func() {
			for range 5 {
				intent.Update(chat.StreamChunkMsg{Content: "hello world ", Done: false})
			}
			Expect(intent.TokenCount()).To(Equal(0))
		})

		It("does not increment token count across intermediate chunks", func() {
			intent.Update(chat.StreamChunkMsg{Content: "hello world ", Done: false})
			first := intent.TokenCount()
			intent.Update(chat.StreamChunkMsg{Content: "hello world ", Done: false})
			second := intent.TokenCount()
			Expect(second).To(Equal(first))
		})

		It("renders token count in View after streaming", func() {
			for range 5 {
				intent.Update(chat.StreamChunkMsg{Content: "hello world ", Done: false})
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

	Describe("formatErrorMessage", func() {
		Context("with an HTTP error containing JSON body", func() {
			It("extracts status code and formats structured output", func() {
				err := fmt.Errorf(`POST "https://api.anthropic.com/v1/messages": 404 Not Found {"type":"error","error":{"type":"not_found_error","message":"model: claude-3-5-haiku-latest is not found"}}`)
				result := chat.FormatErrorMessageForTest(err)
				Expect(result).To(ContainSubstring("API Error"))
				Expect(result).To(ContainSubstring("404"))
				Expect(result).To(ContainSubstring("anthropic"))
				Expect(result).To(ContainSubstring("claude-3-5-haiku-latest"))
			})
		})

		Context("with a generic error string", func() {
			It("returns a truncated fallback message", func() {
				err := fmt.Errorf("connection refused")
				result := chat.FormatErrorMessageForTest(err)
				Expect(result).To(HavePrefix("⚠ Error:"))
				Expect(result).To(ContainSubstring("connection refused"))
			})
		})

		Context("with an empty error message", func() {
			It("handles gracefully", func() {
				err := fmt.Errorf("")
				result := chat.FormatErrorMessageForTest(err)
				Expect(result).To(HavePrefix("⚠ Error:"))
			})
		})

		Context("with a long unparseable error message", func() {
			It("truncates to a readable length", func() {
				longMsg := ""
				for range 20 {
					longMsg += "some long error text "
				}
				err := fmt.Errorf("%s", longMsg)
				result := chat.FormatErrorMessageForTest(err)
				Expect(len(result)).To(BeNumerically("<", len(longMsg)))
				Expect(result).To(ContainSubstring("..."))
			})
		})

		Context("with an HTTP error with model in JSON body", func() {
			It("extracts the model name from the error detail", func() {
				err := fmt.Errorf(`POST "https://api.openai.com/v1/chat/completions": 404 Not Found {"error":{"message":"The model gpt-5-turbo does not exist","type":"invalid_request_error"}}`)
				result := chat.FormatErrorMessageForTest(err)
				Expect(result).To(ContainSubstring("gpt-5-turbo"))
				Expect(result).To(ContainSubstring("openai"))
			})
		})

		Context("with an HTTP error without JSON body", func() {
			It("formats with status code only", func() {
				err := fmt.Errorf(`POST "https://api.anthropic.com/v1/messages": 500 Internal Server Error`)
				result := chat.FormatErrorMessageForTest(err)
				Expect(result).To(ContainSubstring("500"))
				Expect(result).To(ContainSubstring("API Error"))
			})
		})
	})

	Describe("streaming chunk-by-chunk", func() {
		Context("when Update receives an intermediate StreamChunkMsg", func() {
			It("returns a non-nil command to continue reading", func() {
				cmd := intent.Update(chat.StreamChunkMsg{Content: "chunk1", Done: false})
				Expect(cmd).NotTo(BeNil())
			})

			It("accumulates content in response", func() {
				intent.Update(chat.StreamChunkMsg{Content: "chunk1 ", Done: false})
				intent.Update(chat.StreamChunkMsg{Content: "chunk2 ", Done: false})
				Expect(intent.Response()).To(Equal("chunk1 chunk2 "))
			})
		})

		Context("when Update receives a final StreamChunkMsg", func() {
			It("returns nil command when no session store configured", func() {
				intent.SetStreamingForTest(true)
				cmd := intent.Update(chat.StreamChunkMsg{Content: "done", Done: true})
				Expect(cmd).To(BeNil())
			})

			It("finalizes the response into messages", func() {
				intent.SetStreamingForTest(true)
				intent.Update(chat.StreamChunkMsg{Content: "part1 ", Done: false})
				intent.Update(chat.StreamChunkMsg{Content: "part2", Done: true})
				messages := intent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Content).To(Equal("part1 part2"))
			})
		})

		Context("when thinking content arrives during streaming", func() {
			It("accumulates thinking chunks until the stream is done", func() {
				intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "First thought. ", Done: false})
				intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "Second thought.", Done: false})

				Expect(intent.AllViewMessagesForTest()).NotTo(ContainElement(HaveField("Role", Equal("thinking"))))
			})

			It("adds a thinking message when the stream finishes", func() {
				intent.SetStreamingForTest(true)
				intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "First thought. "})
				intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "Second thought.", Done: true})

				messages := intent.AllViewMessagesForTest()
				found := false
				for _, msg := range messages {
					if msg.Role == "thinking" {
						found = true
						Expect(msg.Content).To(Equal("First thought. Second thought."))
					}
				}
				Expect(found).To(BeTrue())
			})
		})

		Describe("Thinking Display Integration", func() {
			BeforeEach(func() {
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				intent.SetStreamingForTest(true)
			})

			It("renders 💭 content in the view after stream completes", func() {
				intent.Update(chat.StreamChunkMsg{Thinking: "Some reasoning.", Done: false})
				intent.Update(chat.StreamChunkMsg{Thinking: " More thinking.", Done: true})

				Expect(intent.View()).To(ContainSubstring("💭"))
				Expect(intent.View()).To(ContainSubstring("Some reasoning."))
			})

			It("does not render thinking content before completion", func() {
				intent.Update(chat.StreamChunkMsg{Thinking: "Hidden thought.", Done: false})

				Expect(intent.View()).NotTo(ContainSubstring("Hidden thought."))
			})
		})

		Context("readStreamChunk behaviour", func() {
			It("reads a single chunk from the stream channel", func() {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "hello", Done: false}
				chunkMsg := chat.ReadStreamChunkForTest(ch)
				Expect(chunkMsg.Content).To(Equal("hello"))
				Expect(chunkMsg.Done).To(BeFalse())
				Expect(chunkMsg.Next).NotTo(BeNil())
			})

			It("returns Done when channel is closed", func() {
				ch := make(chan provider.StreamChunk)
				close(ch)
				chunkMsg := chat.ReadStreamChunkForTest(ch)
				Expect(chunkMsg.Done).To(BeTrue())
				Expect(chunkMsg.Next).To(BeNil())
			})

			It("propagates chunk errors", func() {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "partial", Error: fmt.Errorf("stream error"), Done: true}
				chunkMsg := chat.ReadStreamChunkForTest(ch)
				Expect(chunkMsg.Error).To(MatchError("stream error"))
				Expect(chunkMsg.Done).To(BeTrue())
			})

		})

		Context("sendMessage with streaming engine", func() {
			var (
				eng          *engine.Engine
				reg          *provider.Registry
				streamIntent *chat.Intent
			)

			BeforeEach(func() {
				reg = provider.NewRegistry()
				streamProv := &streamingStubProvider{
					providerName: "test-provider",
					chunks: []provider.StreamChunk{
						{Content: "Hello ", Done: false},
						{Content: "World", Done: false},
					},
				}
				reg.Register(streamProv)

				eng = engine.New(engine.Config{
					Registry: reg,
					Manifest: stubManifestWithProvider("test-provider", "test-model"),
				})

				streamIntent = chat.NewIntent(chat.IntentConfig{
					Engine:       eng,
					Streamer:     eng,
					AgentID:      "test-agent",
					SessionID:    "test-session",
					ProviderName: "test-provider",
					ModelName:    "test-model",
					TokenBudget:  4096,
				})
			})

			It("returns a cmd that produces the first chunk, not all at once", func() {
				streamIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
				cmd := streamIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(cmd).NotTo(BeNil())
				msg := cmd()
				chunkMsg, ok := msg.(chat.StreamChunkMsg)
				Expect(ok).To(BeTrue())
				Expect(chunkMsg.Done).To(BeFalse())
				Expect(chunkMsg.Content).To(Equal("Hello "))
			})
		})
	})

	Describe("Error handling in streaming", func() {
		Context("when a stream error occurs", func() {
			It("displays formatted error message in the chat", func() {
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				intent.SetStreamingForTest(true)
				testErr := fmt.Errorf("connection refused")
				intent.Update(chat.StreamChunkMsg{
					Content: "",
					Error:   testErr,
					Done:    true,
				})
				messages := intent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Content).To(ContainSubstring("Error"))
				Expect(messages[0].Content).To(ContainSubstring("connection refused"))
			})

			It("preserves partial content when error occurs", func() {
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				intent.SetStreamingForTest(true)
				intent.Update(chat.StreamChunkMsg{Content: "Hello ", Error: nil, Done: false})
				Expect(intent.Response()).To(Equal("Hello "))
				testErr := fmt.Errorf("timeout")
				intent.Update(chat.StreamChunkMsg{
					Content: "",
					Error:   testErr,
					Done:    true,
				})
				messages := intent.Messages()
				Expect(messages[0].Content).To(ContainSubstring("Hello"))
				Expect(messages[0].Content).To(ContainSubstring("Error"))
				Expect(messages[0].Content).To(ContainSubstring("timeout"))
			})

			It("appends error message to assistant messages", func() {
				intent.SetStreamingForTest(true)
				testErr := fmt.Errorf("API key invalid")
				intent.Update(chat.StreamChunkMsg{
					Content: "",
					Error:   testErr,
					Done:    true,
				})
				messages := intent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Role).To(Equal("assistant"))
				Expect(messages[0].Content).To(ContainSubstring("Error"))
				Expect(messages[0].Content).To(ContainSubstring("API key invalid"))
			})

			It("accumulates partial response with error", func() {
				intent.SetStreamingForTest(true)
				intent.Update(chat.StreamChunkMsg{Content: "Part 1 ", Error: nil, Done: false})
				intent.Update(chat.StreamChunkMsg{Content: "Part 2 ", Error: nil, Done: false})
				Expect(intent.Response()).To(Equal("Part 1 Part 2 "))
				testErr := fmt.Errorf("provider error")
				intent.Update(chat.StreamChunkMsg{
					Content: "",
					Error:   testErr,
					Done:    true,
				})
				messages := intent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Content).To(ContainSubstring("Part 1"))
				Expect(messages[0].Content).To(ContainSubstring("Part 2"))
				Expect(messages[0].Content).To(ContainSubstring("Error"))
			})
		})

		Context("when no error occurs", func() {
			It("processes normal chunks without error field", func() {
				intent.SetStreamingForTest(true)
				intent.Update(chat.StreamChunkMsg{Content: "Hello", Error: nil, Done: false})
				Expect(intent.Response()).To(Equal("Hello"))
				intent.Update(chat.StreamChunkMsg{Content: " World", Error: nil, Done: true})
				messages := intent.Messages()
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Content).To(Equal("Hello World"))
				Expect(messages[0].Content).NotTo(ContainSubstring("Error:"))
			})
		})
	})

	Describe("status indicator display", func() {
		Context("when streaming is true", func() {
			It("displays thinking status indicator in the view", func() {
				intent.SetStreamingForTest(true)
				view := intent.View()
				Expect(view).To(ContainSubstring("Thinking"))
			})
		})

		Context("when streaming is false", func() {
			It("displays ready status in the view", func() {
				intent.SetStreamingForTest(false)
				view := intent.View()
				Expect(view).To(ContainSubstring("Ready"))
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

	Describe("integration: thinking and ready state display", func() {
		Context("when chat is streaming", func() {
			It("displays Thinking status in view", func() {
				intent.SetStreamingForTest(true)
				view := intent.View()
				Expect(view).To(ContainSubstring("Thinking"))
			})
		})

		Context("when chat is idle", func() {
			It("displays Ready status in view", func() {
				intent.SetStreamingForTest(false)
				view := intent.View()
				Expect(view).To(ContainSubstring("Ready"))
			})
		})

		Context("transitioning from streaming to idle", func() {
			It("shows status change from Thinking to Ready", func() {
				intent.SetStreamingForTest(true)
				streamingView := intent.View()
				Expect(streamingView).To(ContainSubstring("Thinking"))

				intent.SetStreamingForTest(false)
				readyView := intent.View()
				Expect(readyView).To(ContainSubstring("Ready"))
			})
		})
	})

	Describe("Tab key toggles between agents", func() {
		var (
			eng              *engine.Engine
			reg              *provider.Registry
			agentReg         *agent.Registry
			plannerManifest  agent.Manifest
			executorManifest agent.Manifest
			toggleIntent     *chat.Intent
		)

		BeforeEach(func() {
			plannerManifest = agent.Manifest{
				ID:   "planner",
				Name: "Planner Agent",
			}
			executorManifest = agent.Manifest{
				ID:   "executor",
				Name: "Executor Agent",
			}

			agentReg = agent.NewRegistry()
			agentReg.Register(&plannerManifest)
			agentReg.Register(&executorManifest)

			reg = provider.NewRegistry()
			reg.Register(&stubProvider{providerName: "test-provider"})

			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: executorManifest,
			})

			toggleIntent = chat.NewIntent(chat.IntentConfig{
				Engine:        eng,
				Streamer:      eng,
				AgentID:       "executor",
				SessionID:     "test-session",
				ProviderName:  "test-provider",
				ModelName:     "test-model",
				TokenBudget:   4096,
				AgentRegistry: agentReg,
			})
		})

		It("toggles from executor to planner on first Tab press", func() {
			cmd := toggleIntent.Update(tea.KeyMsg{Type: tea.KeyTab})
			Expect(cmd).To(BeNil())
			Expect(toggleIntent.AgentIDForTest()).To(Equal("planner"))
		})

		It("toggles back from planner to executor on second Tab press", func() {
			toggleIntent.Update(tea.KeyMsg{Type: tea.KeyTab})
			Expect(toggleIntent.AgentIDForTest()).To(Equal("planner"))
			toggleIntent.Update(tea.KeyMsg{Type: tea.KeyTab})
			Expect(toggleIntent.AgentIDForTest()).To(Equal("executor"))
		})

		It("updates status bar with new agent on toggle", func() {
			toggleIntent.Update(tea.KeyMsg{Type: tea.KeyTab})
			view := toggleIntent.View()
			Expect(view).To(ContainSubstring("planner"))
		})

		It("preserves message history when toggling agents", func() {
			toggleIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			toggleIntent.Update(tea.KeyMsg{Type: tea.KeyTab})
			Expect(toggleIntent.AgentIDForTest()).To(Equal("planner"))
			Expect(toggleIntent.Input()).To(Equal("hi"))
		})
	})

	Describe("session saving", func() {
		var (
			eng          *engine.Engine
			reg          *provider.Registry
			sessionStore *stubSessionLister
			saveIntent   *chat.Intent
		)

		BeforeEach(func() {
			reg = provider.NewRegistry()
			reg.Register(&streamingStubProvider{
				providerName: "test-provider",
				chunks:       []provider.StreamChunk{},
			})

			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: stubManifestWithProvider("test-provider", "test-model"),
			})

			tmpDir, err := os.MkdirTemp("", "chat-save-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tmpDir) })
			ctxStore, err := recall.NewFileContextStore(filepath.Join(tmpDir, "ctx.json"), "")
			Expect(err).NotTo(HaveOccurred())
			eng.SetContextStore(ctxStore, "test-session")

			sessionStore = &stubSessionLister{}

			saveIntent = chat.NewIntent(chat.IntentConfig{
				Engine:       eng,
				Streamer:     eng,
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
				SessionStore: sessionStore,
			})
		})

		Context("on streaming completion", func() {
			It("triggers async session save when Done is true", func() {
				saveIntent.SetStreamingForTest(true)
				cmd := saveIntent.Update(chat.StreamChunkMsg{Content: "response", Done: true})
				Expect(cmd).NotTo(BeNil())
				msg := cmd()
				_, ok := msg.(chat.SessionSavedMsg)
				Expect(ok).To(BeTrue())
				Expect(sessionStore.saveCalled).To(BeTrue())
			})

			It("passes the correct session ID", func() {
				saveIntent.SetStreamingForTest(true)
				cmd := saveIntent.Update(chat.StreamChunkMsg{Content: "done", Done: true})
				Expect(cmd).NotTo(BeNil())
				cmd()
				Expect(sessionStore.savedID).To(Equal("test-session"))
			})

			It("passes the correct agent ID in metadata", func() {
				saveIntent.SetStreamingForTest(true)
				cmd := saveIntent.Update(chat.StreamChunkMsg{Content: "done", Done: true})
				Expect(cmd).NotTo(BeNil())
				cmd()
				Expect(sessionStore.savedMeta.AgentID).To(Equal("test-agent"))
			})
		})

		Context("on KeyCtrlC exit", func() {
			It("returns a non-nil command that includes session save", func() {
				cmd := saveIntent.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
				Expect(cmd).NotTo(BeNil())
			})
		})

		Context("with nil session store", func() {
			It("returns nil command on streaming completion", func() {
				nilStoreIntent := chat.NewIntent(chat.IntentConfig{
					AgentID:      "test-agent",
					SessionID:    "test-session",
					ProviderName: "openai",
					ModelName:    "gpt-4o",
					TokenBudget:  4096,
				})
				nilStoreIntent.SetStreamingForTest(true)
				cmd := nilStoreIntent.Update(chat.StreamChunkMsg{Content: "done", Done: true})
				Expect(cmd).To(BeNil())
			})
		})

		Context("with nil engine", func() {
			It("returns nil from saveSession", func() {
				noEngineIntent := chat.NewIntent(chat.IntentConfig{
					AgentID:      "test-agent",
					SessionID:    "test-session",
					ProviderName: "openai",
					ModelName:    "gpt-4o",
					TokenBudget:  4096,
					SessionStore: sessionStore,
				})
				cmd := noEngineIntent.SaveSessionForTest()
				Expect(cmd).To(BeNil())
			})
		})

		Context("when save returns an error", func() {
			It("propagates the error in SessionSavedMsg", func() {
				sessionStore.saveErr = fmt.Errorf("disk full")
				saveIntent.SetStreamingForTest(true)
				cmd := saveIntent.Update(chat.StreamChunkMsg{Content: "done", Done: true})
				Expect(cmd).NotTo(BeNil())
				msg := cmd()
				savedMsg, ok := msg.(chat.SessionSavedMsg)
				Expect(ok).To(BeTrue())
				Expect(savedMsg.Err).To(MatchError("disk full"))
			})
		})
	})

	Describe("detectAgentFromInput", func() {
		DescribeTable("returns the correct agent for keyword-based input",
			func(input, expected string) {
				Expect(chat.DetectAgentFromInputForTest(input)).To(Equal(expected))
			},
			Entry("planner keyword: plan", "I want to create a plan for a new API", "planner"),
			Entry("planner keyword: design", "design the architecture", "planner"),
			Entry("planner keyword: architect", "architect a microservice", "planner"),
			Entry("planner keyword: how do i", "how do i set up auth?", "planner"),
			Entry("planner keyword: what should", "what should we build next?", "planner"),
			Entry("planner keyword: help me", "help me figure this out", "planner"),
			Entry("planner keyword: strategy", "we need a strategy", "planner"),
			Entry("planner keyword: create a plan", "create a plan for deployment", "planner"),
			Entry("planner keyword: let's plan", "let's plan the sprint", "planner"),
			Entry("planner keyword: i want to build", "i want to build a CLI tool", "planner"),
			Entry("planner keyword: i need to", "i need to refactor the API", "planner"),
			Entry("executor keyword: execute", "execute this task", "executor"),
			Entry("executor keyword: run the plan", "run the plan for deployment", "planner"),
			Entry("executor keyword: start execution", "start execution of phase 1", "executor"),
			Entry("executor keyword: begin execution", "begin execution immediately", "executor"),
			Entry("executor keyword: run it", "run it please", "executor"),
			Entry("executor keyword: do it", "do it now", "executor"),
			Entry("executor keyword: implement", "implement the feature", "executor"),
			Entry("no match: generic greeting", "hello, how are you?", ""),
			Entry("no match: empty string", "", ""),
			Entry("planner takes priority over executor", "plan to implement the feature", "planner"),
			Entry("case insensitive: uppercase", "DESIGN the system", "planner"),
			Entry("case insensitive: mixed case", "Execute The Task", "executor"),
		)
	})

	Describe("auto agent switching on message send", func() {
		var (
			eng              *engine.Engine
			reg              *provider.Registry
			agentReg         *agent.Registry
			plannerManifest  agent.Manifest
			executorManifest agent.Manifest
			autoSwitchIntent *chat.Intent
		)

		BeforeEach(func() {
			plannerManifest = agent.Manifest{
				ID:   "planner",
				Name: "Planner Agent",
			}
			executorManifest = agent.Manifest{
				ID:   "executor",
				Name: "Executor Agent",
			}

			agentReg = agent.NewRegistry()
			agentReg.Register(&plannerManifest)
			agentReg.Register(&executorManifest)

			reg = provider.NewRegistry()
			reg.Register(&streamingStubProvider{
				providerName: "test-provider",
				chunks:       []provider.StreamChunk{},
			})

			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: executorManifest,
			})

			autoSwitchIntent = chat.NewIntent(chat.IntentConfig{
				Engine:        eng,
				Streamer:      eng,
				AgentID:       "executor",
				SessionID:     "test-session",
				ProviderName:  "test-provider",
				ModelName:     "test-model",
				TokenBudget:   4096,
				AgentRegistry: agentReg,
			})
		})

		It("switches to planner when user sends a planner-keyword message", func() {
			for _, r := range "help me design this" {
				autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			cmd := autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())
			Expect(autoSwitchIntent.AgentIDForTest()).To(Equal("planner"))
		})

		It("switches to executor when user sends an executor-keyword message", func() {
			autoSwitchIntent.SetAgentIDForTest("planner")
			for _, r := range "implement the feature" {
				autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			cmd := autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())
			Expect(autoSwitchIntent.AgentIDForTest()).To(Equal("executor"))
		})

		It("does not switch when no keywords match", func() {
			for _, r := range "hello world" {
				autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			cmd := autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())
			Expect(autoSwitchIntent.AgentIDForTest()).To(Equal("executor"))
		})

		It("does not switch when already on the detected agent", func() {
			for _, r := range "execute something" {
				autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			cmd := autoSwitchIntent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())
			Expect(autoSwitchIntent.AgentIDForTest()).To(Equal("executor"))
		})
	})

	Describe("modal model selection updates engine routing", func() {
		var (
			eng           *engine.Engine
			reg           *provider.Registry
			intentWithEng *chat.Intent
		)

		BeforeEach(func() {
			reg = provider.NewRegistry()
			reg.Register(&stubProvider{providerName: "anthropic", providerModels: []provider.Model{
				{ID: "claude-3", Provider: "anthropic"},
			}})

			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: stubManifestWithProvider("anthropic", "claude-3"),
			})

			intentWithEng = chat.NewIntent(chat.IntentConfig{
				App:          &stubAppShell{registry: reg},
				Engine:       eng,
				Streamer:     eng,
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "ollama",
				ModelName:    "llama3.2",
				TokenBudget:  4096,
			})
		})

		It("updates engine failback preferences after modal model selection", func() {
			selected := intentWithEng.SimulateModalModelSelectionForTest()
			Expect(selected).To(BeTrue())

			Expect(eng.LastProvider()).To(Equal("anthropic"))
			Expect(eng.LastModel()).To(Equal("claude-3"))
		})

		It("updates intent display state after modal model selection", func() {
			selected := intentWithEng.SimulateModalModelSelectionForTest()
			Expect(selected).To(BeTrue())

			Expect(intentWithEng.ProviderNameForTest()).To(Equal("anthropic"))
			Expect(intentWithEng.ModelNameForTest()).To(Equal("claude-3"))
		})
	})

	Describe("Ctrl+A opens agent picker modal", func() {
		var (
			eng          *engine.Engine
			reg          *provider.Registry
			agentReg     *agent.Registry
			pickerIntent *chat.Intent
		)

		BeforeEach(func() {
			agentReg = agent.NewRegistry()
			agentReg.Register(&agent.Manifest{
				ID:   "planner",
				Name: "Planner Agent",
			})
			agentReg.Register(&agent.Manifest{
				ID:   "executor",
				Name: "Executor Agent",
			})

			reg = provider.NewRegistry()
			reg.Register(&stubProvider{providerName: "test-provider"})

			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: stubManifestWithProvider("test-provider", "test-model"),
			})

			pickerIntent = chat.NewIntent(chat.IntentConfig{
				Engine:        eng,
				Streamer:      eng,
				AgentID:       "executor",
				SessionID:     "test-session",
				ProviderName:  "test-provider",
				ModelName:     "test-model",
				TokenBudget:   4096,
				AgentRegistry: agentReg,
			})
		})

		It("returns a non-nil command on Ctrl+A", func() {
			cmd := pickerIntent.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
			Expect(cmd).NotTo(BeNil())
		})

		It("returns nil command when no agent registry is configured", func() {
			noRegIntent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
			cmd := noRegIntent.OpenAgentPickerForTest()
			Expect(cmd).NotTo(BeNil())
			msg := cmd()
			Expect(msg).To(BeNil())
		})

		It("switches agent when selecting from the picker", func() {
			Expect(pickerIntent.AgentIDForTest()).To(Equal("executor"))
			selected := pickerIntent.SimulateAgentPickerSelectionForTest(0)
			Expect(selected).To(BeTrue())
		})

		It("updates engine manifest after agent selection via picker", func() {
			pickerIntent.SimulateAgentPickerSelectionForTest(1)
			Expect(pickerIntent.AgentIDForTest()).To(Equal("planner"))
		})

		It("updates status bar after agent selection via picker", func() {
			pickerIntent.SimulateAgentPickerSelectionForTest(1)
			view := pickerIntent.View()
			Expect(view).To(ContainSubstring("planner"))
		})

		It("preserves input text when opening agent picker", func() {
			pickerIntent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
			pickerIntent.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
			Expect(pickerIntent.Input()).To(Equal("hi"))
		})
	})

	Describe("handleSessionLoaded tool call formatting", func() {
		var (
			eng           *engine.Engine
			reg           *provider.Registry
			sessionIntent *chat.Intent
		)

		BeforeEach(func() {
			reg = provider.NewRegistry()
			reg.Register(&streamingStubProvider{
				providerName: "test-provider",
				chunks:       []provider.StreamChunk{},
			})
			eng = engine.New(engine.Config{
				Registry: reg,
				Manifest: stubManifestWithProvider("test-provider", "test-model"),
			})
			sessionIntent = chat.NewIntent(chat.IntentConfig{
				Engine:       eng,
				Streamer:     eng,
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
		})

		It("converts assistant messages with tool calls to tool_call messages", func() {
			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "user", Content: "run ls"})
			store.Append(provider.Message{
				Role:      "assistant",
				Content:   "",
				ToolCalls: []provider.ToolCall{{Name: "bash", ID: "tc-1"}},
			})
			store.Append(provider.Message{Role: "tool", Content: "file1.go\nfile2.go"})
			store.Append(provider.Message{Role: "assistant", Content: "Here are your files."})

			sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "loaded-session",
				Store:     store,
			})

			messages := sessionIntent.AllViewMessagesForTest()
			Expect(messages).To(HaveLen(4))
			Expect(messages[0].Role).To(Equal("user"))
			Expect(messages[0].Content).To(Equal("run ls"))
			Expect(messages[1].Role).To(Equal("tool_call"))
			Expect(messages[1].Content).To(Equal("bash"))
			Expect(messages[2].Role).To(Equal("tool_result"))
			Expect(messages[2].Content).To(Equal("file1.go\nfile2.go"))
			Expect(messages[3].Role).To(Equal("assistant"))
			Expect(messages[3].Content).To(Equal("Here are your files."))
		})

		It("converts tool role messages to tool_result messages", func() {
			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "tool", Content: "file1.go\nfile2.go"})

			sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "loaded-session",
				Store:     store,
			})

			messages := sessionIntent.AllViewMessagesForTest()
			Expect(messages).To(HaveLen(1))
			Expect(messages[0].Role).To(Equal("tool_result"))
			Expect(messages[0].Content).To(Equal("file1.go\nfile2.go"))
		})

		It("adds one tool_call per ToolCall entry on assistant message", func() {
			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []provider.ToolCall{
					{Name: "bash", ID: "tc-1"},
					{Name: "read_file", ID: "tc-2"},
				},
			})

			sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "loaded-session",
				Store:     store,
			})

			messages := sessionIntent.AllViewMessagesForTest()
			Expect(messages).To(HaveLen(2))
			Expect(messages[0].Role).To(Equal("tool_call"))
			Expect(messages[0].Content).To(Equal("bash"))
			Expect(messages[1].Role).To(Equal("tool_call"))
			Expect(messages[1].Content).To(Equal("read_file"))
		})

		It("passes through regular messages unchanged", func() {
			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "user", Content: "hello"})
			store.Append(provider.Message{Role: "assistant", Content: "hi there"})
			store.Append(provider.Message{Role: "system", Content: "system prompt"})

			sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "loaded-session",
				Store:     store,
			})

			messages := sessionIntent.AllViewMessagesForTest()
			Expect(messages).To(HaveLen(3))
			Expect(messages[0].Role).To(Equal("user"))
			Expect(messages[1].Role).To(Equal("assistant"))
			Expect(messages[2].Role).To(Equal("system"))
		})

		It("converts skill_load tool calls to skill_load messages", func() {
			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{
				Role:      "assistant",
				Content:   "",
				ToolCalls: []provider.ToolCall{{Name: "skill_load", ID: "tc-skill"}},
			})

			sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "loaded-session",
				Store:     store,
			})

			messages := sessionIntent.AllViewMessagesForTest()
			Expect(messages).To(HaveLen(1))
			Expect(messages[0].Role).To(Equal("skill_load"))
			Expect(messages[0].Content).To(Equal("skill_load"))
		})

		It("restores todowrite tool results as todo_update role on session reload", func() {
			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{
				Role:      "assistant",
				Content:   "",
				ToolCalls: []provider.ToolCall{{Name: "todowrite", ID: "tc-todo"}},
			})
			store.Append(provider.Message{Role: "tool", Content: `[{"content":"Write tests","status":"pending","priority":"high"}]`})

			sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "loaded-session",
				Store:     store,
			})

			messages := sessionIntent.AllViewMessagesForTest()
			var found bool
			for _, msg := range messages {
				if msg.Role == "todo_update" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "todowrite tool result should be restored as todo_update role")
		})
	})

	Describe("activeToolCall streaming lifecycle", func() {
		It("tracks active tool call name during streaming", func() {
			intent.Update(chat.StreamChunkMsg{ToolCallName: "bash", Content: ""})
			Expect(intent.ActiveToolCallForTest()).To(Equal("bash"))
		})

		It("adds tool_call message and clears state when tool completes", func() {
			intent.SetStreamingForTest(true)
			intent.Update(chat.StreamChunkMsg{ToolCallName: "bash", Content: ""})
			intent.Update(chat.StreamChunkMsg{ToolCallName: "", Content: "result"})

			Expect(intent.ActiveToolCallForTest()).To(BeEmpty())
			messages := intent.AllViewMessagesForTest()
			Expect(messages).To(ContainElement(
				HaveField("Role", "tool_call"),
			))
		})

		It("does not add tool_call message when no tool was active", func() {
			intent.SetStreamingForTest(true)
			intent.Update(chat.StreamChunkMsg{ToolCallName: "", Content: "normal text"})

			Expect(intent.ActiveToolCallForTest()).To(BeEmpty())
		})
	})

	Describe("message ordering during streaming with tool calls", func() {
		It("places response text before tool_call message when tool call arrives mid-stream", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "I will run bash"})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "bash"})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "", Content: ""})

			messages := intent.AllViewMessagesForTest()

			assistantIdx := -1
			toolCallIdx := -1
			for idx, msg := range messages {
				if msg.Role == "assistant" && assistantIdx == -1 {
					assistantIdx = idx
				}
				if msg.Role == "tool_call" {
					toolCallIdx = idx
				}
			}

			Expect(assistantIdx).To(BeNumerically(">=", 0), "assistant message should be committed")
			Expect(toolCallIdx).To(BeNumerically(">=", 0), "tool_call message should be committed")
			Expect(assistantIdx).To(BeNumerically("<", toolCallIdx), "response text before tool_call")
		})

		It("places response text before tool_call message for skill tool calls", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "Loading skill"})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "skill:pre-action"})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "", Content: ""})

			messages := intent.AllViewMessagesForTest()

			assistantIdx := -1
			skillLoadIdx := -1
			for idx, msg := range messages {
				if msg.Role == "assistant" && assistantIdx == -1 {
					assistantIdx = idx
				}
				if msg.Role == "skill_load" {
					skillLoadIdx = idx
				}
			}

			Expect(assistantIdx).To(BeNumerically(">=", 0), "assistant message should be committed")
			Expect(skillLoadIdx).To(BeNumerically(">=", 0), "skill_load message should be committed")
			Expect(assistantIdx).To(BeNumerically("<", skillLoadIdx), "response text before skill_load")
		})

		It("does not create an empty assistant message when tool call arrives with no prior text", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "bash"})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "", Content: ""})

			messages := intent.AllViewMessagesForTest()

			for _, msg := range messages {
				if msg.Role == "assistant" {
					Expect(msg.Content).NotTo(BeEmpty(), "flushed assistant message must have content")
				}
			}
		})
	})

	Describe("harness retry clears activeToolCall", func() {
		It("clears activeToolCall before restarting streaming on retry", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "bash"})
			Expect(intent.ActiveToolCallForTest()).To(Equal("bash"))

			intent.Update(chat.StreamChunkMsg{EventType: "harness_retry", Content: "Retrying..."})

			Expect(intent.ActiveToolCallForTest()).To(BeEmpty())
		})

		It("does not emit a duplicate tool_call message when retry fires mid-tool-call", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "bash"})
			intent.Update(chat.StreamChunkMsg{EventType: "harness_retry", Content: "Retrying..."})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{ToolCallName: "", Content: "result"})

			messages := intent.AllViewMessagesForTest()
			toolCallCount := 0
			for _, msg := range messages {
				if msg.Role == "tool_call" {
					toolCallCount++
				}
			}
			Expect(toolCallCount).To(Equal(0), "no tool_call message should appear after a retry that cleared the active tool call")
		})
	})
})

type stubProvider struct {
	providerName   string
	providerModels []provider.Model
}

func (p *stubProvider) Name() string { return p.providerName }

func (p *stubProvider) Models() ([]provider.Model, error) {
	return p.providerModels, nil
}

func (p *stubProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *stubProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	go func() { close(ch) }()
	return ch, nil
}

func (p *stubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

type stubAppShell struct {
	registry *provider.Registry
}

func (s *stubAppShell) WriteConfig(_ *config.AppConfig) error { return nil }

func (s *stubAppShell) List() []string { return s.registry.List() }

func (s *stubAppShell) Get(name string) (provider.Provider, error) { return s.registry.Get(name) }

type streamingStubProvider struct {
	providerName string
	chunks       []provider.StreamChunk
}

func (p *streamingStubProvider) Name() string { return p.providerName }

func (p *streamingStubProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "test-model", Provider: p.providerName}}, nil
}

func (p *streamingStubProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *streamingStubProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(p.chunks))
	for i := range p.chunks {
		ch <- p.chunks[i]
	}
	close(ch)
	return ch, nil
}

func (p *streamingStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func stubManifestWithProvider(_, _ string) agent.Manifest {
	return agent.Manifest{
		ID:         "test-agent",
		Name:       "Test Agent",
		Complexity: "standard",
	}
}

type stubSessionLister struct {
	saveCalled bool
	savedID    string
	savedMeta  contextpkg.SessionMetadata
	saveErr    error
	loadStore  *recall.FileContextStore
	loadErr    error
	sessions   []contextpkg.SessionInfo
}

func (s *stubSessionLister) List() []contextpkg.SessionInfo { return s.sessions }

func (s *stubSessionLister) SetTitle(_ string, _ string) error { return nil }

func (s *stubSessionLister) Load(_ string) (*recall.FileContextStore, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.loadStore != nil {
		return s.loadStore, nil
	}
	return nil, errors.New("stub: not implemented")
}

func (s *stubSessionLister) Save(sessionID string, _ *recall.FileContextStore, meta contextpkg.SessionMetadata) error {
	s.saveCalled = true
	s.savedID = sessionID
	s.savedMeta = meta
	return s.saveErr
}

var _ = Describe("skill_load tool call handling", func() {
	Describe("readNextChunk with skill_load", func() {
		It("extracts skill name from skill_load tool call arguments", func() {
			streamChan := make(chan provider.StreamChunk, 1)
			streamChan <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name: "skill_load",
					Arguments: map[string]interface{}{
						"name": "pre-action",
					},
				},
			}
			close(streamChan)

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})

			intent.SetStreamChanForTest(streamChan)
			msg := intent.ReadNextChunkForTest().(chat.StreamChunkMsg)
			Expect(msg.ToolCallName).To(Equal("skill:pre-action"))
		})

		It("leaves non-skill tool calls unchanged", func() {
			streamChan := make(chan provider.StreamChunk, 1)
			streamChan <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name: "bash",
					Arguments: map[string]interface{}{
						"command": "ls",
					},
				},
			}
			close(streamChan)

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})

			intent.SetStreamChanForTest(streamChan)
			msg := intent.ReadNextChunkForTest().(chat.StreamChunkMsg)
			Expect(msg.ToolCallName).To(Equal("bash: ls"))
		})

		It("handles missing skill name gracefully", func() {
			streamChan := make(chan provider.StreamChunk, 1)
			streamChan <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "skill_load",
					Arguments: map[string]interface{}{},
				},
			}
			close(streamChan)

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})

			intent.SetStreamChanForTest(streamChan)
			msg := intent.ReadNextChunkForTest().(chat.StreamChunkMsg)
			Expect(msg.ToolCallName).To(Equal("skill_load"))
		})
	})

	Describe("readStreamChunk with skill_load", func() {
		It("extracts skill name from skill_load tool call arguments", func() {
			streamChan := make(chan provider.StreamChunk, 1)
			streamChan <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name: "skill_load",
					Arguments: map[string]interface{}{
						"name": "memory-keeper",
					},
				},
			}
			close(streamChan)

			msg := chat.ReadStreamChunkForTest(streamChan)
			Expect(msg.ToolCallName).To(Equal("skill:memory-keeper"))
		})

		It("leaves non-skill tool calls unchanged", func() {
			streamChan := make(chan provider.StreamChunk, 1)
			streamChan <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name: "bash",
					Arguments: map[string]interface{}{
						"command": "ls",
					},
				},
			}
			close(streamChan)

			msg := chat.ReadStreamChunkForTest(streamChan)
			Expect(msg.ToolCallName).To(Equal("bash: ls"))
		})
	})

	Describe("handleStreamChunk with skill_load message", func() {
		It("adds skill_load message with skill name when tool call completes", func() {
			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "skill:pre-action",
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "",
			})

			messages := intent.MessagesForTest()
			found := false
			for _, msg := range messages {
				if msg.Role == "skill_load" && msg.Content == "pre-action" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("adds tool_call message for non-skill tool calls", func() {
			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "bash",
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "",
			})

			messages := intent.MessagesForTest()
			found := false
			for _, msg := range messages {
				if msg.Role == "tool_call" && msg.Content == "bash" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("suppresses skill_load results from view messages", func() {
			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "skill:pre-action",
				ToolResult:   "This is skill content that should not appear",
				ToolIsError:  false,
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "",
			})

			messages := intent.MessagesForTest()
			for _, msg := range messages {
				Expect(msg.Role).NotTo(Equal("tool_result"))
			}
		})

		It("shows tool_result for non-skill tool calls", func() {
			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "bash",
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "",
			})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				ToolResult:  "command output",
				ToolIsError: false,
			})

			messages := intent.MessagesForTest()
			found := false
			for _, msg := range messages {
				if msg.Role == "tool_result" && msg.Content == "command output" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})
	})
})

var _ = Describe("isReadToolCall", func() {
	DescribeTable("returns true for read tool call names",
		func(name string, expected bool) {
			Expect(chat.IsReadToolCallForTest(name)).To(Equal(expected))
		},
		Entry("raw tool name", "read", true),
		Entry("formatted with file path", "read: /some/file.go", true),
		Entry("formatted with short path", "read: README.md", true),
		Entry("bash is not a read", "bash", false),
		Entry("bash with command is not a read", "bash: ls -la", false),
		Entry("grep is not a read", "grep: pattern", false),
		Entry("empty string is not a read", "", false),
		Entry("skill_load is not a read", "skill:pre-action", false),
	)
})

var _ = Describe("read tool suppression during streaming", func() {
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

	It("suppresses read tool result from the chat view", func() {
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "read: /path/to/file.go",
		})
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "",
			ToolResult:   "package main\n\nfunc main() {}",
			ToolIsError:  false,
		})

		messages := intent.MessagesForTest()
		for _, msg := range messages {
			Expect(msg.Role).NotTo(Equal("tool_result"), "read tool result should be suppressed")
		}
	})

	It("still shows read tool call indicator in the chat view", func() {
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "read: /path/to/file.go",
		})
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "",
		})

		messages := intent.MessagesForTest()
		found := false
		for _, msg := range messages {
			if msg.Role == "tool_call" && msg.Content == "read: /path/to/file.go" {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "read tool_call indicator should still appear")
	})

	It("still shows read tool error in the chat view", func() {
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "read: /nonexistent/file.go",
		})
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "",
			ToolResult:   "Error: file not found",
			ToolIsError:  true,
		})

		messages := intent.MessagesForTest()
		found := false
		for _, msg := range messages {
			if msg.Role == "tool_error" {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "read tool error should still appear in chat")
	})

	It("does not suppress bash tool results after a read", func() {
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "read: /some/file.go",
		})
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "",
			ToolResult:   "file content here",
			ToolIsError:  false,
		})
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "bash: echo hello",
		})
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolCallName: "",
		})
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
			ToolResult:  "hello",
			ToolIsError: false,
		})

		messages := intent.MessagesForTest()
		foundBashResult := false
		for _, msg := range messages {
			if msg.Role == "tool_result" && msg.Content == "hello" {
				foundBashResult = true
			}
			if msg.Role == "tool_result" && msg.Content == "file content here" {
				Fail("read tool result should have been suppressed")
			}
		}
		Expect(foundBashResult).To(BeTrue(), "bash tool result should still appear")
	})
})

var _ = Describe("read tool suppression on session reload", func() {
	var (
		eng           *engine.Engine
		reg           *provider.Registry
		sessionIntent *chat.Intent
	)

	BeforeEach(func() {
		reg = provider.NewRegistry()
		reg.Register(&streamingStubProvider{
			providerName: "test-provider",
			chunks:       []provider.StreamChunk{},
		})
		eng = engine.New(engine.Config{
			Registry: reg,
			Manifest: stubManifestWithProvider("test-provider", "test-model"),
		})
		sessionIntent = chat.NewIntent(chat.IntentConfig{
			Engine:       eng,
			Streamer:     eng,
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  4096,
		})
	})

	It("suppresses read tool result on session reload", func() {
		store := recall.NewEmptyContextStore("")
		store.Append(provider.Message{Role: "user", Content: "show me the file"})
		store.Append(provider.Message{
			Role:      "assistant",
			Content:   "",
			ToolCalls: []provider.ToolCall{{Name: "read", ID: "tc-read", Arguments: map[string]interface{}{"filePath": "/main.go"}}},
		})
		store.Append(provider.Message{Role: "tool", Content: "package main\n\nfunc main() {}"})
		store.Append(provider.Message{Role: "assistant", Content: "Here is the file."})

		sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "loaded-session",
			Store:     store,
		})

		messages := sessionIntent.AllViewMessagesForTest()
		for _, msg := range messages {
			Expect(msg.Role).NotTo(Equal("tool_result"), "read tool result should be suppressed on reload")
		}
	})

	It("keeps bash tool result visible on session reload", func() {
		store := recall.NewEmptyContextStore("")
		store.Append(provider.Message{
			Role:      "assistant",
			Content:   "",
			ToolCalls: []provider.ToolCall{{Name: "bash", ID: "tc-bash", Arguments: map[string]interface{}{"command": "ls"}}},
		})
		store.Append(provider.Message{Role: "tool", Content: "file1.go\nfile2.go"})

		sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "loaded-session",
			Store:     store,
		})

		messages := sessionIntent.AllViewMessagesForTest()
		found := false
		for _, msg := range messages {
			if msg.Role == "tool_result" && msg.Content == "file1.go\nfile2.go" {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "bash tool result should still be visible")
	})

	It("keeps read tool error visible on session reload", func() {
		store := recall.NewEmptyContextStore("")
		store.Append(provider.Message{
			Role:      "assistant",
			Content:   "",
			ToolCalls: []provider.ToolCall{{Name: "read", ID: "tc-read", Arguments: map[string]interface{}{"filePath": "/missing.go"}}},
		})
		store.Append(provider.Message{Role: "tool", Content: "Error: file not found"})

		sessionIntent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "loaded-session",
			Store:     store,
		})

		messages := sessionIntent.AllViewMessagesForTest()
		found := false
		for _, msg := range messages {
			if msg.Role == "tool_error" {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "read tool error should still be visible on reload")
	})
})

var _ = Describe("model ID stamping", func() {
	var (
		eng          *engine.Engine
		reg          *provider.Registry
		sessionStore *stubSessionLister
	)

	BeforeEach(func() {
		reg = provider.NewRegistry()
		reg.Register(&streamingStubProvider{
			providerName: "test-provider",
			chunks:       []provider.StreamChunk{},
		})
		eng = engine.New(engine.Config{
			Registry: reg,
			Manifest: stubManifestWithProvider("test-provider", "test-model"),
		})
		sessionStore = &stubSessionLister{}
	})

	It("stamps messages with the model active at stream start, not the current model", func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		i := chat.NewIntent(chat.IntentConfig{
			Engine:       eng,
			Streamer:     eng,
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "test-provider",
			ModelName:    "model-A",
			TokenBudget:  4096,
			SessionStore: sessionStore,
		})

		i.SetStreamingForTest(true)
		i.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "hello", Done: false})

		i.SetModelNameForTest("model-B")

		i.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: " world", Done: true, ModelID: "model-A"})

		messages := i.AllViewMessagesForTest()
		var assistantMsg *chatview.Message
		for idx := range messages {
			if messages[idx].Role == "assistant" {
				assistantMsg = &messages[idx]
				break
			}
		}
		Expect(assistantMsg).NotTo(BeNil())
		Expect(assistantMsg.ModelID).To(Equal("model-A"))
	})

	It("does not overwrite existing ModelID when loading session history", func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		store := recall.NewEmptyContextStore("")
		store.Append(provider.Message{Role: "user", Content: "hello"})
		store.Append(provider.Message{Role: "assistant", Content: "hi there", ModelID: "original-model"})

		sessionStore.loadStore = store

		i := chat.NewIntent(chat.IntentConfig{
			Engine:       eng,
			Streamer:     eng,
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "test-provider",
			ModelName:    "current-model",
			TokenBudget:  4096,
			SessionStore: sessionStore,
		})

		i.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "test-session",
			Store:     store,
		})

		messages := i.AllViewMessagesForTest()
		var assistantMsg *chatview.Message
		for idx := range messages {
			if messages[idx].Role == "assistant" {
				assistantMsg = &messages[idx]
				break
			}
		}
		Expect(assistantMsg).NotTo(BeNil())
		Expect(assistantMsg.ModelID).To(Equal("original-model"))
	})
})

var _ = Describe("MessageFooter Integration", func() {
	var (
		i            *chat.Intent
		eng          *engine.Engine
		reg          *provider.Registry
		sessionStore *stubSessionLister
	)

	BeforeEach(func() {
		reg = provider.NewRegistry()
		reg.Register(&streamingStubProvider{
			providerName: "test-provider",
			chunks:       []provider.StreamChunk{},
		})
		eng = engine.New(engine.Config{
			Registry: reg,
			Manifest: stubManifestWithProvider("test-provider", "test-model"),
		})
		sessionStore = &stubSessionLister{}

		i = chat.NewIntent(chat.IntentConfig{
			Engine:       eng,
			Streamer:     eng,
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "test-provider",
			ModelName:    "current-model",
			TokenBudget:  4096,
			SessionStore: sessionStore,
		})
		i.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	})

	It("renders ▣ and modelID footer for completed assistant message", func() {
		store := recall.NewEmptyContextStore("")
		store.Append(provider.Message{Role: "user", Content: "hello"})
		store.Append(provider.Message{Role: "assistant", Content: "hi there", ModelID: "gpt-4o"})

		i.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "test-session",
			Store:     store,
		})

		view := i.View()
		Expect(view).To(ContainSubstring("▣"))
		Expect(view).To(ContainSubstring("gpt-4o"))
	})

	It("stamps the intent model ID onto assistant messages that have no ModelID", func() {
		store := recall.NewEmptyContextStore("")
		store.Append(provider.Message{Role: "user", Content: "hello"})
		store.Append(provider.Message{Role: "assistant", Content: "hi there"})

		i.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "test-session",
			Store:     store,
		})

		view := i.View()
		Expect(view).To(ContainSubstring("▣"))
		Expect(view).To(ContainSubstring("current-model"))
	})

	Context("with agent colour", func() {
		It("renders footer ▣ indicator without panic when AgentColor is set", func() {
			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "assistant", Content: "coloured response", ModelID: "claude-sonnet"})

			i.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "test-session",
				Store:     store,
			})

			messages := i.AllViewMessagesForTest()
			var found bool
			for idx := range messages {
				if messages[idx].ModelID == "claude-sonnet" {
					found = true
				}
			}
			Expect(found).To(BeTrue())

			Expect(func() { i.View() }).NotTo(Panic())
		})
	})
})
