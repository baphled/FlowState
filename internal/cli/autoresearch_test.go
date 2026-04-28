package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

	// initRepo creates a temp git repo with a committed planner manifest
	// at internal/app/agents/planner.md (the harness's hard-coded MVP
	// surface per § 5.5) and a committed default `autoresearch` skill at
	// skills/autoresearch/SKILL.md. The skill stub is the resolution
	// target for the default `--program autoresearch` flag (Slice 6) —
	// without it the skill-name resolver fails before the run starts.
	initRepo := func(repo, manifestBody string) {
		Expect(os.MkdirAll(filepath.Join(repo, "internal", "app", "agents"), 0o755)).To(Succeed())
		manifestPath := filepath.Join(repo, "internal", "app", "agents", "planner.md")
		Expect(os.WriteFile(manifestPath, []byte(manifestBody), 0o600)).To(Succeed())

		Expect(os.MkdirAll(filepath.Join(repo, "skills", "autoresearch"), 0o755)).To(Succeed())
		skillPath := filepath.Join(repo, "skills", "autoresearch", "SKILL.md")
		Expect(os.WriteFile(skillPath, []byte("default autoresearch skill body\n"), 0o600)).To(Succeed())

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
			Expect(output).To(ContainSubstring("--program"))
			Expect(output).To(ContainSubstring("--calling-agent"))
			// Live-driver Slice 1 flags.
			Expect(output).To(ContainSubstring("--driver-timeout"))
			Expect(output).To(ContainSubstring("--driver-max-turns"))
			Expect(output).To(ContainSubstring("--prompt-history-window"))
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

		It("rejects the run when the surface path does not exist", func() {
			err := runCmd("autoresearch", "run",
				"--surface", filepath.Join(repoDir, "internal", "app", "agents", "ghost.md"),
				"--max-trials", "1",
				"--worktree-base", filepath.Join(dataDir, "wt-ghost"),
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("surface"))
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

		// Lifecycle plan Slice 1 — named-branch worktrees. Every run's
		// worktree is created on a real branch named
		// `autoresearch/<run-id-short>` so kept commits remain reachable
		// as a branch ref after the worktree is removed (Slice 2). The
		// run-id prefix is the first 8 characters; this matches the
		// `.claude/worktrees/agent-<8hex>/` convention.
		Context("when the trial worktree is created on a named branch", func() {
			It("checks the worktree out on autoresearch/<run-id-short>, not detached HEAD", func() {
				driver := writeNoOpDriver()
				scorer := writeNoOpScorer()

				// run-id is chosen so the first 8 chars form a stable
				// readable suffix the assertions below pin verbatim.
				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "fixrunaa-rest-of-id",
					"--max-trials", "0",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-branch"),
					"--driver-script", driver,
					"--evaluator-script", scorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				record := readManifestRecord("fixrunaa-rest-of-id")
				worktreePath, _ := record["worktree_path"].(string)
				Expect(worktreePath).NotTo(BeEmpty())

				// HEAD inside the worktree must resolve to the named
				// branch, not the literal "HEAD" symbolic ref produced
				// by --detach.
				headCmd := exec.Command("git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
				headOut, headErr := headCmd.CombinedOutput()
				Expect(headErr).NotTo(HaveOccurred(), "git rev-parse: %s", string(headOut))
				Expect(strings.TrimSpace(string(headOut))).To(Equal("autoresearch/fixrunaa"))

				// And the branch must exist as a parent-repo branch ref.
				branchCmd := exec.Command("git", "-C", repoDir, "branch", "--list", "autoresearch/fixrunaa")
				branchOut, branchErr := branchCmd.CombinedOutput()
				Expect(branchErr).NotTo(HaveOccurred(), "git branch --list: %s", string(branchOut))
				Expect(strings.TrimSpace(string(branchOut))).NotTo(BeEmpty(),
					"branch autoresearch/fixrunaa should exist in the parent repo")
			})

			It("uses the first 8 characters of the run-id as the branch suffix", func() {
				driver := writeNoOpDriver()
				scorer := writeNoOpScorer()

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "deadbeefcafef00d-extra-tail",
					"--max-trials", "0",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-prefix"),
					"--driver-script", driver,
					"--evaluator-script", scorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				branchCmd := exec.Command("git", "-C", repoDir, "branch", "--list", "autoresearch/deadbeef")
				branchOut, branchErr := branchCmd.CombinedOutput()
				Expect(branchErr).NotTo(HaveOccurred(), "git branch --list: %s", string(branchOut))
				Expect(strings.TrimSpace(string(branchOut))).NotTo(BeEmpty(),
					"branch autoresearch/deadbeef should exist (8-char prefix of run-id)")
			})

			It("fails when the branch already exists (collision on rerun with same run-id)", func() {
				driver := writeNoOpDriver()
				scorer := writeNoOpScorer()

				args := []string{
					"autoresearch", "run",
					"--surface", surface,
					"--run-id", "clashfix-rest-of-id",
					"--max-trials", "0",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-collide"),
					"--driver-script", driver,
					"--evaluator-script", scorer,
				}

				// First run succeeds and creates autoresearch/clashfix.
				Expect(runCmd(args...)).To(Succeed(), "first run should succeed; out: %s", out.String())

				// Second run with the same run-id targets a different
				// worktree path; the branch collision is what must
				// fail. Use a distinct base so the worktree path itself
				// is fresh.
				out.Reset()
				args2 := append([]string{}, args...)
				for i, a := range args2 {
					if a == "--worktree-base" {
						args2[i+1] = filepath.Join(dataDir, "wt-collide-2")
					}
				}
				err := runCmd(args2...)
				Expect(err).To(HaveOccurred(), "second run should fail on branch collision")
				Expect(err.Error()).To(ContainSubstring("autoresearch/clashfix"))
			})
		})

		// Lifecycle plan Slice 3 — `--allow-dirty` stashes the parent's
		// uncommitted state at run start and restores it on exit so
		// the operator can run the harness against an in-progress
		// edit without forcing a commit. Without the flag the
		// pre-existing dirty-tree refusal still applies.
		Context("when the parent working tree is dirty and --allow-dirty toggles the precondition", func() {
			dirtyParent := func() {
				Expect(os.WriteFile(filepath.Join(repoDir, "dirty.txt"),
					[]byte("uncommitted-by-operator"), 0o600)).To(Succeed())
			}
			parentWorkingState := func() string {
				path := filepath.Join(repoDir, "dirty.txt")
				body, err := os.ReadFile(path)
				if errors.Is(err, os.ErrNotExist) {
					return ""
				}
				Expect(err).NotTo(HaveOccurred())
				return string(body)
			}

			It("is a no-op on a clean tree (--allow-dirty does not stash)", func() {
				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "alldircl-rest-of-id",
					"--max-trials", "0",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-allow-clean"),
					"--driver-script", writeNoOpDriver(),
					"--evaluator-script", writeNoOpScorer(),
					"--allow-dirty",
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				Expect(out.String()).NotTo(ContainSubstring("stashed parent state"),
					"clean tree must not trigger stash flow")
			})

			It("refuses a dirty tree when --allow-dirty is not set", func() {
				dirtyParent()

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "alldirno-rest-of-id",
					"--max-trials", "0",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-allow-refused"),
					"--driver-script", writeNoOpDriver(),
					"--evaluator-script", writeNoOpScorer(),
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("dirty"))
				Expect(err.Error()).To(ContainSubstring("--allow-dirty"))

				// Operator's edit must remain intact.
				Expect(parentWorkingState()).To(Equal("uncommitted-by-operator"))
			})

			It("stashes the dirty tree, runs the loop, restores the stash on clean exit", func() {
				dirtyParent()

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "alldiryes-rest-of-id",
					"--max-trials", "1",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-allow-yes"),
					"--driver-script", writeNoOpDriver(),
					"--evaluator-script", writeNoOpScorer(),
					"--allow-dirty",
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				Expect(out.String()).To(ContainSubstring("stashed parent state"))
				Expect(out.String()).To(ContainSubstring("stash"))
				Expect(out.String()).To(ContainSubstring("restored"))

				// Operator's edit must be back in place after the run.
				Expect(parentWorkingState()).To(Equal("uncommitted-by-operator"))

				// Manifest record carries the audit annotation.
				record := readManifestRecord("alldiryes-rest-of-id")
				Expect(record).To(HaveKeyWithValue("allow_dirty", true))

				// No leftover harness-tagged stash entry.
				stashList := exec.Command("git", "-C", repoDir, "stash", "list")
				stashOut, _ := stashList.CombinedOutput()
				Expect(string(stashOut)).NotTo(ContainSubstring("flowstate-autoresearch-allow-dirty"))
			})

			It("restores the parent's stash on a non-clean (evaluator-contract-failure-rate) termination", func() {
				// An evaluator that always violates the contract drives
				// the loop to terminationEvaluatorContractFailure after
				// three strikes — a non-clean termination. The
				// harness-managed stash must still be restored on this
				// exit path so the operator's edit survives.
				badEvaluator := filepath.Join(dataDir, "bad-evaluator.sh")
				Expect(os.WriteFile(badEvaluator,
					[]byte("#!/usr/bin/env bash\necho not-an-integer\nexit 0\n"), 0o755)).To(Succeed())

				dirtyParent()

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "alldirerr-rest-of-id",
					"--max-trials", "10",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-allow-err"),
					"--driver-script", writeNoOpDriver(),
					"--evaluator-script", badEvaluator,
					"--allow-dirty",
				)
				// The baseline evaluator runs first and fails the
				// contract; the harness aborts before the loop. The
				// defer-shaped stash restore must still fire.
				Expect(err).To(HaveOccurred(),
					"baseline evaluator contract violation surfaces as harness error")

				// Operator's edit must still be restored despite the error.
				Expect(parentWorkingState()).To(Equal("uncommitted-by-operator"))
				stashList := exec.Command("git", "-C", repoDir, "stash", "list")
				stashOut, _ := stashList.CombinedOutput()
				Expect(string(stashOut)).NotTo(ContainSubstring("flowstate-autoresearch-allow-dirty"))
			})
		})

		// Lifecycle plan Slice 2 — auto-prune cleanup. After
		// printRunSummary the harness removes the worktree on a clean
		// termination; the branch is always preserved as the durable
		// kept-commit anchor. Four states per the plan's § Auto-prune
		// contract:
		//   1. Clean termination → worktree removed, branch preserved.
		//   2. --keep-worktree set → worktree preserved on clean exit.
		//   3. Signal termination → worktree preserved regardless.
		//   4. Cleanup failure → run still succeeds, branch preserved.
		Context("when the run terminates and the auto-prune contract fires", func() {
			// runMaxTrialsOne drives a one-trial run with the no-op
			// fixtures. The fixed-point-skipped trial 1 + maxTrials=1
			// produces terminationReason=max-trials (a clean exit).
			runMaxTrialsOne := func(runID, worktreeBase string, extra ...string) error {
				args := []string{
					"autoresearch", "run",
					"--surface", surface,
					"--run-id", runID,
					"--max-trials", "1",
					"--time-budget", "30s",
					"--worktree-base", worktreeBase,
					"--driver-script", writeNoOpDriver(),
					"--evaluator-script", writeNoOpScorer(),
				}
				args = append(args, extra...)
				return runCmd(args...)
			}

			It("removes the worktree and preserves the branch on a clean termination", func() {
				worktreeBase := filepath.Join(dataDir, "wt-clean")
				err := runMaxTrialsOne("cleanrun-rest-of-id", worktreeBase)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				// Worktree directory must be gone.
				worktreePath := filepath.Join(worktreeBase, "cleanrun-rest-of-id", "worktree")
				_, statErr := os.Stat(worktreePath)
				Expect(os.IsNotExist(statErr)).To(BeTrue(),
					"worktree should be removed; got stat err: %v\nout: %s", statErr, out.String())

				// `git worktree list` must not mention the path.
				listCmd := exec.Command("git", "-C", repoDir, "worktree", "list")
				listOut, listErr := listCmd.CombinedOutput()
				Expect(listErr).NotTo(HaveOccurred(), "git worktree list: %s", string(listOut))
				Expect(string(listOut)).NotTo(ContainSubstring(worktreePath))

				// Branch must remain.
				branchCmd := exec.Command("git", "-C", repoDir, "branch", "--list", "autoresearch/cleanrun")
				branchOut, branchErr := branchCmd.CombinedOutput()
				Expect(branchErr).NotTo(HaveOccurred(), "git branch --list: %s", string(branchOut))
				Expect(strings.TrimSpace(string(branchOut))).NotTo(BeEmpty(),
					"autoresearch/cleanrun branch should be preserved")

				// Summary stdout must mention the cleanup line.
				Expect(out.String()).To(ContainSubstring("worktree removed"))
			})

			It("preserves the worktree on clean termination when --keep-worktree is set", func() {
				worktreeBase := filepath.Join(dataDir, "wt-keep")
				err := runMaxTrialsOne("keeprunn-rest-of-id", worktreeBase, "--keep-worktree")
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				worktreePath := filepath.Join(worktreeBase, "keeprunn-rest-of-id", "worktree")
				info, statErr := os.Stat(worktreePath)
				Expect(statErr).NotTo(HaveOccurred(),
					"worktree should be preserved with --keep-worktree")
				Expect(info.IsDir()).To(BeTrue())

				// Branch must remain (always preserved).
				branchCmd := exec.Command("git", "-C", repoDir, "branch", "--list", "autoresearch/keeprunn")
				branchOut, _ := branchCmd.CombinedOutput()
				Expect(strings.TrimSpace(string(branchOut))).NotTo(BeEmpty())

				// Summary stdout must mention --keep-worktree.
				Expect(out.String()).To(ContainSubstring("--keep-worktree"))
			})

			It("preserves the worktree on a signal termination regardless of --keep-worktree default", func() {
				// A driver that drops a marker file then waits long
				// enough for the test goroutine to cancel the parent
				// context. Cancelling cmd.Context() propagates through
				// signal.NotifyContext into the loop's <-ctx.Done()
				// branch, producing terminationReason=signal — same
				// contract as the operator-Ctrl-C path without the
				// fragility of sending SIGTERM to the test binary
				// (which collides with Ginkgo's own interrupt handler).
				markerPath := filepath.Join(dataDir, "signal-marker")
				driverPath := filepath.Join(dataDir, "wait-driver.sh")
				driverBody := fmt.Sprintf(`#!/usr/bin/env bash
touch %q
# Wait long enough for the test to cancel the parent context.
# The driver subprocess uses context.Background() so this sleep is
# bounded by --driver-timeout, not the run-level signal.
sleep 5
exit 0
`, markerPath)
				Expect(os.WriteFile(driverPath, []byte(driverBody), 0o755)).To(Succeed())

				worktreeBase := filepath.Join(dataDir, "wt-signal")
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Watcher goroutine: when the driver drops the marker,
				// cancel the run context; the loop's next iteration
				// observes <-ctx.Done() and breaks with reason=signal.
				go func() {
					defer GinkgoRecover()
					deadline := time.Now().Add(10 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(markerPath); err == nil {
							cancel()
							return
						}
						time.Sleep(20 * time.Millisecond)
					}
				}()

				root := cli.NewRootCmd(testApp)
				root.SetOut(out)
				root.SetErr(out)
				root.SetArgs([]string{
					"autoresearch", "run",
					"--surface", surface,
					"--run-id", "signalrn-rest-of-id",
					"--max-trials", "5",
					"--time-budget", "30s",
					"--driver-timeout", "8s",
					"--worktree-base", worktreeBase,
					"--driver-script", driverPath,
					"--evaluator-script", writeNoOpScorer(),
				})
				err := root.ExecuteContext(ctx)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				worktreePath := filepath.Join(worktreeBase, "signalrn-rest-of-id", "worktree")
				_, statErr := os.Stat(worktreePath)
				Expect(statErr).NotTo(HaveOccurred(),
					"worktree should be preserved on signal termination")

				// Summary stdout must mention preserve + signal reason.
				Expect(out.String()).To(ContainSubstring("worktree preserved"))
				Expect(out.String()).To(ContainSubstring("signal"))
			})

			It("reports cleanup failure non-fatally and preserves the branch", func() {
				// A driver that locks the worktree via `git worktree
				// lock` makes `git worktree remove --force` refuse with
				// "is locked" — exercising the cleanup-failure path.
				// The branch is preserved as the durable artefact and
				// the run still returns nil.
				driverPath := filepath.Join(dataDir, "lock-driver.sh")
				driverBody := `#!/usr/bin/env bash
set -eu
# Resolve the parent repo via this worktree's git dir, then lock
# the worktree by its path. Locked worktrees refuse
# 'git worktree remove' even with --force.
worktree_root="$PWD"
git -c gc.auto=0 worktree lock "$worktree_root" 2>/dev/null || true
exit 0
`
				Expect(os.WriteFile(driverPath, []byte(driverBody), 0o755)).To(Succeed())

				worktreeBase := filepath.Join(dataDir, "wt-cleanup-fail")
				args := []string{
					"autoresearch", "run",
					"--surface", surface,
					"--run-id", "cleanupf-rest-of-id",
					"--max-trials", "1",
					"--time-budget", "30s",
					"--worktree-base", worktreeBase,
					"--driver-script", driverPath,
					"--evaluator-script", writeNoOpScorer(),
				}
				err := runCmd(args...)
				Expect(err).NotTo(HaveOccurred(),
					"cleanup failure must not fail the run; out: %s", out.String())

				// Cleanup failure surfaces as a log line.
				Expect(out.String()).To(ContainSubstring("cleanup"))
				Expect(out.String()).To(ContainSubstring("removal failed"))

				// Branch must still exist regardless.
				branchCmd := exec.Command("git", "-C", repoDir, "branch", "--list", "autoresearch/cleanupf")
				branchOut, _ := branchCmd.CombinedOutput()
				Expect(strings.TrimSpace(string(branchOut))).NotTo(BeEmpty(),
					"branch must be preserved even when worktree removal fails")

				// Manual cleanup: unlock so other tests / teardown can
				// proceed without git complaining.
				worktreePath := filepath.Join(worktreeBase, "cleanupf-rest-of-id", "worktree")
				DeferCleanup(func() {
					_ = exec.Command("git", "-C", repoDir, "worktree", "unlock", worktreePath).Run()
					_ = exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", worktreePath).Run()
				})
			})
		})
	})

	// Trial-loop specs (Slice 1c). The driver script appends a marker
	// line to the surface keyed by a counter file in dataDir. The
	// scorer reads a sequence-of-integers file from dataDir and emits
	// the Nth integer where N is the current trial counter. This keeps
	// the trajectory entirely deterministic — no provider, no network,
	// no time-of-day dependency.
	Describe("trial loop", func() {
		// writeDeterministicDriver writes a driver that:
		// 1. Reads the trial counter from $DATA_DIR/trial-counter (defaults 1).
		// 2. Reads the next candidate body from $DATA_DIR/candidate-N.
		// 3. Overwrites the surface at $FLOWSTATE_AUTORESEARCH_SURFACE.
		writeDeterministicDriver := func() string {
			path := filepath.Join(dataDir, "det-driver.sh")
			body := `#!/usr/bin/env bash
set -eu
trial_file="$DATA_DIR/trial-counter"
n=$(cat "$trial_file" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$trial_file"
candidate="$DATA_DIR/candidate-$n"
if [ ! -f "$candidate" ]; then
  exit 0
fi
cp "$candidate" "$FLOWSTATE_AUTORESEARCH_SURFACE"
`
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		// writeDeterministicScorer writes a scorer that reads the
		// score sequence file and emits the Nth integer. When the
		// trial counter is absent or 0 (the baseline-scoring step),
		// it emits the dedicated baseline-score file (defaults to 0).
		writeDeterministicScorer := func() string {
			path := filepath.Join(dataDir, "det-scorer.sh")
			body := `#!/usr/bin/env bash
set -eu
trial_file="$DATA_DIR/trial-counter"
n=$(cat "$trial_file" 2>/dev/null || echo 0)
if [ "$n" -le 0 ]; then
  if [ -f "$DATA_DIR/baseline-score" ]; then
    cat "$DATA_DIR/baseline-score"
  else
    echo 0
  fi
  exit 0
fi
score=$(sed -n "${n}p" "$DATA_DIR/score-sequence")
echo "$score"
`
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		writeCandidate := func(n int, body string) {
			path := filepath.Join(dataDir, fmt.Sprintf("candidate-%d", n))
			Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())
		}

		writeScoreSequence := func(scores []string) {
			path := filepath.Join(dataDir, "score-sequence")
			Expect(os.WriteFile(path, []byte(strings.Join(scores, "\n")+"\n"), 0o600)).To(Succeed())
		}

		writeBaselineScore := func(score string) {
			path := filepath.Join(dataDir, "baseline-score")
			Expect(os.WriteFile(path, []byte(score+"\n"), 0o600)).To(Succeed())
		}

		// makeManifest produces a valid manifest body with a unique
		// marker so each trial's content SHA is distinct.
		makeManifest := func(marker string) string {
			return fmt.Sprintf(`---
schema_version: "1"
id: planner
name: Planner
complexity: standard
metadata:
  role: planner role - %s
capabilities:
  tools: [read, plan]
---
planner body %s
`, marker, marker)
		}

		readTrialRecord := func(runID string, n int) map[string]any {
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())
			key := fmt.Sprintf("autoresearch/%s/trial-%d", runID, n)
			val, ok := entries[key]
			Expect(ok).To(BeTrue(), "trial-%d record expected at %s", n, key)
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())
			return record
		}

		readBestRecord := func(runID string) map[string]any {
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())
			key := fmt.Sprintf("autoresearch/%s/best", runID)
			val, ok := entries[key]
			Expect(ok).To(BeTrue(), "best record expected at %s", key)
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())
			return record
		}

		runHarness := func(runID string, maxTrials int, extraArgs ...string) error {
			args := []string{
				"autoresearch", "run",
				"--surface", surface,
				"--run-id", runID,
				"--max-trials", fmt.Sprintf("%d", maxTrials),
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--no-improve-window", "2",
				"--worktree-base", filepath.Join(dataDir, "wt-"+runID),
				"--driver-script", writeDeterministicDriver(),
				"--evaluator-script", writeDeterministicScorer(),
			}
			args = append(args, extraArgs...)
			// The driver/scorer read $DATA_DIR; expose it via the
			// process env. Tests run sequentially so the env mutation
			// is contained.
			Expect(os.Setenv("DATA_DIR", dataDir)).To(Succeed())
			DeferCleanup(func() { _ = os.Unsetenv("DATA_DIR") })
			return runCmd(args...)
		}

		It("keeps a trial whose score improves under metric-direction=min", func() {
			// Baseline manifest scores 10. Trial 1 scores 5
			// (improvement, kept). Trial 2 has no candidate so the
			// driver no-ops; the surface SHA repeats → fixed-point.
			writeBaselineScore("10")
			writeCandidate(1, makeManifest("v1"))
			writeScoreSequence([]string{"5", "5"})

			err := runHarness("imp-1", 1)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("imp-1", 1)
			Expect(rec).To(HaveKeyWithValue("kept", true))
			Expect(rec).To(HaveKeyWithValue("reason", "improved"))
			Expect(rec["score"]).To(BeNumerically("==", 5))

			best := readBestRecord("imp-1")
			Expect(best).To(HaveKeyWithValue("score", float64(5)))
			Expect(best).To(HaveKey("commit_sha"))
		})

		It("reverts a trial whose score regresses under metric-direction=min", func() {
			writeCandidate(1, makeManifest("regressed"))
			writeScoreSequence([]string{"99", "99"})

			err := runHarness("reg-1", 1)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("reg-1", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "regression"))
		})

		It("skips a fixed-point candidate (SHA repeat) without scoring", func() {
			// Trial 1: surface unchanged because no candidate file.
			// Driver no-ops → SHA matches baseline → fixed-point-skipped.
			writeScoreSequence([]string{"0", "0"})

			err := runHarness("fp-1", 1)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("fp-1", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "fixed-point-skipped"))
		})

		It("reverts a trial whose candidate fails the manifest gate", func() {
			// Manifest with an invalid colour value — frontmatter
			// parses, id derives from filename, but Validate rejects
			// the malformed hex string.
			brokenBody := `---
schema_version: "1"
id: planner
name: Planner
color: not-a-hex
complexity: standard
metadata:
  role: broken
capabilities:
  tools: [read]
---
broken candidate
`
			writeCandidate(1, brokenBody)
			writeScoreSequence([]string{"1", "1"})

			err := runHarness("mg-1", 1)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("mg-1", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "manifest-validate-failed"))
		})

		It("terminates with reason=max-trials when the counter is exhausted", func() {
			writeBaselineScore("10")
			writeCandidate(1, makeManifest("a"))
			writeCandidate(2, makeManifest("b"))
			writeScoreSequence([]string{"5", "4", "3"})

			err := runHarness("mt-1", 2)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			result := readTrialRecord("mt-1", 1)
			Expect(result).NotTo(BeNil())
			result2 := readTrialRecord("mt-1", 2)
			Expect(result2).NotTo(BeNil())

			// Result record records the termination reason.
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())
			val, ok := entries["autoresearch/mt-1/result"]
			Expect(ok).To(BeTrue(), "result record expected")
			var resultRec map[string]any
			Expect(json.Unmarshal([]byte(val), &resultRec)).To(Succeed())
			Expect(resultRec).To(HaveKeyWithValue("termination_reason", "max-trials"))
			Expect(resultRec["total_trials"]).To(BeNumerically("==", 2))
		})

		It("terminates with reason=converged after --no-improve-window non-improving trials", func() {
			// Baseline scoreless; trial 1 scores 5 (kept). Trials 2 and 3
			// score 6 each (regressions). With --no-improve-window 2,
			// the harness terminates after trial 3 with reason=converged.
			writeCandidate(1, makeManifest("first"))
			writeCandidate(2, makeManifest("second"))
			writeCandidate(3, makeManifest("third"))
			writeScoreSequence([]string{"5", "6", "6", "6"})

			err := runHarness("conv-1", 10)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())
			val, ok := entries["autoresearch/conv-1/result"]
			Expect(ok).To(BeTrue())
			var resultRec map[string]any
			Expect(json.Unmarshal([]byte(val), &resultRec)).To(Succeed())
			Expect(resultRec).To(HaveKeyWithValue("termination_reason", "converged"))
		})

		It("honours --metric-direction max by keeping higher scores", func() {
			writeCandidate(1, makeManifest("hi"))
			writeScoreSequence([]string{"50", "50"})

			err := runHarness("max-1", 1, "--metric-direction", "max")
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("max-1", 1)
			Expect(rec).To(HaveKeyWithValue("kept", true))
			Expect(rec).To(HaveKeyWithValue("reason", "improved"))
			Expect(rec["score"]).To(BeNumerically("==", 50))
		})

		It("prints a final summary block on completion", func() {
			writeBaselineScore("10")
			writeCandidate(1, makeManifest("summary-v1"))
			writeScoreSequence([]string{"5", "5"})

			err := runHarness("sum-1", 1)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			output := out.String()
			Expect(output).To(ContainSubstring("autoresearch run sum-1: summary"))
			Expect(output).To(ContainSubstring("trials_run="))
			Expect(output).To(ContainSubstring("kept="))
			Expect(output).To(ContainSubstring("reverted="))
			Expect(output).To(ContainSubstring("best_score="))
			Expect(output).To(ContainSubstring("termination_reason=max-trials"))
		})

		It("records baseline_score and baseline_commit in the manifest record", func() {
			writeBaselineScore("7")
			writeCandidate(1, makeManifest("with-baseline"))
			writeScoreSequence([]string{"3"})

			err := runHarness("baseline-1", 1)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("baseline-1")
			Expect(rec).To(HaveKey("baseline_score"))
			Expect(rec["baseline_score"]).To(BeNumerically("==", 7))
			Expect(rec).To(HaveKey("baseline_commit"))
			Expect(rec["baseline_commit"]).NotTo(BeEmpty())
		})

		It("appends seen-candidates SHAs across trials", func() {
			writeCandidate(1, makeManifest("seen-1"))
			writeCandidate(2, makeManifest("seen-2"))
			writeScoreSequence([]string{"3", "2", "1"})

			err := runHarness("seen-1", 2)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())

			val, ok := entries["autoresearch/seen-1/seen-candidates"]
			Expect(ok).To(BeTrue(), "seen-candidates ring expected")
			var ring []map[string]any
			Expect(json.Unmarshal([]byte(val), &ring)).To(Succeed())
			Expect(len(ring)).To(BeNumerically(">=", 2))
		})

		// Slice 3 — deterministic spine smoke. Drives the harness for
		// five trials with a mixed-trajectory fixture that exercises
		// every § 4.7 termination branch reachable in 5 trials:
		// improved, regression, fixed-point-skipped,
		// manifest-validate-failed, and a second improvement to land
		// on a new best. Pass criteria are loop-correctness only —
		// trial reasons, ratchet decisions, coord-store records, and
		// summary line. The fixture scripts live under
		// internal/cli/testdata/ as the canonical drivers per
		// plan v3.1 § 5.7.
		Context("deterministic spine smoke (Slice 3)", func() {
			testdataScript := func(name string) string {
				_, thisFile, _, ok := runtime.Caller(0)
				Expect(ok).To(BeTrue())
				return filepath.Join(filepath.Dir(thisFile), "testdata", name)
			}

			brokenManifest := `---
schema_version: "1"
id: planner
name: Planner
color: not-a-hex
complexity: standard
metadata:
  role: broken
capabilities:
  tools: [read]
---
broken candidate body
`

			It("ratchets correctly across a 5-trial mixed trajectory", func() {
				// Trajectory:
				//   baseline = 10
				//   trial 1: candidate-1 (clean v1)  → score 5  → improved (kept)
				//   trial 2: candidate-2 (clean v2)  → score 8  → regression (reverted)
				//   trial 3: no candidate file       → no-op    → fixed-point-skipped
				//                                                  (surface SHA matches kept trial-1 SHA)
				//   trial 4: candidate-4 (broken)    → manifest gate fails (reverted)
				//   trial 5: candidate-5 (clean v5)  → score 3  → improved (new best, kept)
				//
				// Final state: trials_run=5, kept=2, reverted=3,
				// best_score=3, termination_reason=max-trials.
				writeBaselineScore("10")
				writeCandidate(1, makeManifest("v1"))
				writeCandidate(2, makeManifest("v2"))
				// trial 3 deliberately omits candidate-3 to force
				// the driver no-op → fixed-point branch.
				writeCandidate(4, brokenManifest)
				writeCandidate(5, makeManifest("v5"))
				// score-sequence is consumed only on trials that
				// reach scoring. Trials 3 (fixed-point) and 4
				// (manifest gate) short-circuit before scoring, so
				// the sequence only needs entries for trials 1, 2,
				// 5 — but it is keyed by trial index, so we pad the
				// gap entries with sentinels that should never be
				// observed.
				writeScoreSequence([]string{"5", "8", "999", "999", "3"})

				const runID = "smoke-3-spine"
				args := []string{
					"autoresearch", "run",
					"--surface", surface,
					"--run-id", runID,
					"--max-trials", "5",
					"--time-budget", "30s",
					"--metric-direction", "min",
					"--no-improve-window", "10",
					"--worktree-base", filepath.Join(dataDir, "wt-"+runID),
					"--driver-script", testdataScript("autoresearch-driver.sh"),
					"--evaluator-script", testdataScript("autoresearch-scorer.sh"),
				}
				Expect(os.Setenv("DATA_DIR", dataDir)).To(Succeed())
				DeferCleanup(func() { _ = os.Unsetenv("DATA_DIR") })

				err := runCmd(args...)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				// Five trial records under autoresearch/<runID>/trial-*.
				trial1 := readTrialRecord(runID, 1)
				Expect(trial1).To(HaveKeyWithValue("kept", true))
				Expect(trial1).To(HaveKeyWithValue("reason", "improved"))
				Expect(trial1["score"]).To(BeNumerically("==", 5))

				trial2 := readTrialRecord(runID, 2)
				Expect(trial2).To(HaveKeyWithValue("kept", false))
				Expect(trial2).To(HaveKeyWithValue("reason", "regression"))

				trial3 := readTrialRecord(runID, 3)
				Expect(trial3).To(HaveKeyWithValue("kept", false))
				Expect(trial3).To(HaveKeyWithValue("reason", "fixed-point-skipped"))

				trial4 := readTrialRecord(runID, 4)
				Expect(trial4).To(HaveKeyWithValue("kept", false))
				Expect(trial4).To(HaveKeyWithValue("reason", "manifest-validate-failed"))

				trial5 := readTrialRecord(runID, 5)
				Expect(trial5).To(HaveKeyWithValue("kept", true))
				Expect(trial5).To(HaveKeyWithValue("reason", "improved"))
				Expect(trial5["score"]).To(BeNumerically("==", 3))

				// Best pointer references trial 5 (score=3 beats
				// trial 1's score=5 under metric-direction=min).
				best := readBestRecord(runID)
				Expect(best["score"]).To(BeNumerically("==", 3))
				Expect(best).To(HaveKey("commit_sha"))
				Expect(best["commit_sha"]).NotTo(BeEmpty())
				// Best's commit SHA should match trial 5's recorded SHA.
				Expect(best["commit_sha"]).To(Equal(trial5["commit_sha"]))

				// Seen-candidates ring captures all five trial SHAs.
				raw, readErr := os.ReadFile(coordPath)
				Expect(readErr).NotTo(HaveOccurred())
				var entries map[string]string
				Expect(json.Unmarshal(raw, &entries)).To(Succeed())
				ringRaw, ok := entries["autoresearch/"+runID+"/seen-candidates"]
				Expect(ok).To(BeTrue(), "seen-candidates ring expected")
				var ring []map[string]any
				Expect(json.Unmarshal([]byte(ringRaw), &ring)).To(Succeed())
				// Ring carries the baseline (trial_n=0) plus one entry
				// per trial, so for a 5-trial run we expect 6 entries.
				Expect(ring).To(HaveLen(6), "ring length: %d", len(ring))
				// Trial 3's recorded candidate SHA matches trial 1's
				// — that is the SHA collision that drove the
				// fixed-point-skipped reason.
				Expect(ring[1]["candidate_sha"]).To(Equal(ring[3]["candidate_sha"]))

				// Result record summarises the run.
				resultRaw, ok := entries["autoresearch/"+runID+"/result"]
				Expect(ok).To(BeTrue(), "result record expected")
				var result map[string]any
				Expect(json.Unmarshal([]byte(resultRaw), &result)).To(Succeed())
				Expect(result).To(HaveKeyWithValue("termination_reason", "max-trials"))
				Expect(result["total_trials"]).To(BeNumerically("==", 5))

				// Final stdout summary reflects the trajectory.
				output := out.String()
				Expect(output).To(ContainSubstring("trials_run=5"))
				Expect(output).To(ContainSubstring("kept=2"))
				Expect(output).To(ContainSubstring("reverted=3"))
				Expect(output).To(ContainSubstring("best_score=3"))
				Expect(output).To(ContainSubstring("termination_reason=max-trials"))
				Expect(output).To(ContainSubstring("autoresearch run " + runID + ": summary"))
			})
		})
	})

	// Slice 4 — pluggable surface + surface-type detection per
	// plan v3.1 § 4.4. The MVP's hard-coded planner.md gate is
	// generalised: any single file is acceptable as a surface, and
	// the manifest-validate gate fires only when type=manifest.
	//
	// Detection rules (in order):
	//   1. path under cfg.AgentDir or cfg.AgentDirs              → manifest
	//   2. .md file with frontmatter carrying capabilities.tools
	//      or delegation.delegation_allowlist                    → manifest
	//   3. path under skills/ ending in SKILL.md                 → skill
	//   4. else                                                  → source
	//
	// The detected type is persisted on the manifest record AND on
	// each trial record so an operator can audit which gate fired.
	Describe("surface-type detection (Slice 4)", func() {
		writeNoOpDriver := func() string {
			path := filepath.Join(dataDir, "noop-driver-st.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755)).To(Succeed())
			return path
		}
		writeNoOpScorer := func() string {
			path := filepath.Join(dataDir, "noop-scorer-st.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\necho 0\n"), 0o755)).To(Succeed())
			return path
		}

		// makeSurface writes content to a path inside repoDir,
		// creates parent directories as needed, and re-commits the
		// repo so the clean-tree precondition still holds.
		makeSurface := func(relPath, body string) string {
			abs := filepath.Join(repoDir, relPath)
			Expect(os.MkdirAll(filepath.Dir(abs), 0o755)).To(Succeed())
			Expect(os.WriteFile(abs, []byte(body), 0o600)).To(Succeed())

			run := func(args ...string) {
				c := exec.Command("git", args...)
				c.Dir = repoDir
				c.Env = append(os.Environ(),
					"GIT_AUTHOR_NAME=test",
					"GIT_AUTHOR_EMAIL=test@example.com",
					"GIT_COMMITTER_NAME=test",
					"GIT_COMMITTER_EMAIL=test@example.com",
				)
				combined, err := c.CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(combined))
			}
			run("add", relPath)
			run("commit", "--no-verify", "-m", "add "+relPath)
			return abs
		}

		runSetup := func(runID, surfacePath string, extraArgs ...string) error {
			args := []string{
				"autoresearch", "run",
				"--surface", surfacePath,
				"--run-id", runID,
				"--max-trials", "0",
				"--time-budget", "30s",
				"--worktree-base", filepath.Join(dataDir, "wt-"+runID),
				"--driver-script", writeNoOpDriver(),
				"--evaluator-script", writeNoOpScorer(),
			}
			args = append(args, extraArgs...)
			return runCmd(args...)
		}

		It("detects a surface under cfg.AgentDir as manifest (path heuristic)", func() {
			// The default planner.md surface lives under AgentDir →
			// rule 1 fires.
			err := runSetup("st-manifest-path", surface)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("st-manifest-path")
			Expect(rec).To(HaveKeyWithValue("surface_type", "manifest"))
		})

		It("detects an .md surface with capabilities.tools as manifest (frontmatter probe)", func() {
			// Same manifest body but parked outside AgentDir so
			// rule 1 misses; rule 2 catches it.
			body := `---
schema_version: "1"
id: stray
name: Stray
complexity: standard
metadata:
  role: stray role
capabilities:
  tools: [read]
---
stray manifest body
`
			path := makeSurface(filepath.Join("docs", "stray.md"), body)

			err := runSetup("st-manifest-fm", path)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("st-manifest-fm")
			Expect(rec).To(HaveKeyWithValue("surface_type", "manifest"))
		})

		It("detects an .md surface with delegation_allowlist as manifest (frontmatter probe)", func() {
			body := `---
schema_version: "1"
id: gateway
name: Gateway
complexity: standard
metadata:
  role: gateway role
delegation:
  delegation_allowlist: [child-a, child-b]
---
gateway manifest body
`
			path := makeSurface(filepath.Join("ops", "gateway.md"), body)

			err := runSetup("st-manifest-deleg", path)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("st-manifest-deleg")
			Expect(rec).To(HaveKeyWithValue("surface_type", "manifest"))
		})

		It("does NOT auto-classify schema_version-only frontmatter as manifest", func() {
			// schema_version was rejected as a marker per § 4.4 —
			// it appears on plan and ADR notes too. Such files fall
			// to rule 4 (source) when neither path heuristic nor
			// the manifest-only keys match.
			body := `---
schema_version: "1"
title: Some Plan
---
plan body
`
			path := makeSurface(filepath.Join("plans", "some-plan.md"), body)

			err := runSetup("st-not-manifest", path)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("st-not-manifest")
			Expect(rec).To(HaveKeyWithValue("surface_type", "source"))
		})

		It("detects a SKILL.md under skills/ as skill", func() {
			body := `---
name: example-skill
---
# Skill body

Prose only — no manifest keys.
`
			path := makeSurface(filepath.Join("skills", "example", "SKILL.md"), body)

			err := runSetup("st-skill", path)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("st-skill")
			Expect(rec).To(HaveKeyWithValue("surface_type", "skill"))
		})

		It("classifies arbitrary source files as source", func() {
			body := "package fixture\n\nfunc Demo() int { return 0 }\n"
			path := makeSurface(filepath.Join("internal", "fixture", "demo.go"), body)

			err := runSetup("st-source", path)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("st-source")
			Expect(rec).To(HaveKeyWithValue("surface_type", "source"))
		})

		It("includes the surface_type in the run summary line", func() {
			err := runSetup("st-summary", surface)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			Expect(out.String()).To(ContainSubstring("surface_type=manifest"))
		})
	})

	// Slice 4 — manifest gate behaviour now keys off detected type
	// rather than the MVP's path-prefix probe. Manifest gate fires
	// for type=manifest (regardless of file location). For
	// type ∈ {skill, source} the gate is a no-op and the trial
	// proceeds to scoring.
	Describe("manifest gate behaviour by surface type (Slice 4)", func() {
		// makeSurface mirrors the helper in the surface-type
		// Describe — written here so the gate spec can stand alone.
		makeSurface := func(relPath, body string) string {
			abs := filepath.Join(repoDir, relPath)
			Expect(os.MkdirAll(filepath.Dir(abs), 0o755)).To(Succeed())
			Expect(os.WriteFile(abs, []byte(body), 0o600)).To(Succeed())

			run := func(args ...string) {
				c := exec.Command("git", args...)
				c.Dir = repoDir
				c.Env = append(os.Environ(),
					"GIT_AUTHOR_NAME=test",
					"GIT_AUTHOR_EMAIL=test@example.com",
					"GIT_COMMITTER_NAME=test",
					"GIT_COMMITTER_EMAIL=test@example.com",
				)
				combined, err := c.CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(combined))
			}
			run("add", relPath)
			run("commit", "--no-verify", "-m", "add "+relPath)
			return abs
		}

		// brokenManifest is a frontmatter-keyed manifest with an
		// invalid colour — Validate rejects it. Used to prove the
		// gate fires for type=manifest detected via the probe.
		brokenManifestBody := `---
schema_version: "1"
id: stray-broken
name: StrayBroken
color: not-a-hex
complexity: standard
metadata:
  role: broken stray
capabilities:
  tools: [read]
---
broken stray manifest
`

		// validManifestBody seeds the surface. The driver overwrites
		// it with brokenManifestBody to trip the gate.
		validManifestBody := `---
schema_version: "1"
id: stray-valid
name: StrayValid
complexity: standard
metadata:
  role: valid stray
capabilities:
  tools: [read]
---
valid stray manifest
`

		writeOverwriteDriver := func(replacementPath string) string {
			path := filepath.Join(dataDir, "overwrite-driver.sh")
			body := fmt.Sprintf(`#!/usr/bin/env bash
set -eu
cp %q "$FLOWSTATE_AUTORESEARCH_SURFACE"
`, replacementPath)
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		writeNoOpScorer := func() string {
			path := filepath.Join(dataDir, "noop-scorer-gate.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\necho 0\n"), 0o755)).To(Succeed())
			return path
		}

		readTrialRecord := func(runID string, n int) map[string]any {
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())
			key := fmt.Sprintf("autoresearch/%s/trial-%d", runID, n)
			val, ok := entries[key]
			Expect(ok).To(BeTrue(), "trial-%d record expected at %s", n, key)
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())
			return record
		}

		It("fires the manifest gate for a manifest detected via frontmatter probe", func() {
			surfacePath := makeSurface(filepath.Join("docs", "stray-manifest.md"), validManifestBody)

			// Stage the broken replacement separately so the driver
			// can copy it without re-touching the worktree's
			// committed state.
			brokenPath := filepath.Join(dataDir, "broken-replacement.md")
			Expect(os.WriteFile(brokenPath, []byte(brokenManifestBody), 0o600)).To(Succeed())

			args := []string{
				"autoresearch", "run",
				"--surface", surfacePath,
				"--run-id", "gate-fm",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--no-improve-window", "10",
				"--worktree-base", filepath.Join(dataDir, "wt-gate-fm"),
				"--driver-script", writeOverwriteDriver(brokenPath),
				"--evaluator-script", writeNoOpScorer(),
			}
			err := runCmd(args...)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("gate-fm", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "manifest-validate-failed"))
		})

		It("does NOT fire the manifest gate for a skill surface", func() {
			// SKILL.md contains nothing the manifest validator
			// would accept. If the gate fires, the trial would
			// fail with manifest-validate-failed instead of being
			// kept. Pin: trial-1 is kept (improved) under min.
			seedBody := "---\nname: example\n---\n\nseed body v0\n"
			surfacePath := makeSurface(filepath.Join("skills", "example", "SKILL.md"), seedBody)

			replaceBody := "---\nname: example\n---\n\nimproved body v1\n"
			replacePath := filepath.Join(dataDir, "skill-replacement.md")
			Expect(os.WriteFile(replacePath, []byte(replaceBody), 0o600)).To(Succeed())

			// Scorer drops from 10 → 5 so trial 1 is kept under min.
			scorer := filepath.Join(dataDir, "drop-scorer.sh")
			scorerBody := `#!/usr/bin/env bash
set -eu
state="$DATA_DIR/drop-scorer-state"
n=$(cat "$state" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$state"
if [ "$n" -le 1 ]; then
  echo 10
else
  echo 5
fi
`
			Expect(os.WriteFile(scorer, []byte(scorerBody), 0o755)).To(Succeed())

			Expect(os.Setenv("DATA_DIR", dataDir)).To(Succeed())
			DeferCleanup(func() { _ = os.Unsetenv("DATA_DIR") })

			args := []string{
				"autoresearch", "run",
				"--surface", surfacePath,
				"--run-id", "gate-skill",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--no-improve-window", "10",
				"--worktree-base", filepath.Join(dataDir, "wt-gate-skill"),
				"--driver-script", writeOverwriteDriver(replacePath),
				"--evaluator-script", scorer,
			}
			err := runCmd(args...)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("gate-skill", 1)
			Expect(rec).To(HaveKeyWithValue("kept", true))
			Expect(rec).To(HaveKeyWithValue("reason", "improved"))
		})

		It("does NOT fire the manifest gate for a source surface", func() {
			// A Go source file that the manifest validator would
			// reject outright. If the gate fires the trial would
			// short-circuit; pin that scoring proceeds normally.
			seedBody := "package demo\n\nfunc Demo() int { return 0 }\n"
			surfacePath := makeSurface(filepath.Join("internal", "demo", "demo.go"), seedBody)

			replaceBody := "package demo\n\nfunc Demo() int { return 1 }\n"
			replacePath := filepath.Join(dataDir, "source-replacement.go")
			Expect(os.WriteFile(replacePath, []byte(replaceBody), 0o600)).To(Succeed())

			scorer := filepath.Join(dataDir, "drop-scorer-src.sh")
			scorerBody := `#!/usr/bin/env bash
set -eu
state="$DATA_DIR/drop-scorer-src-state"
n=$(cat "$state" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$state"
if [ "$n" -le 1 ]; then
  echo 10
else
  echo 5
fi
`
			Expect(os.WriteFile(scorer, []byte(scorerBody), 0o755)).To(Succeed())

			Expect(os.Setenv("DATA_DIR", dataDir)).To(Succeed())
			DeferCleanup(func() { _ = os.Unsetenv("DATA_DIR") })

			args := []string{
				"autoresearch", "run",
				"--surface", surfacePath,
				"--run-id", "gate-source",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--no-improve-window", "10",
				"--worktree-base", filepath.Join(dataDir, "wt-gate-source"),
				"--driver-script", writeOverwriteDriver(replacePath),
				"--evaluator-script", scorer,
			}
			err := runCmd(args...)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("gate-source", 1)
			Expect(rec).To(HaveKeyWithValue("kept", true))
			Expect(rec).To(HaveKeyWithValue("reason", "improved"))
		})

		It("persists surface_type on each trial record", func() {
			seedBody := "package demo\n\nfunc Demo() int { return 0 }\n"
			surfacePath := makeSurface(filepath.Join("internal", "trace", "trace.go"), seedBody)

			replaceBody := "package demo\n\nfunc Demo() int { return 1 }\n"
			replacePath := filepath.Join(dataDir, "trace-replacement.go")
			Expect(os.WriteFile(replacePath, []byte(replaceBody), 0o600)).To(Succeed())

			scorer := filepath.Join(dataDir, "drop-scorer-trace.sh")
			scorerBody := `#!/usr/bin/env bash
set -eu
state="$DATA_DIR/drop-scorer-trace-state"
n=$(cat "$state" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$state"
if [ "$n" -le 1 ]; then
  echo 10
else
  echo 5
fi
`
			Expect(os.WriteFile(scorer, []byte(scorerBody), 0o755)).To(Succeed())

			Expect(os.Setenv("DATA_DIR", dataDir)).To(Succeed())
			DeferCleanup(func() { _ = os.Unsetenv("DATA_DIR") })

			args := []string{
				"autoresearch", "run",
				"--surface", surfacePath,
				"--run-id", "trace-st",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--no-improve-window", "10",
				"--worktree-base", filepath.Join(dataDir, "wt-trace-st"),
				"--driver-script", writeOverwriteDriver(replacePath),
				"--evaluator-script", scorer,
			}
			err := runCmd(args...)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("trace-st", 1)
			Expect(rec).To(HaveKeyWithValue("surface_type", "source"))
		})
	})

	// Slice 5 — formal evaluator contract per plan v3.1 § 4.6 +
	// reference `bench.sh` evaluator + `--metric-direction max`
	// end-to-end demonstration. The MVP plumbing accepted any non-zero
	// scalar; Slice 5 hardens that surface so contract violations are
	// caught and the operator-facing `--evaluator-script` flag has a
	// documented, testable contract.
	//
	// Pinned behaviour:
	//   - stdout containing a non-integer string → evaluator-contract-violation
	//   - stdout containing a negative integer    → evaluator-contract-violation
	//   - stdout containing more than one non-empty line (after trim)
	//                                              → evaluator-contract-violation
	//   - non-zero exit code                       → evaluator-contract-violation
	//   - evaluator wall-clock exceeds --evaluator-timeout
	//                                              → evaluator-contract-violation
	//                                                + evaluator_timeout_ms recorded on the trial
	//   - three consecutive evaluator-contract-violation trials
	//                                              → terminate with
	//                                                reason=evaluator-contract-failure-rate
	//   - --metric-direction max with the reference bench.sh evaluator
	//                                              → higher scores kept
	Describe("evaluator contract (Slice 5)", func() {
		writeNoOpDriver := func() string {
			path := filepath.Join(dataDir, "noop-driver-eval.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755)).To(Succeed())
			return path
		}

		// writeAlwaysEditDriver writes a driver that overwrites the
		// surface with a unique trial-N body each invocation so the
		// fixed-point gate never fires; we want each trial to reach
		// the evaluator so the contract checks actually run.
		writeAlwaysEditDriver := func() string {
			path := filepath.Join(dataDir, "always-edit-driver.sh")
			body := `#!/usr/bin/env bash
set -eu
trial_file="$DATA_DIR/eval-counter"
n=$(cat "$trial_file" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$trial_file"
cat <<EOF > "$FLOWSTATE_AUTORESEARCH_SURFACE"
---
schema_version: "1"
id: planner
name: Planner
complexity: standard
metadata:
  role: planner role - eval-trial-$n
capabilities:
  tools: [read, plan]
---
planner body trial $n
EOF
`
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		// writeContractEvaluator writes an evaluator whose stdout body
		// is taken verbatim from the supplied string for trial-level
		// invocations. The very first invocation (baseline scoring)
		// always emits a clean `0\n` so the run reaches the trial
		// loop; otherwise the run would abort during baseline.
		// Used to drive the contract-violation specs (non-integer,
		// negative, multi-line, etc.).
		writeContractEvaluator := func(stdoutBody string) string {
			path := filepath.Join(dataDir, "contract-evaluator.sh")
			body := fmt.Sprintf(`#!/usr/bin/env bash
state="$DATA_DIR/contract-evaluator-state"
n=$(cat "$state" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$state"
if [ "$n" -le 1 ]; then
  echo 0
  exit 0
fi
cat <<'STDOUT_EOF'
%s
STDOUT_EOF
`, stdoutBody)
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		// writeNonZeroExitEvaluator writes an evaluator that emits a
		// clean baseline `0` then exits non-zero on every trial-level
		// invocation — the MVP-canonical way to signal evaluator-side
		// failure per plan § 4.6.
		writeNonZeroExitEvaluator := func() string {
			path := filepath.Join(dataDir, "exit-fail-evaluator.sh")
			body := `#!/usr/bin/env bash
state="$DATA_DIR/exit-fail-state"
n=$(cat "$state" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$state"
if [ "$n" -le 1 ]; then
  echo 0
  exit 0
fi
echo "boom" >&2
exit 7
`
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		// writeSlowEvaluator writes an evaluator that emits `0`
		// instantly on the baseline call then sleeps for `seconds`
		// on subsequent trial-level calls. Used to exercise the
		// --evaluator-timeout SIGTERM path.
		writeSlowEvaluator := func(seconds int) string {
			path := filepath.Join(dataDir, "slow-evaluator.sh")
			body := fmt.Sprintf(`#!/usr/bin/env bash
state="$DATA_DIR/slow-eval-state"
n=$(cat "$state" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$state"
if [ "$n" -le 1 ]; then
  echo 0
  exit 0
fi
sleep %d
echo 0
`, seconds)
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		readTrialRecord := func(runID string, n int) map[string]any {
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())
			key := fmt.Sprintf("autoresearch/%s/trial-%d", runID, n)
			val, ok := entries[key]
			Expect(ok).To(BeTrue(), "trial-%d record expected at %s", n, key)
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())
			return record
		}

		readResultRecord := func(runID string) map[string]any {
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())
			key := fmt.Sprintf("autoresearch/%s/result", runID)
			val, ok := entries[key]
			Expect(ok).To(BeTrue(), "result record expected at %s", key)
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())
			return record
		}

		runWithEvaluator := func(runID string, maxTrials int, evaluator string, extraArgs ...string) error {
			args := []string{
				"autoresearch", "run",
				"--surface", surface,
				"--run-id", runID,
				"--max-trials", fmt.Sprintf("%d", maxTrials),
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--no-improve-window", "10",
				"--worktree-base", filepath.Join(dataDir, "wt-"+runID),
				"--driver-script", writeAlwaysEditDriver(),
				"--evaluator-script", evaluator,
			}
			args = append(args, extraArgs...)
			Expect(os.Setenv("DATA_DIR", dataDir)).To(Succeed())
			DeferCleanup(func() { _ = os.Unsetenv("DATA_DIR") })
			return runCmd(args...)
		}

		It("exposes --evaluator-timeout in run --help", func() {
			err := runCmd("autoresearch", "run", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("--evaluator-timeout"))
		})

		It("rejects evaluator stdout that is not an integer", func() {
			eval := writeContractEvaluator("not-a-number")
			err := runWithEvaluator("eval-non-int", 1, eval)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("eval-non-int", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "evaluator-contract-violation"))
		})

		It("rejects evaluator stdout that is a negative integer", func() {
			eval := writeContractEvaluator("-5")
			err := runWithEvaluator("eval-neg", 1, eval)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("eval-neg", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "evaluator-contract-violation"))
		})

		It("rejects evaluator stdout containing more than one non-empty line", func() {
			eval := writeContractEvaluator("12\n34")
			err := runWithEvaluator("eval-multi", 1, eval)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("eval-multi", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "evaluator-contract-violation"))
		})

		It("treats a non-zero evaluator exit as an evaluator-contract-violation (not a regression)", func() {
			eval := writeNonZeroExitEvaluator()
			err := runWithEvaluator("eval-exit", 1, eval)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("eval-exit", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "evaluator-contract-violation"))
		})

		It("terminates with reason=evaluator-contract-failure-rate after three consecutive violations", func() {
			eval := writeNonZeroExitEvaluator()
			err := runWithEvaluator("eval-rate", 5, eval)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			result := readResultRecord("eval-rate")
			Expect(result).To(HaveKeyWithValue("termination_reason", "evaluator-contract-failure-rate"))
			// Run halts on trial 3, not trial 5.
			Expect(result["total_trials"]).To(BeNumerically("==", 3))
		})

		It("records evaluator_timeout_ms on a trial when --evaluator-timeout fires", func() {
			eval := writeSlowEvaluator(5)
			err := runWithEvaluator("eval-timeout", 1, eval, "--evaluator-timeout", "200ms")
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readTrialRecord("eval-timeout", 1)
			Expect(rec).To(HaveKeyWithValue("kept", false))
			Expect(rec).To(HaveKeyWithValue("reason", "evaluator-contract-violation"))
			Expect(rec).To(HaveKey("evaluator_timeout_ms"))
			Expect(rec["evaluator_timeout_ms"]).To(BeNumerically(">=", 200))
		})

		It("records evaluator_script on the manifest record", func() {
			eval := writeContractEvaluator("0")
			err := runWithEvaluator("eval-script-name", 0, eval)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			rec := readManifestRecord("eval-script-name")
			Expect(rec).To(HaveKeyWithValue("evaluator_script", eval))
		})

		// The reference bench.sh evaluator parses ns/op out of a
		// `go test -bench` style output and emits ops/sec for the
		// max-direction demonstration. Tests do NOT run live `go
		// test -bench` — instead, the script is fed a fixture
		// captured to $FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT, mirroring
		// the convention in plan v3.1 § 5.9.
		Context("reference bench.sh evaluator", func() {
			testdataPath := func(name string) string {
				_, thisFile, _, ok := runtime.Caller(0)
				Expect(ok).To(BeTrue())
				return filepath.Join(filepath.Dir(thisFile), "testdata", name)
			}

			scriptPath := func() string {
				_, thisFile, _, ok := runtime.Caller(0)
				Expect(ok).To(BeTrue())
				// scripts/ lives at the repo root; walk up from the
				// test file (internal/cli/) two directories.
				return filepath.Join(filepath.Dir(thisFile), "..", "..", "scripts", "autoresearch-evaluators", "bench.sh")
			}

			It("emits a non-negative integer to stdout for a fixture bench output", func() {
				bench := scriptPath()
				info, statErr := os.Stat(bench)
				Expect(statErr).NotTo(HaveOccurred(), "scripts/autoresearch-evaluators/bench.sh must exist")
				Expect(info.Mode()&0o111).NotTo(BeZero(), "bench.sh must be executable")

				fixture := testdataPath("fake-bench-output.txt")
				_, statErr = os.Stat(fixture)
				Expect(statErr).NotTo(HaveOccurred(), "testdata/fake-bench-output.txt must exist")

				cmd := exec.Command(bench)
				cmd.Env = append(os.Environ(),
					"FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT="+fixture,
				)
				stdout, runErr := cmd.Output()
				Expect(runErr).NotTo(HaveOccurred(), "bench.sh: %s", string(stdout))

				line := strings.TrimSpace(string(stdout))
				Expect(line).NotTo(BeEmpty())
				Expect(strings.Contains(line, "\n")).To(BeFalse(), "bench.sh must emit exactly one line")

				// Positive integer: ops/sec derived from a positive
				// ns/op. Plan § 4.6 allows non-negative; for a real
				// benchmark fixture the value is strictly > 0.
				var n int
				_, scanErr := fmt.Sscanf(line, "%d", &n)
				Expect(scanErr).NotTo(HaveOccurred(), "bench.sh stdout %q must parse as int", line)
				Expect(n).To(BeNumerically(">", 0))
			})

			It("ratchets under --metric-direction max when bench.sh reports an improving ops/sec", func() {
				// Two fixture files: trial 1 reports a slower ns/op
				// (lower ops/sec); trial 2 reports a faster ns/op
				// (higher ops/sec). Under max direction, trial 1 is
				// the new best (first scored trial always kept) and
				// trial 2 ratchets upward.
				slowFixture := filepath.Join(dataDir, "bench-slow.txt")
				fastFixture := filepath.Join(dataDir, "bench-fast.txt")
				Expect(os.WriteFile(slowFixture, []byte(
					"BenchmarkDemo-8   	1000000	      1000 ns/op\n"+
						"PASS\n"+
						"ok  	example.com/demo	1.234s\n",
				), 0o600)).To(Succeed())
				Expect(os.WriteFile(fastFixture, []byte(
					"BenchmarkDemo-8   	5000000	       100 ns/op\n"+
						"PASS\n"+
						"ok  	example.com/demo	1.234s\n",
				), 0o600)).To(Succeed())

				// Wrapper evaluator: maintains its own invocation
				// counter so the trajectory is:
				//   call 1 (baseline scoring)         → very-slow (low ops/sec)
				//   call 2 (trial 1 candidate score)  → slow      (mid ops/sec, improvement)
				//   call 3 (trial 2 candidate score)  → fast      (high ops/sec, further improvement)
				// Driver runs strictly between scoring calls so we
				// can't share the driver's eval-counter here.
				verySlowFixture := filepath.Join(dataDir, "bench-baseline.txt")
				Expect(os.WriteFile(verySlowFixture, []byte(
					"BenchmarkDemo-8   	1000	      10000 ns/op\n"+
						"PASS\n"+
						"ok  	example.com/demo	1.234s\n",
				), 0o600)).To(Succeed())

				wrapper := filepath.Join(dataDir, "bench-wrapper.sh")
				wrapperBody := fmt.Sprintf(`#!/usr/bin/env bash
set -eu
state="$DATA_DIR/bench-wrapper-state"
n=$(cat "$state" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$state"
if [ "$n" -le 1 ]; then
  export FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT=%q
elif [ "$n" -le 2 ]; then
  export FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT=%q
else
  export FLOWSTATE_AUTORESEARCH_BENCH_OUTPUT=%q
fi
exec %q
`, verySlowFixture, slowFixture, fastFixture, scriptPath())
				Expect(os.WriteFile(wrapper, []byte(wrapperBody), 0o755)).To(Succeed())

				err := runWithEvaluator("eval-max-bench", 2, wrapper, "--metric-direction", "max")
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				trial1 := readTrialRecord("eval-max-bench", 1)
				Expect(trial1).To(HaveKeyWithValue("kept", true))
				Expect(trial1).To(HaveKeyWithValue("reason", "improved"))

				trial2 := readTrialRecord("eval-max-bench", 2)
				Expect(trial2).To(HaveKeyWithValue("kept", true))
				Expect(trial2).To(HaveKeyWithValue("reason", "improved"))
				Expect(trial2["score"]).To(BeNumerically(">", trial1["score"]))
			})
		})

		Context("evaluator validation", func() {
			It("rejects --evaluator-script when the path does not exist", func() {
				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "eval-missing",
					"--max-trials", "1",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-eval-missing"),
					"--driver-script", writeNoOpDriver(),
					"--evaluator-script", filepath.Join(dataDir, "ghost-evaluator.sh"),
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("evaluator-script"))
			})

			It("rejects --evaluator-script when the path is not executable", func() {
				notExec := filepath.Join(dataDir, "not-exec.sh")
				Expect(os.WriteFile(notExec, []byte("#!/usr/bin/env bash\necho 0\n"), 0o644)).To(Succeed())

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "eval-not-exec",
					"--max-trials", "1",
					"--time-budget", "30s",
					"--worktree-base", filepath.Join(dataDir, "wt-eval-not-exec"),
					"--driver-script", writeNoOpDriver(),
					"--evaluator-script", notExec,
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("executable"))
			})
		})
	})

	// Slice 6 — `--program <skill-name | path>` resolves the program-of-
	// record. Skill names look up `skills/<name>/SKILL.md` under the
	// repo root; paths (anything containing `/` or ending in `.md`)
	// resolve relative to repo root or absolute. Missing programs
	// reject before the run starts. The N12 de-dup behaviour pins
	// double-loading: when the calling agent already declares the
	// program skill in its `always_active_skills`, the harness logs
	// the de-dup decision and annotates the manifest record.
	Describe("program resolution (Slice 6)", func() {
		var (
			noOpDriver  string
			noOpScorer  string
			worktreeDir string
		)

		// writeProgramSkill creates `skills/<name>/SKILL.md` under the
		// test repoDir so skill-name resolution has something real to
		// point at. The body is minimal but valid markdown — the
		// harness only resolves the path; it does not parse the
		// content beyond a frontmatter/file-existence probe.
		writeProgramSkill := func(name, body string) string {
			skillDir := filepath.Join(repoDir, "skills", name)
			Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
			skillPath := filepath.Join(skillDir, "SKILL.md")
			Expect(os.WriteFile(skillPath, []byte(body), 0o600)).To(Succeed())
			// Re-stage and re-commit so the worktree starts clean.
			gitCmd := func(args ...string) {
				c := exec.Command("git", args...)
				c.Dir = repoDir
				c.Env = append(os.Environ(),
					"GIT_AUTHOR_NAME=test",
					"GIT_AUTHOR_EMAIL=test@example.com",
					"GIT_COMMITTER_NAME=test",
					"GIT_COMMITTER_EMAIL=test@example.com",
				)
				combined, err := c.CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(combined))
			}
			gitCmd("add", ".")
			gitCmd("commit", "--no-verify", "-m", "add skill "+name)
			return skillPath
		}

		// writeCallingAgentManifest authors a JSON manifest under the
		// dataDir (NOT inside the surface repo — placing it under
		// repoDir/internal/app/agents would dirty the tree and trip
		// the clean-tree precondition before the run starts). The
		// manifest is read by `applyCallingAgentDeDup` purely as input
		// to the N12 de-dup check; it does not need to live alongside
		// the surface. JSON over markdown so always_active_skills is
		// honoured via the JSON tag directly rather than re-mapped
		// through `default_skills`.
		writeCallingAgentManifest := func(id string, alwaysActive []string) string {
			manifestPath := filepath.Join(dataDir, id+".json")
			body := fmt.Sprintf(`{
  "schema_version": "1",
  "id": %q,
  "name": %q,
  "complexity": "standard",
  "metadata": {"role": "calling agent"},
  "capabilities": {
    "tools": ["read"],
    "always_active_skills": %s
  }
}`, id, id, jsonStringSlice(alwaysActive))
			Expect(os.WriteFile(manifestPath, []byte(body), 0o600)).To(Succeed())
			return manifestPath
		}

		BeforeEach(func() {
			noOpDriver = filepath.Join(dataDir, "noop-driver.sh")
			Expect(os.WriteFile(noOpDriver, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755)).To(Succeed())
			noOpScorer = filepath.Join(dataDir, "noop-scorer.sh")
			Expect(os.WriteFile(noOpScorer, []byte("#!/usr/bin/env bash\necho 0\n"), 0o755)).To(Succeed())
			worktreeDir = filepath.Join(dataDir, "wt-program")
		})

		Context("skill-name resolution", func() {
			It("defaults --program to the autoresearch skill when omitted", func() {
				skillPath := writeProgramSkill("autoresearch", "skill body")

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-default",
					"--max-trials", "0",
					"--worktree-base", worktreeDir,
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				record := readManifestRecord("prog-default")
				Expect(record).To(HaveKeyWithValue("program", "autoresearch"))
				Expect(record).To(HaveKeyWithValue("program_resolved", skillPath))
			})

			It("resolves --program <skill-name> via skills/<name>/SKILL.md", func() {
				skillPath := writeProgramSkill("custom-program", "another skill body")

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-named",
					"--max-trials", "0",
					"--worktree-base", worktreeDir,
					"--program", "custom-program",
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				record := readManifestRecord("prog-named")
				Expect(record).To(HaveKeyWithValue("program", "custom-program"))
				Expect(record).To(HaveKeyWithValue("program_resolved", skillPath))
			})

			It("rejects --program <skill-name> when skills/<name>/SKILL.md does not exist", func() {
				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-missing-skill",
					"--max-trials", "1",
					"--worktree-base", worktreeDir,
					"--program", "ghost-skill",
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("program"))
				Expect(err.Error()).To(ContainSubstring("ghost-skill"))
			})
		})

		Context("path resolution", func() {
			It("resolves --program <path> as an absolute file path", func() {
				adHocDir := filepath.Join(dataDir, "ad-hoc")
				Expect(os.MkdirAll(adHocDir, 0o755)).To(Succeed())
				adHocPath := filepath.Join(adHocDir, "program.md")
				Expect(os.WriteFile(adHocPath, []byte("ad-hoc program body"), 0o600)).To(Succeed())

				// A skill that looks like the default still must not
				// be picked up — the path form takes precedence.
				_ = writeProgramSkill("autoresearch", "ignored-skill-body")

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-abs-path",
					"--max-trials", "0",
					"--worktree-base", worktreeDir,
					"--program", adHocPath,
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				record := readManifestRecord("prog-abs-path")
				Expect(record).To(HaveKeyWithValue("program", adHocPath))
				Expect(record).To(HaveKeyWithValue("program_resolved", adHocPath))
			})

			It("rejects --program <path> when the file does not exist", func() {
				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-missing-path",
					"--max-trials", "1",
					"--worktree-base", worktreeDir,
					"--program", filepath.Join(dataDir, "missing", "program.md"),
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("program"))
			})

			It("treats values containing '/' as paths, not skill names", func() {
				_ = writeProgramSkill("autoresearch", "default skill body")

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-slash-form",
					"--max-trials", "1",
					"--worktree-base", worktreeDir,
					// "skills/autoresearch" without trailing SKILL.md is
					// a path (contains '/'), and as a path it does not
					// exist as a regular file → reject.
					"--program", "skills/autoresearch",
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("program"))
			})
		})

		Context("N12 de-dup against calling agent's always_active_skills", func() {
			It("logs de-dup and annotates program_resolved when the calling agent declares the program skill", func() {
				skillPath := writeProgramSkill("autoresearch", "shared skill body")
				callingAgent := writeCallingAgentManifest("planner-orchestrator",
					[]string{"pre-action", "autoresearch"})

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-dedup",
					"--max-trials", "0",
					"--worktree-base", worktreeDir,
					"--program", "autoresearch",
					"--calling-agent", callingAgent,
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				output := out.String()
				Expect(output).To(ContainSubstring("autoresearch: program skill 'autoresearch' already loaded by calling agent"))
				Expect(output).To(ContainSubstring("skipping re-injection"))

				record := readManifestRecord("prog-dedup")
				Expect(record).To(HaveKeyWithValue("program", "autoresearch"))
				resolved, _ := record["program_resolved"].(string)
				Expect(resolved).To(ContainSubstring(skillPath))
				Expect(resolved).To(ContainSubstring("deduplicated against calling agent"))
			})

			It("does NOT fire de-dup when the calling agent does not declare the program skill", func() {
				skillPath := writeProgramSkill("autoresearch", "skill body")
				callingAgent := writeCallingAgentManifest("solo-agent",
					[]string{"pre-action", "memory-keeper"})

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-no-dedup",
					"--max-trials", "0",
					"--worktree-base", worktreeDir,
					"--program", "autoresearch",
					"--calling-agent", callingAgent,
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				Expect(out.String()).NotTo(ContainSubstring("skipping re-injection"))
				record := readManifestRecord("prog-no-dedup")
				Expect(record).To(HaveKeyWithValue("program_resolved", skillPath))
			})

			It("does NOT fire de-dup when the program is supplied as a path even if the calling agent matches", func() {
				_ = writeProgramSkill("autoresearch", "skill body")
				callingAgent := writeCallingAgentManifest("path-orchestrator",
					[]string{"autoresearch"})

				adHocPath := filepath.Join(dataDir, "path-program.md")
				Expect(os.WriteFile(adHocPath, []byte("ad-hoc program"), 0o600)).To(Succeed())

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-path-no-dedup",
					"--max-trials", "0",
					"--worktree-base", worktreeDir,
					"--program", adHocPath,
					"--calling-agent", callingAgent,
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				Expect(out.String()).NotTo(ContainSubstring("skipping re-injection"))
				record := readManifestRecord("prog-path-no-dedup")
				Expect(record).To(HaveKeyWithValue("program_resolved", adHocPath))
			})

			It("ignores --calling-agent when the manifest cannot be loaded (best-effort de-dup)", func() {
				skillPath := writeProgramSkill("autoresearch", "skill body")

				err := runCmd("autoresearch", "run",
					"--surface", surface,
					"--run-id", "prog-bad-calling-agent",
					"--max-trials", "0",
					"--worktree-base", worktreeDir,
					"--program", "autoresearch",
					"--calling-agent", filepath.Join(dataDir, "no-such-manifest.json"),
					"--driver-script", noOpDriver,
					"--evaluator-script", noOpScorer,
				)
				Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

				Expect(out.String()).NotTo(ContainSubstring("skipping re-injection"))
				record := readManifestRecord("prog-bad-calling-agent")
				Expect(record).To(HaveKeyWithValue("program_resolved", skillPath))
			})
		})

		Context("preset programs", func() {
			It("ships skills/autoresearch-presets/planner-quality.md as a reference program", func() {
				_, thisFile, _, ok := runtime.Caller(0)
				Expect(ok).To(BeTrue())
				preset := filepath.Join(filepath.Dir(thisFile), "..", "..",
					"skills", "autoresearch-presets", "planner-quality.md")
				info, err := os.Stat(preset)
				Expect(err).NotTo(HaveOccurred(), "planner-quality.md preset must exist")
				Expect(info.Mode().IsRegular()).To(BeTrue())
			})

			It("ships skills/autoresearch-presets/perf-preserve-behaviour.md as a reference program", func() {
				_, thisFile, _, ok := runtime.Caller(0)
				Expect(ok).To(BeTrue())
				preset := filepath.Join(filepath.Dir(thisFile), "..", "..",
					"skills", "autoresearch-presets", "perf-preserve-behaviour.md")
				info, err := os.Stat(preset)
				Expect(err).NotTo(HaveOccurred(), "perf-preserve-behaviour.md preset must exist")
				Expect(info.Mode().IsRegular()).To(BeTrue())
			})
		})
	})

	Describe("live driver prompt synthesiser (Slice 1)", func() {
		// writePromptRecorderDriver returns a fixture driver script
		// path that records (a) the value of FLOWSTATE_AUTORESEARCH_PROMPT_FILE,
		// (b) the contents of the file at that path, and (c) the trial
		// counter env var, into a sentinel file under dataDir. The
		// fixture exits 0 without editing the surface so the trial
		// records `fixed-point-skipped` — the spec asserts on the
		// recorded file rather than the trial outcome.
		writePromptRecorderDriver := func(sentinelPath string) string {
			path := filepath.Join(dataDir, "prompt-recorder-driver.sh")
			body := fmt.Sprintf(`#!/usr/bin/env bash
set -eu
{
  echo "PROMPT_FILE_ENV=${FLOWSTATE_AUTORESEARCH_PROMPT_FILE:-MISSING}"
  echo "TRIAL_ENV=${FLOWSTATE_AUTORESEARCH_TRIAL:-MISSING}"
  echo "MAX_TURNS_ENV=${FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS:-MISSING}"
  echo "RUN_ID_ENV=${FLOWSTATE_AUTORESEARCH_RUN_ID:-MISSING}"
  echo "----PROMPT-START----"
  if [ -n "${FLOWSTATE_AUTORESEARCH_PROMPT_FILE:-}" ] && [ -f "${FLOWSTATE_AUTORESEARCH_PROMPT_FILE}" ]; then
    cat "${FLOWSTATE_AUTORESEARCH_PROMPT_FILE}"
  else
    echo "MISSING_PROMPT_FILE"
  fi
  echo "----PROMPT-END----"
} > %q
exit 0
`, sentinelPath)
			Expect(os.WriteFile(path, []byte(body), 0o755)).To(Succeed())
			return path
		}

		writeNoOpScorer := func() string {
			path := filepath.Join(dataDir, "noop-scorer.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\necho 0\n"), 0o755)).To(Succeed())
			return path
		}

		It("writes the synthesised prompt and exposes its path via FLOWSTATE_AUTORESEARCH_PROMPT_FILE", func() {
			sentinelPath := filepath.Join(dataDir, "driver-recorded.txt")
			driver := writePromptRecorderDriver(sentinelPath)
			scorer := writeNoOpScorer()

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "ld-slice1-prompt",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--worktree-base", filepath.Join(dataDir, "wt-ld-slice1"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			recorded, readErr := os.ReadFile(sentinelPath)
			Expect(readErr).NotTo(HaveOccurred(), "driver should have recorded the prompt env to the sentinel")
			body := string(recorded)

			// Env var is set and points at an existing file under
			// the worktree's `.autoresearch/` scratch dir.
			Expect(body).NotTo(ContainSubstring("PROMPT_FILE_ENV=MISSING"))
			Expect(body).To(ContainSubstring(filepath.Join(".autoresearch", "trial-1-prompt.txt")))
			Expect(body).To(ContainSubstring("TRIAL_ENV=1"))
			Expect(body).To(ContainSubstring("RUN_ID_ENV=ld-slice1-prompt"))
			// max-turns default of 10 is propagated.
			Expect(body).To(ContainSubstring("MAX_TURNS_ENV=10"))

			// Section markers appear in fixed order (R1.5).
			programIdx := strings.Index(body, "# PROGRAM")
			surfaceIdx := strings.Index(body, "# SURFACE")
			historyIdx := strings.Index(body, "# HISTORY")
			instructionIdx := strings.Index(body, "# INSTRUCTION")
			Expect(programIdx).To(BeNumerically(">", 0), "# PROGRAM marker must appear")
			Expect(surfaceIdx).To(BeNumerically(">", programIdx), "# SURFACE must follow # PROGRAM")
			Expect(historyIdx).To(BeNumerically(">", surfaceIdx), "# HISTORY must follow # SURFACE")
			Expect(instructionIdx).To(BeNumerically(">", historyIdx), "# INSTRUCTION must follow # HISTORY")

			// First-trial history is the literal placeholder.
			Expect(body).To(ContainSubstring("(no prior trials)"))

			// Surface section quotes the surface path verbatim and
			// embeds the full surface contents.
			Expect(body).To(ContainSubstring("internal/app/agents/planner.md"))
			Expect(body).To(ContainSubstring("planner body"))

			// Instruction section pins the fenced-block contract.
			Expect(body).To(ContainSubstring("```surface"))
		})

		It("records prompt_file and prompt_sha on the trial record", func() {
			sentinelPath := filepath.Join(dataDir, "driver-recorded-sha.txt")
			driver := writePromptRecorderDriver(sentinelPath)
			scorer := writeNoOpScorer()

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "ld-slice1-sha",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--worktree-base", filepath.Join(dataDir, "wt-ld-slice1-sha"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())

			trialKey := "autoresearch/ld-slice1-sha/trial-1"
			val, ok := entries[trialKey]
			Expect(ok).To(BeTrue(), "trial-1 record expected at %s", trialKey)
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())

			Expect(record).To(HaveKey("prompt_file"))
			Expect(record).To(HaveKey("prompt_sha"))
			promptSHA, _ := record["prompt_sha"].(string)
			Expect(promptSHA).To(MatchRegexp(`^[a-f0-9]{64}$`), "prompt_sha must be a sha-256 hex string")
		})

		It("records driver_mode, driver_script, and prompt_history_window on the manifest record", func() {
			sentinelPath := filepath.Join(dataDir, "driver-recorded-manifest.txt")
			driver := writePromptRecorderDriver(sentinelPath)
			scorer := writeNoOpScorer()

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "ld-slice1-manifest",
				"--max-trials", "0",
				"--worktree-base", filepath.Join(dataDir, "wt-ld-slice1-manifest"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
				"--prompt-history-window", "7",
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			record := readManifestRecord("ld-slice1-manifest")
			Expect(record).To(HaveKeyWithValue("driver_mode", "script"))
			Expect(record).To(HaveKeyWithValue("driver_script", driver))
			Expect(record).To(HaveKey("driver_timeout_ms"))
			Expect(record).To(HaveKeyWithValue("prompt_history_window", float64(7)))
		})

		It("BuildDriverPrompt is deterministic and emits the four section markers in order", func() {
			programBody := "# Program\n\nDo not break X. Do not regress Y."
			surfacePath := "internal/app/agents/planner.md"
			surfaceBytes := []byte("---\nid: planner\n---\nplanner body\n")

			out1, err := cli.BuildDriverPrompt(programBody, surfacePath, surfaceBytes, nil, 0)
			Expect(err).NotTo(HaveOccurred())
			out2, err := cli.BuildDriverPrompt(programBody, surfacePath, surfaceBytes, nil, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(out1).To(Equal(out2), "synthesiser must be deterministic on identical inputs")

			body := string(out1)
			Expect(body).To(ContainSubstring("# PROGRAM"))
			Expect(body).To(ContainSubstring("# SURFACE"))
			Expect(body).To(ContainSubstring("# HISTORY"))
			Expect(body).To(ContainSubstring("# INSTRUCTION"))
			Expect(strings.Index(body, "# PROGRAM")).To(BeNumerically("<", strings.Index(body, "# SURFACE")))
			Expect(strings.Index(body, "# SURFACE")).To(BeNumerically("<", strings.Index(body, "# HISTORY")))
			Expect(strings.Index(body, "# HISTORY")).To(BeNumerically("<", strings.Index(body, "# INSTRUCTION")))
		})
	})

	Describe("default-assistant driver script (Slice 2)", func() {
		// repoDriverPath returns the absolute path to the
		// default-assistant-driver.sh shipped under
		// scripts/autoresearch-drivers/. Resolved via runtime.Caller
		// so the spec is independent of the test runner's cwd.
		repoDriverPath := func() string {
			_, thisFile, _, ok := runtime.Caller(0)
			Expect(ok).To(BeTrue())
			path := filepath.Join(filepath.Dir(thisFile), "..", "..",
				"scripts", "autoresearch-drivers", "default-assistant-driver.sh")
			abs, err := filepath.Abs(path)
			Expect(err).NotTo(HaveOccurred())
			info, statErr := os.Stat(abs)
			Expect(statErr).NotTo(HaveOccurred(), "default-assistant-driver.sh must exist at %s", abs)
			Expect(info.Mode()&0o100).NotTo(BeZero(), "driver must be executable")
			return abs
		}

		writeNoOpScorer := func() string {
			path := filepath.Join(dataDir, "noop-scorer-slice2.sh")
			Expect(os.WriteFile(path, []byte("#!/usr/bin/env bash\necho 0\n"), 0o755)).To(Succeed())
			return path
		}

		// validManifestSurface produces a fenced-block response whose
		// body is a structurally-valid planner manifest (so the
		// manifest gate passes and the trial reaches scoring rather
		// than dying at validation).
		validManifestSurface := func(marker string) string {
			body := fmt.Sprintf(`---
schema_version: "1"
id: planner
name: Planner
complexity: standard
metadata:
  role: %s
capabilities:
  tools: [read, plan]
---
planner body %s
`, marker, marker)
			return "Here is the next candidate:\n\n```surface\n" + body + "```\n"
		}

		It("applies the agent's fenced-surface block and produces a non-fixed-point trial", func() {
			driver := repoDriverPath()
			scorer := writeNoOpScorer()

			// Canned response file the driver reads via its
			// FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT escape hatch.
			responseFile := filepath.Join(dataDir, "canned-response.txt")
			Expect(os.WriteFile(responseFile, []byte(validManifestSurface("slice2-applied")), 0o600)).To(Succeed())

			Expect(os.Setenv("FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT", responseFile)).To(Succeed())
			DeferCleanup(func() { _ = os.Unsetenv("FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT") })

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "ld-slice2-applied",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--worktree-base", filepath.Join(dataDir, "wt-ld-slice2-applied"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			// The trial must NOT be fixed-point-skipped — the driver
			// changed the surface, so the candidate SHA differs from
			// the baseline. The MVP DoD's pass criterion (plan § 5.3)
			// is exactly this: at least one trial escapes
			// fixed-point-skipped.
			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())

			val, ok := entries["autoresearch/ld-slice2-applied/trial-1"]
			Expect(ok).To(BeTrue(), "trial-1 record expected")
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())

			Expect(record).NotTo(HaveKeyWithValue("reason", "fixed-point-skipped"),
				"driver applied an edit; the trial must not be fixed-point-skipped")
			// Fenced-block parser dropped the trailing newline; the
			// surface-bytes hash is reflected in candidate_sha.
			Expect(record).To(HaveKey("candidate_sha"))
		})

		It("records validator-io-error when the agent emits no fenced block", func() {
			driver := repoDriverPath()
			scorer := writeNoOpScorer()

			responseFile := filepath.Join(dataDir, "no-fenced-block-response.txt")
			Expect(os.WriteFile(responseFile, []byte("I cannot produce a fenced block, sorry.\n"), 0o600)).To(Succeed())

			Expect(os.Setenv("FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT", responseFile)).To(Succeed())
			DeferCleanup(func() { _ = os.Unsetenv("FLOWSTATE_AUTORESEARCH_DRIVER_OUTPUT") })

			err := runCmd("autoresearch", "run",
				"--surface", surface,
				"--run-id", "ld-slice2-no-block",
				"--max-trials", "1",
				"--time-budget", "30s",
				"--metric-direction", "min",
				"--worktree-base", filepath.Join(dataDir, "wt-ld-slice2-no-block"),
				"--driver-script", driver,
				"--evaluator-script", scorer,
			)
			Expect(err).NotTo(HaveOccurred(), "out: %s", out.String())

			raw, err := os.ReadFile(coordPath)
			Expect(err).NotTo(HaveOccurred())
			var entries map[string]string
			Expect(json.Unmarshal(raw, &entries)).To(Succeed())

			val, ok := entries["autoresearch/ld-slice2-no-block/trial-1"]
			Expect(ok).To(BeTrue(), "trial-1 record expected")
			var record map[string]any
			Expect(json.Unmarshal([]byte(val), &record)).To(Succeed())

			// Driver exit-3 (driver-no-edit-produced) collapses onto
			// validator-io-error per plan § 4.5.
			Expect(record).To(HaveKeyWithValue("reason", "validator-io-error"))
			Expect(record).To(HaveKeyWithValue("kept", false))
		})
	})
})

// jsonStringSlice renders a Go []string as a JSON array literal for
// inline manifest fixtures.
func jsonStringSlice(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
