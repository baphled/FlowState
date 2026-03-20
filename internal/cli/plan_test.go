package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/plan"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan Command", func() {
	var (
		out     *bytes.Buffer
		testApp *app.App
		planDir string
		planCmd func(args ...string) error
	)

	BeforeEach(func() {
		out = &bytes.Buffer{}
		planDir = filepath.Join(GinkgoT().TempDir(), "plans")
		tc := app.TestConfig{
			AgentsDir: "",
			SkillsDir: "",
		}
		var err error
		testApp, err = app.NewForTest(tc)
		Expect(err).NotTo(HaveOccurred())

		testApp.Config.DataDir = filepath.Dir(planDir)

		err = os.MkdirAll(planDir, 0o755)
		Expect(err).NotTo(HaveOccurred())

		planCmd = func(args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(args)
			return root.Execute()
		}
	})

	Context("when listing plans", func() {
		It("prints a message when no plans exist", func() {
			out.Reset()
			err := planCmd("plan", "list")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No plans yet"))
		})

		It("prints table headers when plans exist", func() {
			store, err := plan.NewPlanStore(planDir)
			Expect(err).NotTo(HaveOccurred())

			err = store.Create(plan.File{
				ID:        "test-plan-1",
				Title:     "Test Plan",
				Status:    "pending",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			})
			Expect(err).NotTo(HaveOccurred())

			out.Reset()
			err = planCmd("plan", "list")
			Expect(err).NotTo(HaveOccurred())
			output := out.String()
			Expect(output).To(ContainSubstring("ID"))
			Expect(output).To(ContainSubstring("Title"))
			Expect(output).To(ContainSubstring("Status"))
		})

		It("lists all plan summaries", func() {
			store, err := plan.NewPlanStore(planDir)
			Expect(err).NotTo(HaveOccurred())

			for i, title := range []string{"Plan One", "Plan Two"} {
				id := "plan-" + string(rune('a'+i))
				err = store.Create(plan.File{
					ID:        id,
					Title:     title,
					Status:    "active",
					CreatedAt: time.Now(),
					Tasks:     []plan.Task{},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			out.Reset()
			err = planCmd("plan", "list")
			Expect(err).NotTo(HaveOccurred())
			output := out.String()
			Expect(output).To(ContainSubstring("Plan One"))
			Expect(output).To(ContainSubstring("Plan Two"))
		})
	})

	Context("when selecting a plan", func() {
		It("returns error when plan ID is missing", func() {
			out.Reset()
			err := planCmd("plan", "select")
			Expect(err).To(HaveOccurred())
		})

		It("returns error when plan does not exist", func() {
			out.Reset()
			err := planCmd("plan", "select", "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("plan not found"))
		})

		It("displays full plan content", func() {
			store, err := plan.NewPlanStore(planDir)
			Expect(err).NotTo(HaveOccurred())

			planFile := plan.File{
				ID:          "test-plan",
				Title:       "Integration Test",
				Description: "A test plan for selection",
				Status:      "in_progress",
				CreatedAt:   time.Now(),
				Tasks: []plan.Task{
					{
						Title:       "First Task",
						Description: "Task description",
						Status:      "pending",
					},
				},
			}
			err = store.Create(planFile)
			Expect(err).NotTo(HaveOccurred())

			out.Reset()
			err = planCmd("plan", "select", "test-plan")
			Expect(err).NotTo(HaveOccurred())
			output := out.String()
			Expect(output).To(ContainSubstring("Integration Test"))
			Expect(output).To(ContainSubstring("A test plan for selection"))
			Expect(output).To(ContainSubstring("in_progress"))
		})
	})

	Context("when deleting a plan", func() {
		It("returns error when plan ID is missing", func() {
			out.Reset()
			err := planCmd("plan", "delete")
			Expect(err).To(HaveOccurred())
		})

		It("returns error when plan does not exist", func() {
			out.Reset()
			err := planCmd("plan", "delete", "nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("deletes plan and prints confirmation", func() {
			store, err := plan.NewPlanStore(planDir)
			Expect(err).NotTo(HaveOccurred())

			err = store.Create(plan.File{
				ID:        "delete-me",
				Title:     "Temporary Plan",
				Status:    "draft",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			})
			Expect(err).NotTo(HaveOccurred())

			out.Reset()
			err = planCmd("plan", "delete", "delete-me")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("deleted"))

			summaries, err := store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(BeEmpty())
		})
	})
})
