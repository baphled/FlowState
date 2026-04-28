package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	})
})
