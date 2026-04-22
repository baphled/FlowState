package cli_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agents refresh command", func() {
	var (
		out       *bytes.Buffer
		testApp   *app.App
		agentsDir string
		runCmd    func(args ...string) error
	)

	BeforeEach(func() {
		out = &bytes.Buffer{}
		agentsDir = filepath.Join(GinkgoT().TempDir(), "agents")
		Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())

		// Seed the destination from the binary's embedded manifests so the
		// baseline matches what a real user would have after first install.
		Expect(app.SeedAgentsDir(app.EmbeddedAgentsFS(), agentsDir)).To(Succeed())

		var err error
		testApp, err = app.NewForTest(app.TestConfig{AgentsDir: agentsDir})
		Expect(err).NotTo(HaveOccurred())

		runCmd = func(args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(append([]string{"--agents-dir", agentsDir}, args...))
			return root.Execute()
		}
	})

	Context("when a manifest on disk has drifted from the embedded version", func() {
		BeforeEach(func() {
			plannerPath := filepath.Join(agentsDir, "planner.md")
			stale := "---\nid: planner\nname: Stale Planner\n---\nstale body\n"
			Expect(os.WriteFile(plannerPath, []byte(stale), 0o600)).To(Succeed())
		})

		It("--dry-run reports the change without writing", func() {
			out.Reset()
			err := runCmd("agents", "refresh", "--dry-run")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("planner.md"))
			Expect(output).To(ContainSubstring("updated"))

			content, err := os.ReadFile(filepath.Join(agentsDir, "planner.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("Stale Planner"), "dry-run must not write")
		})

		It("overwrites the stale file on a real run", func() {
			out.Reset()
			err := runCmd("agents", "refresh")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("planner.md"))
			Expect(output).To(ContainSubstring("updated"))

			content, err := os.ReadFile(filepath.Join(agentsDir, "planner.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).NotTo(ContainSubstring("Stale Planner"))

			embeddedBytes, err := fs.ReadFile(app.EmbeddedAgentsFS(), "agents/planner.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(content).To(Equal(embeddedBytes), "file must match embedded byte-for-byte")
		})
	})

	Context("when the manifest matches the embedded version", func() {
		It("reports it as unchanged and exits cleanly", func() {
			out.Reset()
			err := runCmd("agents", "refresh")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("unchanged"))
		})
	})

	Context("when --agent filter is set", func() {
		It("only refreshes the named manifest", func() {
			plannerPath := filepath.Join(agentsDir, "planner.md")
			executorPath := filepath.Join(agentsDir, "executor.md")

			stalePlanner := "---\nid: planner\nname: Stale Planner\n---\n"
			staleExecutor := "---\nid: executor\nname: Stale Executor\n---\n"
			Expect(os.WriteFile(plannerPath, []byte(stalePlanner), 0o600)).To(Succeed())
			Expect(os.WriteFile(executorPath, []byte(staleExecutor), 0o600)).To(Succeed())

			out.Reset()
			err := runCmd("agents", "refresh", "--agent", "planner")
			Expect(err).NotTo(HaveOccurred())

			plannerContent, err := os.ReadFile(plannerPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(plannerContent)).NotTo(ContainSubstring("Stale Planner"))

			executorContent, err := os.ReadFile(executorPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(executorContent)).To(ContainSubstring("Stale Executor"),
				"--agent filter must not touch other manifests")

			Expect(out.String()).NotTo(ContainSubstring("executor.md"))
		})

		It("returns a non-zero error when the agent is unknown", func() {
			out.Reset()
			err := runCmd("agents", "refresh", "--agent", "no-such-agent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no-such-agent"))
		})
	})

	Context("when --verbose is set", func() {
		It("includes size deltas in the report", func() {
			plannerPath := filepath.Join(agentsDir, "planner.md")
			stale := "---\nid: planner\nname: Stale\n---\n"
			Expect(os.WriteFile(plannerPath, []byte(stale), 0o600)).To(Succeed())

			out.Reset()
			err := runCmd("agents", "refresh", "--verbose")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("planner.md"))
			Expect(output).To(MatchRegexp(`\d+\s*->\s*\d+|bytes`))
		})
	})
})
