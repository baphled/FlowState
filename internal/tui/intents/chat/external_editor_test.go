package chat_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Ctrl+X — external editor flow (P17.S1).
//
// These tests pin the seam between openExternalEditor and the bubbletea
// ExecProcess wrapper. The real tea.ExecProcess dispatches on an internal
// Program state that cannot be driven from a unit test, so the product
// code exposes a package-level editorProcessRunner that the test suite
// replaces with a synchronous fake. The fake simulates the editor writing
// to the temp file (or failing) and then invokes the bubbletea callback
// immediately, producing the same ExternalEditorFinishedMsg shape the
// production path would emit.
var _ = Describe("External editor (Ctrl+X)", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "editor-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("openExternalEditor with a primed input buffer", func() {
		It("seeds the temp file with the current input and replaces the buffer on exit", func() {
			intent.SetInputForTest("original draft")

			var capturedTempPath string
			var observedInitial string
			restore := chat.SetEditorProcessRunnerForTest(func(
				cmd *exec.Cmd, cb tea.ExecCallback,
			) tea.Cmd {
				// Final arg on the exec.Cmd is the temp file path.
				capturedTempPath = cmd.Args[len(cmd.Args)-1]
				data, _ := os.ReadFile(capturedTempPath)
				observedInitial = string(data)
				// Simulate the editor rewriting the file.
				_ = os.WriteFile(capturedTempPath, []byte("edited content"), 0o600)
				return func() tea.Msg { return cb(nil) }
			})
			defer restore()

			// S1 uses a dedicated $EDITOR shim so the production fallback
			// to the host's vim binary never fires during tests.
			GinkgoT().Setenv("EDITOR", "/bin/true")

			cmd := intent.OpenExternalEditorForTest()
			Expect(cmd).NotTo(BeNil(), "expected a tea.Cmd driving the editor")

			msg, ok := cmd().(chat.ExternalEditorFinishedMsg)
			Expect(ok).To(BeTrue(), "expected ExternalEditorFinishedMsg")
			Expect(msg.Err).NotTo(HaveOccurred())
			Expect(msg.Content).To(Equal("edited content"))
			Expect(observedInitial).To(Equal("original draft"),
				"expected the editor to see the seeded draft")

			intent.HandleExternalEditorFinishedForTest(msg)
			Expect(intent.Input()).To(Equal("edited content"))

			_, statErr := os.Stat(capturedTempPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(),
				"expected temp file to be deleted after consumption")
		})
	})

	Describe("openExternalEditor with an empty input buffer", func() {
		It("still opens the editor and accepts whatever is typed", func() {
			intent.SetInputForTest("")

			restore := chat.SetEditorProcessRunnerForTest(func(
				cmd *exec.Cmd, cb tea.ExecCallback,
			) tea.Cmd {
				path := cmd.Args[len(cmd.Args)-1]
				_ = os.WriteFile(path, []byte("typed from nothing"), 0o600)
				return func() tea.Msg { return cb(nil) }
			})
			defer restore()
			GinkgoT().Setenv("EDITOR", "/bin/true")

			cmd := intent.OpenExternalEditorForTest()
			Expect(cmd).NotTo(BeNil())
			msg := cmd().(chat.ExternalEditorFinishedMsg)
			Expect(msg.Err).NotTo(HaveOccurred())

			intent.HandleExternalEditorFinishedForTest(msg)
			Expect(intent.Input()).To(Equal("typed from nothing"))
		})
	})

	Describe("openExternalEditor when no editor is available", func() {
		It("surfaces a notification and returns a nil Cmd instead of crashing", func() {
			// Clear $EDITOR and point the fallback vim LookPath at a
			// known-missing directory so externalEditorBinary definitely
			// returns "".
			GinkgoT().Setenv("EDITOR", "")
			GinkgoT().Setenv("PATH", "/var/empty")

			cmd := intent.OpenExternalEditorForTest()
			Expect(cmd).To(BeNil(), "expected nil Cmd when no editor is resolvable")

			mgr := intent.NotificationManagerForTest()
			Expect(mgr).NotTo(BeNil())
			var titles []string
			for _, n := range mgr.Active() {
				titles = append(titles, n.Title)
			}
			Expect(strings.Join(titles, "|")).To(ContainSubstring("External editor unavailable"))
		})
	})

	Describe("handleExternalEditorFinished with a runtime error", func() {
		It("keeps the input buffer untouched and surfaces an error notification", func() {
			intent.SetInputForTest("keep me")

			tempFile, err := os.CreateTemp("", "flowstate-editor-err-*.txt")
			Expect(err).NotTo(HaveOccurred())
			_ = tempFile.Close()
			defer func() { _ = os.Remove(tempFile.Name()) }()

			msg := chat.ExternalEditorFinishedMsg{
				TempPath: tempFile.Name(),
				Err:      errors.New("editor blew up"),
			}
			intent.HandleExternalEditorFinishedForTest(msg)

			Expect(intent.Input()).To(Equal("keep me"),
				"expected input buffer to remain unchanged on editor error")
			_, statErr := os.Stat(tempFile.Name())
			Expect(os.IsNotExist(statErr)).To(BeTrue(),
				"expected handler to remove the temp file even on error")

			mgr := intent.NotificationManagerForTest()
			var titles []string
			for _, n := range mgr.Active() {
				titles = append(titles, n.Title)
			}
			Expect(strings.Join(titles, "|")).To(ContainSubstring("External editor failed"))
		})
	})
})
