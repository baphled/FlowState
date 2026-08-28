package engine

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool/todo"
)

var _ = Describe("buildTodoContinuationMessage", func() {
	Context("when all items are pending", func() {
		It("returns the pending-only continuation message with explicit tool call demand", func() {
			items := []todo.Item{
				{Content: "Step one", Status: "pending", Priority: "high"},
				{Content: "Step two", Status: "pending", Priority: "medium"},
			}

			msg := buildTodoContinuationMessage(items)

			Expect(msg.Role).To(Equal("user"))
			Expect(msg.Content).To(ContainSubstring("You have incomplete tasks that still need to be completed"))
			Expect(msg.Content).To(ContainSubstring("- [pending] Step one (high priority)"))
			Expect(msg.Content).To(ContainSubstring("Resume working on these tasks now by calling tools"))
			Expect(msg.Content).To(ContainSubstring("Do NOT respond with prose"))
		})
	})

	Context("when items are in_progress and pending", func() {
		It("returns the active-task continuation message with queued tasks and tool call demand", func() {
			items := []todo.Item{
				{Content: "Active task", Status: "in_progress", Priority: "high"},
				{Content: "Pending task", Status: "pending", Priority: "medium"},
			}

			msg := buildTodoContinuationMessage(items)

			Expect(msg.Role).To(Equal("user"))
			Expect(msg.Content).To(ContainSubstring("You have an active task"))
			Expect(msg.Content).To(ContainSubstring("▶ \"Active task\""))
			Expect(msg.Content).To(ContainSubstring("○ \"Pending task\""))
			Expect(msg.Content).To(ContainSubstring("You must complete or cancel the active task now by calling the appropriate tools"))
			Expect(msg.Content).To(ContainSubstring("Do NOT respond with prose describing what you will do"))
			Expect(msg.Content).NotTo(ContainSubstring("You have incomplete tasks that still need to be completed"))
		})
	})

	Context("when the only item is in_progress", func() {
		It("returns the active-task continuation message without queued tasks but with tool call demand", func() {
			items := []todo.Item{
				{Content: "Active solo task", Status: "in_progress", Priority: "high"},
			}

			msg := buildTodoContinuationMessage(items)

			Expect(msg.Role).To(Equal("user"))
			Expect(msg.Content).To(ContainSubstring("You have an active task"))
			Expect(msg.Content).To(ContainSubstring("▶ \"Active solo task\""))
			Expect(msg.Content).NotTo(ContainSubstring("Additional queued tasks"))
			Expect(msg.Content).To(ContainSubstring("You must complete or cancel the active task now by calling the appropriate tools"))
			Expect(msg.Content).To(ContainSubstring("Do NOT respond with prose"))
		})
	})
})

// The reseed cache specs verify that ReseedFailoverBasePreferences skips
// rebuilding the manifest-head + config-tail chain when the manifest and
// provider/model selection are unchanged, and rebuilds it when they change.
// Behaviour (resulting preferences) must stay identical either way.
var _ = Describe("Engine reseed preference cache", func() {
	var (
		mgr *failover.Manager
		eng *Engine
	)

	BeforeEach(func() {
		reg := provider.NewRegistry()
		mgr = failover.NewManager(reg, failover.NewHealthManager(), 5*time.Minute)
		mgr.SetBasePreferences([]provider.ModelPreference{
			{Provider: "zai", Model: "glm-4.6"},
			{Provider: "openai", Model: "gpt-4o"},
		})
		eng = New(Config{
			Registry:        reg,
			FailoverManager: mgr,
			Manifest:        agent.Manifest{ID: "planner", Name: "Planner"},
		})
	})

	It("skips the rebuild when the manifest and selection are unchanged", func() {
		manifest := agent.Manifest{
			ID: "planner",
			PreferredModels: []agent.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			},
		}

		eng.ReseedFailoverBasePreferences(manifest, "", "")
		first := mgr.Preferences()
		Expect(first).NotTo(BeEmpty())

		eng.ReseedFailoverBasePreferences(manifest, "", "")
		second := mgr.Preferences()

		Expect(eng.ReseedCount()).To(Equal(int64(1)),
			"unchanged manifest + selection must not rebuild the failover chain")
		Expect(second).To(Equal(first),
			"cached skip must leave the chain identical to a fresh rebuild")
	})

	It("rebuilds when the manifest preferred_models change", func() {
		first := agent.Manifest{
			ID: "planner",
			PreferredModels: []agent.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			},
		}
		second := agent.Manifest{
			ID: "planner",
			PreferredModels: []agent.ModelPreference{
				{Provider: "zai", Model: "glm-4.6"},
			},
		}

		eng.ReseedFailoverBasePreferences(first, "", "")
		eng.ReseedFailoverBasePreferences(second, "", "")

		Expect(eng.ReseedCount()).To(Equal(int64(2)))
		Expect(mgr.Preferences()[0]).To(Equal(provider.ModelPreference{Provider: "zai", Model: "glm-4.6"}))
	})

	It("rebuilds when the selected provider or model changes", func() {
		manifest := agent.Manifest{ID: "planner"}

		eng.ReseedFailoverBasePreferences(manifest, "zai", "glm-4.6")
		eng.ReseedFailoverBasePreferences(manifest, "zai", "glm-4.6")
		eng.ReseedFailoverBasePreferences(manifest, "openai", "gpt-4o")

		Expect(eng.ReseedCount()).To(Equal(int64(2)),
			"identical selection is cached; a changed selection rebuilds")
	})
})
