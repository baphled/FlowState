package chat_test

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
)

// P17.S3 session-scoped permission cache.
//
// The specs pin:
//
//   - 's' approves the current tool and inserts it into
//     sessionApprovedTools, emitting ToolPermissionDecision{Approved:
//     true, Remember: true}.
//   - A follow-up ToolPermissionMsg for the same tool name is
//     auto-approved (no prompt, same decision shape).
//   - 'y' approves one-off, does NOT populate the cache, so a second
//     request for the same tool is re-prompted.
//   - 'n' denies one-off, does NOT populate the cache.
//   - handleSessionLoaded wipes the cache so 's'-granted approvals do
//     not leak across sessions.
var _ = Describe("Session-scoped permission cache", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "perm-cache-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("approve-and-remember with 's'", func() {
		It("emits a Remember=true decision and caches the tool name", func() {
			ch := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "ls"},
				Response:  ch,
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

			var decision chat.ToolPermissionDecision
			Eventually(ch).Should(Receive(&decision))
			Expect(decision).To(Equal(chat.ToolPermissionDecision{
				Approved: true, Remember: true,
			}))
		})

		It("auto-approves the next request for the same tool without prompting", func() {
			firstCh := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "ls"},
				Response:  firstCh,
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
			Eventually(firstCh).Should(Receive())

			// Second request: the intent must auto-resolve without
			// waiting for a key press.
			secondCh := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "pwd"},
				Response:  secondCh,
			})

			select {
			case got := <-secondCh:
				Expect(got).To(Equal(chat.ToolPermissionDecision{
					Approved: true, Remember: true,
				}))
			case <-time.After(200 * time.Millisecond):
				Fail("expected auto-approval on second ToolPermissionMsg, " +
					"but response channel is empty")
			}

			// The view must not be showing the permission prompt,
			// since we short-circuited before entering permission
			// mode.
			Expect(intent.View()).NotTo(ContainSubstring("PERMISSION"))
		})

		It("treats 'S' (uppercase) the same as 's'", func() {
			ch := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "read",
				Arguments: map[string]interface{}{"file": "/tmp/foo"},
				Response:  ch,
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})

			var decision chat.ToolPermissionDecision
			Eventually(ch).Should(Receive(&decision))
			Expect(decision.Remember).To(BeTrue())
		})
	})

	Describe("approve-once with 'y'", func() {
		It("does NOT cache the tool; second request re-prompts", func() {
			ch := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "ls"},
				Response:  ch,
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			Eventually(ch).Should(Receive(Equal(chat.ToolPermissionDecision{
				Approved: true, Remember: false,
			})))

			// Second request should enter permission mode, not
			// resolve synchronously.
			secondCh := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "pwd"},
				Response:  secondCh,
			})

			// Allow a brief window for any spurious async resolution,
			// then confirm the channel is still empty and the prompt
			// is visible.
			select {
			case got := <-secondCh:
				Fail(fmt.Sprintf("expected permission prompt, but got auto-decision: %+v", got))
			case <-time.After(50 * time.Millisecond):
			}
			Expect(intent.View()).To(ContainSubstring("PERMISSION"))
		})
	})

	Describe("deny with 'n'", func() {
		It("does NOT cache the tool", func() {
			ch := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "ls"},
				Response:  ch,
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
			Eventually(ch).Should(Receive(Equal(chat.ToolPermissionDecision{
				Approved: false, Remember: false,
			})))

			// Follow-up must re-prompt; verify by checking that a
			// ToolPermissionMsg with the same name still enters
			// permission mode.
			secondCh := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "pwd"},
				Response:  secondCh,
			})
			Expect(intent.View()).To(ContainSubstring("PERMISSION"))
		})
	})

	Describe("session switch", func() {
		It("clears the approval cache so 's'-granted approvals do not leak", func() {
			// Grant a remembered approval in session A.
			ch := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "ls"},
				Response:  ch,
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
			Eventually(ch).Should(Receive())

			// Load a different session. handleSessionLoaded resets
			// the view and, critically for P17.S3, wipes the
			// approval cache. We use an empty in-memory context
			// store so no real engine wiring is required.
			emptyStore := recall.NewEmptyContextStore("test-model")
			intent.Update(sessionbrowser.SessionLoadedMsg{
				SessionID: "other-session",
				Store:     emptyStore,
			})

			// A fresh request for the same tool name must prompt
			// again.
			secondCh := make(chan chat.ToolPermissionDecision, 1)
			intent.Update(chat.ToolPermissionMsg{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "pwd"},
				Response:  secondCh,
			})
			select {
			case got := <-secondCh:
				Fail(fmt.Sprintf("expected prompt after session switch, got auto-decision: %+v", got))
			case <-time.After(50 * time.Millisecond):
			}
			Expect(intent.View()).To(ContainSubstring("PERMISSION"))
		})
	})
})
