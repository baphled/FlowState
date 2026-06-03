package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/coordination"
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
		// Pin the plan location explicitly so the project-marker walk in
		// ResolvedPlanLocation does NOT find the FlowState worktree's
		// own .flowstate/ when these tests run from inside the repo.
		testApp.Config.PlanLocation = planDir

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
			store, err := plan.NewStore(planDir)
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
			store, err := plan.NewStore(planDir)
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
			store, err := plan.NewStore(planDir)
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

		It("displays multiple tasks with descriptions", func() {
			store, err := plan.NewStore(planDir)
			Expect(err).NotTo(HaveOccurred())

			planFile := plan.File{
				ID:          "multi-task",
				Title:       "Multi Task Plan",
				Description: "",
				Status:      "active",
				CreatedAt:   time.Now(),
				Tasks: []plan.Task{
					{
						Title:       "Task Alpha",
						Description: "Alpha description",
						Status:      "done",
					},
					{
						Title:       "Task Beta",
						Description: "Beta description",
						Status:      "pending",
					},
					{
						Title:       "Task Gamma",
						Description: "",
						Status:      "pending",
					},
				},
			}
			err = store.Create(planFile)
			Expect(err).NotTo(HaveOccurred())

			out.Reset()
			err = planCmd("plan", "select", "multi-task")
			Expect(err).NotTo(HaveOccurred())
			output := out.String()
			Expect(output).To(ContainSubstring("Task Alpha"))
			Expect(output).To(ContainSubstring("Alpha description"))
			Expect(output).To(ContainSubstring("Task Beta"))
			Expect(output).To(ContainSubstring("Beta description"))
			Expect(output).To(ContainSubstring("Task Gamma"))
			Expect(output).To(ContainSubstring("## Tasks"))
		})

		It("displays plan without description", func() {
			store, err := plan.NewStore(planDir)
			Expect(err).NotTo(HaveOccurred())

			planFile := plan.File{
				ID:        "no-desc",
				Title:     "No Description Plan",
				Status:    "draft",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err = store.Create(planFile)
			Expect(err).NotTo(HaveOccurred())

			out.Reset()
			err = planCmd("plan", "select", "no-desc")
			Expect(err).NotTo(HaveOccurred())
			output := out.String()
			Expect(output).To(ContainSubstring("No Description Plan"))
			Expect(output).To(ContainSubstring("draft"))
			Expect(output).NotTo(ContainSubstring("## Tasks"))
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
			store, err := plan.NewStore(planDir)
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

	Context("when publishing a plan (plan publish --chain)", func() {
		var (
			vaultDir   string
			coordPath  string
			publishApp *app.App
			publishCmd func(args ...string) error
			publishOut *bytes.Buffer
			coordStore coordination.Store
		)

		BeforeEach(func() {
			publishOut = &bytes.Buffer{}
			dataDir := GinkgoT().TempDir()
			vaultDir = filepath.Join(GinkgoT().TempDir(), "vault")
			coordPath = filepath.Join(dataDir, "coordination.json")

			var err error
			coordStore, err = coordination.NewFileStore(coordPath)
			Expect(err).NotTo(HaveOccurred())

			publishApp, err = app.NewForTest(app.TestConfig{
				DataDir:       dataDir,
				PlanOutputDir: vaultDir,
			})
			Expect(err).NotTo(HaveOccurred())

			publishCmd = func(args ...string) error {
				root := cli.NewRootCmd(publishApp)
				root.SetOut(publishOut)
				root.SetErr(publishOut)
				root.SetArgs(args)
				return root.Execute()
			}
		})

		It("publishes the named chain's approved plan and prints the path", func() {
			Expect(coordStore.Set("mhc-2026-05-27/plan",
				[]byte(`{"markdown":"# Mental Health Companion\n\nbody","title":"Mental Health Companion"}`))).To(Succeed())
			Expect(coordStore.Set("mhc-2026-05-27/review", []byte(`{"verdict":"approve"}`))).To(Succeed())
			// A stale chain that must NOT be published.
			Expect(coordStore.Set("stale-chain/plan", []byte("# Stale\n\nbody"))).To(Succeed())

			publishOut.Reset()
			err := publishCmd("plan", "publish", "--chain", "mhc-2026-05-27")
			Expect(err).NotTo(HaveOccurred())

			expected := filepath.Join(vaultDir, "Mental Health Companion.md")
			Expect(publishOut.String()).To(ContainSubstring(expected))
			Expect(expected).To(BeAnExistingFile())

			body, readErr := os.ReadFile(expected)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("# Mental Health Companion"))

			entries, _ := os.ReadDir(vaultDir)
			Expect(entries).To(HaveLen(1), "only the named chain is published, not the stale one")
		})

		It("honours an explicit --output-dir override", func() {
			altDir := filepath.Join(GinkgoT().TempDir(), "alt-vault")
			Expect(coordStore.Set("override-chain/plan",
				[]byte("# Override Plan\n\nbody"))).To(Succeed())

			publishOut.Reset()
			err := publishCmd("plan", "publish", "--chain", "override-chain", "--output-dir", altDir)
			Expect(err).NotTo(HaveOccurred())

			expected := filepath.Join(altDir, "Override Plan.md")
			Expect(expected).To(BeAnExistingFile())
			Expect(publishOut.String()).To(ContainSubstring(expected))
		})

		It("refuses a structured-JSON spec blob and prints ONLY the error, no usage block", func() {
			// A JSON spec blob at "<chain>/plan" is NOT a plan document; the
			// publisher refuses it (commit 939a23fa). The refusal is a RUNTIME
			// error, so the output must be the error line ALONE — cobra's
			// Usage/flags dump is noise for a runtime failure and must not
			// appear. SilenceUsage on the publish command enforces this.
			Expect(coordStore.Set("json-chain/plan",
				[]byte(`{"purpose":"Be supportive.","responsibilities":["Listen","Signpost"]}`))).To(Succeed())

			publishOut.Reset()
			err := publishCmd("plan", "publish", "--chain", "json-chain")
			Expect(err).To(HaveOccurred(), "a JSON spec blob must NOT be published")

			out := publishOut.String()
			Expect(out).To(ContainSubstring("not a plan document"),
				"the runtime refusal reason is still printed")
			Expect(out).NotTo(ContainSubstring("Usage:"),
				"a runtime refusal must not dump cobra's usage block")
			Expect(out).NotTo(ContainSubstring("Global Flags:"),
				"a runtime refusal must not dump the global-flags block")
			Expect(out).NotTo(ContainSubstring("--output-dir"),
				"a runtime refusal must not dump the command flags")

			// No garbage reaches the vault on a refused publish.
			entries, _ := os.ReadDir(vaultDir)
			Expect(entries).To(BeEmpty())
		})

		It("still shows usage on a flag/arg-parse error (missing --chain)", func() {
			// SilenceUsage must be scoped to RUNTIME refusals only. A genuine
			// arg-parse error (here: the required --chain omitted) is reported
			// BEFORE RunE runs, so its helpful usage hint must survive. This is
			// the counterpart to the runtime-refusal test above: clean errors
			// for runtime failures, helpful hints for misuse.
			publishOut.Reset()
			err := publishCmd("plan", "publish")
			Expect(err).To(HaveOccurred(), "the required --chain flag is missing")

			out := publishOut.String()
			Expect(out).To(ContainSubstring(`required flag(s) "chain" not set`),
				"the arg-parse error is reported")
			Expect(out).To(ContainSubstring("Usage:"),
				"a flag/arg-parse error keeps cobra's usage hint")
		})

		It("prints a clear no-op message for an unknown chain", func() {
			publishOut.Reset()
			err := publishCmd("plan", "publish", "--chain", "does-not-exist")
			Expect(err).NotTo(HaveOccurred())
			Expect(publishOut.String()).To(ContainSubstring("Nothing published"))
			Expect(publishOut.String()).To(ContainSubstring("does-not-exist"))

			entries, _ := os.ReadDir(vaultDir)
			Expect(entries).To(BeEmpty())
		})

		It("does not publish a chain the reviewer rejected (no-op message)", func() {
			Expect(coordStore.Set("rejected-chain/plan", []byte("# Rejected\n\nbody"))).To(Succeed())
			Expect(coordStore.Set("rejected-chain/review", []byte(`{"verdict":"reject"}`))).To(Succeed())

			publishOut.Reset()
			err := publishCmd("plan", "publish", "--chain", "rejected-chain")
			Expect(err).NotTo(HaveOccurred())
			Expect(publishOut.String()).To(ContainSubstring("Nothing published"))

			entries, _ := os.ReadDir(vaultDir)
			Expect(entries).To(BeEmpty())
		})

		It("reports a no-op when no output dir is resolvable", func() {
			noDirApp, err := app.NewForTest(app.TestConfig{
				DataDir: GinkgoT().TempDir(),
				// no PlanOutputDir
			})
			Expect(err).NotTo(HaveOccurred())

			localOut := &bytes.Buffer{}
			root := cli.NewRootCmd(noDirApp)
			root.SetOut(localOut)
			root.SetErr(localOut)
			root.SetArgs([]string{"plan", "publish", "--chain", "any-chain"})
			Expect(root.Execute()).NotTo(HaveOccurred())
			Expect(localOut.String()).To(ContainSubstring("No plan_output_dir configured"))
		})
	})
})
