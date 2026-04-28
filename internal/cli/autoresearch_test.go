package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin the surface and behaviour contract of
// `flowstate autoresearch run` — the MVP loop spine landed across
// Slices 1a–1d of the autoresearch plan v3.1. The shape mirrors
// coordination_test.go's seam-spec convention: drive the cobra root
// with cli.NewRootCmd and assert against the captured output and the
// coord-store side effects.
//
// The tests construct a real git repository under a tempdir so the
// worktree-add path exercises `git worktree`. Tests that exercise the
// trial loop use the deterministic fixture driver under
// internal/cli/testdata/fake-scorer.sh — no provider, no network, no
// host config dependencies.
var _ = Describe("autoresearch run command", func() {
	var (
		out       *bytes.Buffer
		testApp   *app.App
		dataDir   string
		repoDir   string
		agentsDir string
		surface   string
		coordPath string
		runCmd    func(args ...string) error
	)

	// initRepo creates a temp git repo with a single committed manifest
	// at internal/app/agents/planner.md. The harness's hard-coded MVP
	// surface is `internal/app/agents/planner.md` per § 5.5; the test
	// repo mirrors that layout so the surface path validation is real.
	initRepo := func(repo, manifestBody string) {
		Expect(os.MkdirAll(filepath.Join(repo, "internal", "app", "agents"), 0o755)).To(Succeed())
		manifestPath := filepath.Join(repo, "internal", "app", "agents", "planner.md")
		Expect(os.WriteFile(manifestPath, []byte(manifestBody), 0o600)).To(Succeed())

		run := func(args ...string) {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			// Quieten git's identity warnings inside the test.
			cmd.Env = append(os.Environ(),
				"GIT_AUTHOR_NAME=test",
				"GIT_AUTHOR_EMAIL=test@example.com",
				"GIT_COMMITTER_NAME=test",
				"GIT_COMMITTER_EMAIL=test@example.com",
			)
			combined, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(combined))
		}
		run("init", "--initial-branch=main", repo)
		run("config", "user.email", "test@example.com")
		run("config", "user.name", "test")
		run("add", ".")
		run("commit", "--no-verify", "-m", "initial")
	}

	defaultManifest := `---
schema_version: "1"
id: planner
name: Planner
complexity: standard
metadata:
  role: planner role
capabilities:
  tools: [read, plan]
---
planner body
`

	BeforeEach(func() {
		out = &bytes.Buffer{}
		dataDir = GinkgoT().TempDir()
		repoDir = GinkgoT().TempDir()
		coordPath = filepath.Join(dataDir, "coordination.json")

		initRepo(repoDir, defaultManifest)
		agentsDir = filepath.Join(repoDir, "internal", "app", "agents")
		surface = filepath.Join(agentsDir, "planner.md")

		var err error
		testApp, err = app.NewForTest(app.TestConfig{
			DataDir:   dataDir,
			AgentsDir: agentsDir,
		})
		Expect(err).NotTo(HaveOccurred())

		runCmd = func(args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(args)
			return root.Execute()
		}
	})

	readManifestRecord := func(runID string) map[string]any {
		raw, err := os.ReadFile(coordPath)
		Expect(err).NotTo(HaveOccurred(), "coord-store file should exist")

		var entries map[string]string
		Expect(json.Unmarshal(raw, &entries)).To(Succeed())

		key := "autoresearch/" + runID + "/manifest"
		val, ok := entries[key]
		Expect(ok).To(BeTrue(), "manifest record should exist at %s", key)

		var record map[string]any
		Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())
		return record
	}

	Describe("command discovery and help", func() {
		It("registers the autoresearch group under the root command", func() {
			err := runCmd("autoresearch")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("autoresearch"))
			Expect(output).To(ContainSubstring("run"))
		})

		It("registers the run subcommand with the documented flags", func() {
			err := runCmd("autoresearch", "run", "--help")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("--surface"))
			Expect(output).To(ContainSubstring("--max-trials"))
			Expect(output).To(ContainSubstring("--metric-direction"))
			Expect(output).To(ContainSubstring("--time-budget"))
			Expect(output).To(ContainSubstring("--run-id"))
			Expect(output).To(ContainSubstring("--worktree-base"))
			Expect(output).To(ContainSubstring("--no-improve-window"))
		})
	})

	Describe("flag validation", func() {
		It("rejects --metric-direction values other than min or max", func() {
			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--metric-direction", "sideways",
				"--max-trials", "1",
				"--worktree-base", filepath.Join(dataDir, "wt"),
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metric-direction"))
		})

		It("requires --surface to point at an existing file", func() {
			err := runCmd("autoresearch", "run",
				"--surface", filepath.Join(repoDir, "does", "not", "exist.md"),
				"--max-trials", "1",
				"--worktree-base", filepath.Join(dataDir, "wt"),
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("surface"))
		})
	})

	Describe("run lifecycle", func() {
		// driverScript writes a fixture driver script under the
		// configured testdata path. It must be executable. The MVP
		// loop's per-trial driver is a fixture script in 1b for
		// testing — a real run wires the calling agent's edit hook in
		// a later slice (§ 5.5). Slice 1c exercises the loop; Slice
		// 1b only checks that --max-trials 0 (or a no-op driver) lets
		// the run set up cleanly and write the manifest record.
		writeNoOpDriver := func() string {
			path := filepath.Join(dataDir, "noop-driver.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755)).To(Succeed())
			return path
		}

		writeNoOpScorer := func() string {
			path := filepath.Join(dataDir, "noop-scorer.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\necho 0\n"), 0o755)).To(Succeed())
			return path
		}

		It("writes the manifest record under autoresearch/<runID>/manifest", func() {
			driver := writeNoOpDriver()
			scorer := writeNoOpScorer()

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "fixture-run-1b",
				"--max-trials", "0",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--worktree-base", filepath.Join(dataDir, "wt"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			record := readManifestRecord("fixture-run-1b")
			Expect(record).To(HaveKeyWithValue("surface", surface))
			Expect(record).To(HaveKeyWithValue("metric_direction", "min"))
			Expect(record).To(HaveKey("max_trials"))
			Expect(record).To(HaveKey("time_budget"))
			Expect(record).To(HaveKey("started_at"))
			Expect(record).To(HaveKey("worktree_path"))
		})

		It("generates a run-id when --run-id is not provided", func() {
			driver := writeNoOpDriver()
			scorer := writeNoOpScorer()

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--max-trials", "0",
				"--time-budget", "30s",
				"--worktree-base", filepath.Join(dataDir, "wt-gen"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			// Coord-store should now contain exactly one autoresearch
			// manifest record under a generated id.
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())

			var found []string
			for k := range entries {
				if strings.HasPrefix(k, "autoresearch/") && strings.HasSuffix(k, "/manifest") {
					found = append(found, k)
				}
			}
			Expect(found).To(HaveLen(1), "exactly one generated manifest record expected: %v", found)
			// Generated id must be non-empty.
			parts := strings.Split(found[0], "/")
			Expect(parts).To(HaveLen(3))
			Expect(parts[1]).NotTo(BeEmpty())
		})

		It("creates a worktree under <worktree-base>/<runID>/worktree", func() {
			driver := writeNoOpDriver()
			scorer := writeNoOpScorer()

			worktreeBase := filepath.Join(dataDir, "wt-create")
			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "fixture-wt",
				"--max-trials", "0",
				"--time-budget", "30s",
				"--worktree-base", worktreeBase,
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			record := readManifestRecord("fixture-wt")
			worktreePath, _ := record["worktree_path"].(string)
			Expect(worktreePath).NotTo(BeEmpty())
			info, statErr := os.Stat(worktreePath)
			Expect(statErr).NotTo(HaveOccurred(), "worktree directory should exist at %s", worktreePath)
			Expect(info.IsDir()).To(BeTrue())
		})

		It("rejects the run when the parent working tree is dirty", func() {
			// Dirty the parent repo before running.
			Expect(os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("uncommitted"), 0o600)).To(Succeed())

			driver := writeNoOpDriver()
			scorer := writeNoOpScorer()

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "should-not-start",
				"--max-trials", "0",
				"--time-budget", "30s",
				"--worktree-base", filepath.Join(dataDir, "wt-dirty"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("dirty"))

			// No manifest record should have been written.
			if raw, readErr := os.ReadFile(coordPath); readErr == nil {
				var entries map[string]string
				Expect(json.Unmarshal(raw, &entries)).To(Succeed())
				_, exists := entries["autoresearch/should-not-start/manifest"]
				Expect(exists).To(BeFalse())
			}
		})
	})
})
