package tui_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tui"
	"github.com/baphled/flowstate/internal/tui/intents/agentpicker"
)

var _ = Describe("BuildStartIntent", func() {
	var registry *agent.Registry

	BeforeEach(func() {
		registry = agent.NewRegistry()
		registry.Register(&agent.Manifest{ID: "planner", Name: "Planner"})
		registry.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
	})

	Context("when agentID is empty", func() {
		It("returns an AgentPickerIntent", func() {
			intent := tui.BuildStartIntent("", registry)
			Expect(intent).NotTo(BeNil())
			_, ok := intent.(*agentpicker.Intent)
			Expect(ok).To(BeTrue())
		})

		It("includes all registry agents in the picker", func() {
			intent := tui.BuildStartIntent("", registry)
			view := intent.View()
			Expect(view).To(ContainSubstring("Planner"))
			Expect(view).To(ContainSubstring("Executor"))
		})
	})

	Context("when agentID is not empty", func() {
		It("returns nil so Run uses the chat intent directly", func() {
			result := tui.BuildStartIntent("planner", registry)
			Expect(result).To(BeNil())
		})
	})

	Context("when registry is nil and agentID is empty", func() {
		It("returns an AgentPickerIntent with an empty list", func() {
			intent := tui.BuildStartIntent("", nil)
			Expect(intent).NotTo(BeNil())
			_, ok := intent.(*agentpicker.Intent)
			Expect(ok).To(BeTrue())
		})
	})
})

// Deferral 2 of commit 32ba71d: root sessions created by the TUI entry
// point must drop a <sessionID>.meta.json sidecar so the hierarchy
// survives app restarts. The Bubble Tea program inside tui.Run is not
// trivially testable (p.Run blocks on a real terminal), so the sidecar
// write is extracted into a pure helper that Run calls on its happy
// path. This suite pins the helper's contract; the Run-level wiring is
// a one-line call covered at the seam.
var _ = Describe("PersistRootSessionMetadata", func() {
	Context("when sessionsDir is configured", func() {
		It("writes a .meta.json sidecar with id, agent, status and empty parent", func() {
			sessionsDir := GinkgoT().TempDir()

			tui.PersistRootSessionMetadata(sessionsDir, "root-tui-sidecar", "tui-agent")

			metaPath := filepath.Join(sessionsDir, "root-tui-sidecar.meta.json")
			Expect(metaPath).To(BeAnExistingFile())

			data, err := os.ReadFile(metaPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"id":"root-tui-sidecar"`))
			Expect(string(data)).To(ContainSubstring(`"agent_id":"tui-agent"`))
			Expect(string(data)).To(ContainSubstring(`"status":"active"`))
			Expect(string(data)).To(ContainSubstring(`"parent_id":""`))
		})
	})

	Context("when sessionsDir is empty", func() {
		It("is a silent no-op (matches engine-side convention)", func() {
			// No panic, no error returned, nothing to assert on disk.
			// The contract is: empty sessionsDir disables persistence,
			// same as DelegateTool.persistSessionMetadata.
			Expect(func() {
				tui.PersistRootSessionMetadata("", "should-not-write", "some-agent")
			}).NotTo(Panic())
		})
	})

	Context("when sessionID is empty", func() {
		It("is a silent no-op — nothing to identify the file by", func() {
			sessionsDir := GinkgoT().TempDir()

			tui.PersistRootSessionMetadata(sessionsDir, "", "some-agent")

			entries, err := os.ReadDir(sessionsDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})
	})
})
