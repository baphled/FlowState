package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin the surface and safety contract of `flowstate
// coordination prune`: dry-run by default, --apply required to delete,
// orphan classification heuristics, --prefix narrowing,
// --include-chain-format opt-in destruction of chain-<nano> keys, and
// --older-than gating on file mtime. The missing-file path is also
// pinned as a clean no-op so fresh installs don't error.
//
// The test never passes any persistent CLI flag (--agents-dir,
// --config etc.) — those would trigger initApp's app.New() reinit
// which depends on host config (Anthropic OAuth, etc.) that is
// unrelated to coord-store GC and would fail spuriously on
// developer machines without authentic provider creds.
var _ = Describe("coordination prune command", func() {
	var (
		out       *bytes.Buffer
		testApp   *app.App
		dataDir   string
		runCmd    func(args ...string) error
		coordPath string
	)

	writeStore := func(entries map[string]string) {
		raw, err := json.MarshalIndent(entries, "", "  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(coordPath, raw, 0o600)).To(Succeed())
	}

	readStore := func() map[string]string {
		raw, err := os.ReadFile(coordPath)
		Expect(err).NotTo(HaveOccurred())
		var entries map[string]string
		Expect(json.Unmarshal(raw, &entries)).To(Succeed())
		return entries
	}

	BeforeEach(func() {
		out = &bytes.Buffer{}
		dataDir = GinkgoT().TempDir()
		coordPath = filepath.Join(dataDir, "coordination.json")

		var err error
		testApp, err = app.NewForTest(app.TestConfig{DataDir: dataDir})
		Expect(err).NotTo(HaveOccurred())

		runCmd = func(args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(args)
			return root.Execute()
		}
	})

	Context("when the coord-store file does not exist", func() {
		It("exits cleanly with a no-op message", func() {
			err := runCmd("coordination", "prune")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No coordination store"))
			Expect(out.String()).To(ContainSubstring("0 orphan"))
		})
	})

	Context("when the coord-store contains a mix of orphan and chain-format keys", func() {
		BeforeEach(func() {
			writeStore(map[string]string{
				"chain-1713898000000000000/plan":       "legit-plan-body",
				"chain-1713898000000000000/review":     "legit-review",
				"chainID":                              "stranded-top-level-key",
				"flowstate/codebase-findings":          strings.Repeat("x", 1024),
				"health-endpoint-plan/interview":       "ad-hoc-1",
				"cli-sessions-prune/codebase-findings": "ad-hoc-2",
			})
		})

		It("reports orphans without writing on dry-run (default)", func() {
			err := runCmd("coordination", "prune")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("dry-run"))
			Expect(output).To(ContainSubstring("chainID"))
			Expect(output).To(ContainSubstring("flowstate/codebase-findings"))
			Expect(output).To(ContainSubstring("health-endpoint-plan/interview"))
			Expect(output).To(ContainSubstring("Summary:"))
			Expect(output).To(ContainSubstring("4 orphan"))

			// Nothing deleted on dry-run.
			after := readStore()
			Expect(after).To(HaveLen(6))
			Expect(after).To(HaveKey("chainID"))
			Expect(after).To(HaveKey("flowstate/codebase-findings"))
		})

		It("excludes legitimate chain-<nano> keys from the orphan set by default", func() {
			err := runCmd("coordination", "prune")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).NotTo(ContainSubstring("chain-1713898000000000000/plan"),
				"chain-<unixnano> keys are the canonical delegation namespace; "+
					"they must NOT be classified as orphans by default")
			Expect(output).NotTo(ContainSubstring("chain-1713898000000000000/review"))
		})

		It("classifies orphans by reason in the report", func() {
			err := runCmd("coordination", "prune")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("no-chain-prefix"),
				"top-level keys (no `/` separator) are no-chain-prefix orphans")
			Expect(output).To(ContainSubstring("flowstate-namespace"),
				"`flowstate/...` keys are explicit contract violations")
			Expect(output).To(ContainSubstring("non-chain-prefix"),
				"keys with a `/` separator whose prefix is not `chain-<digits>` are non-chain-prefix orphans")
		})

		It("--apply actually deletes orphans and preserves chain-format keys", func() {
			err := runCmd("coordination", "prune", "--apply")
			Expect(err).NotTo(HaveOccurred())

			Expect(out.String()).NotTo(ContainSubstring("dry-run"))
			Expect(out.String()).To(ContainSubstring("4 orphan"))

			after := readStore()
			Expect(after).To(HaveLen(2))
			Expect(after).To(HaveKey("chain-1713898000000000000/plan"))
			Expect(after).To(HaveKey("chain-1713898000000000000/review"))
			Expect(after).NotTo(HaveKey("chainID"))
			Expect(after).NotTo(HaveKey("flowstate/codebase-findings"))
		})

		It("--prefix narrows the orphan set to the named prefix", func() {
			err := runCmd("coordination", "prune", "--prefix", "flowstate/")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("flowstate/codebase-findings"))
			Expect(output).To(ContainSubstring("1 orphan"))
			Expect(output).NotTo(ContainSubstring("health-endpoint-plan/interview"),
				"keys outside the prefix must be excluded from the report")
			Expect(output).NotTo(ContainSubstring("Summary: 4"),
				"the summary count must reflect the prefix-narrowed set, not the full store")
		})
	})

	Context("when --include-chain-format is set", func() {
		BeforeEach(func() {
			writeStore(map[string]string{
				"chain-1713898000000000000/plan":   "should-be-swept",
				"chain-1713898000000000000/review": "should-be-swept",
				"chainID":                          "stranded-top-level",
			})
		})

		It("classifies chain-<nano> keys as orphans for sweeping", func() {
			err := runCmd("coordination", "prune", "--include-chain-format")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(ContainSubstring("chain-1713898000000000000/plan"),
				"--include-chain-format must surface chain-<nano> keys")
			Expect(output).To(ContainSubstring("chain-1713898000000000000/review"))
			Expect(output).To(ContainSubstring("chain-format"),
				"chain-format reason classifier must appear in the destructive sweep")
			Expect(output).To(ContainSubstring("3 orphan"))
		})
	})

	Context("when --older-than is set", func() {
		BeforeEach(func() {
			writeStore(map[string]string{"chainID": "v"})
		})

		It("performs the run when the file mtime exceeds the threshold", func() {
			// Backdate the file mtime to 1 hour ago.
			past := time.Now().Add(-1 * time.Hour)
			Expect(os.Chtimes(coordPath, past, past)).To(Succeed())

			err := runCmd("coordination", "prune", "--older-than", "30m")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("1 orphan"))
		})

		It("skips the run when the file mtime is within the threshold", func() {
			err := runCmd("coordination", "prune", "--older-than", "1h")
			Expect(err).NotTo(HaveOccurred())

			output := out.String()
			Expect(output).To(SatisfyAny(
				ContainSubstring("skipped"),
				ContainSubstring("recent"),
				ContainSubstring("not run"),
			), "the report must indicate the run was gated and not performed")
			Expect(output).NotTo(ContainSubstring("0 orphan"),
				"a gated run must NOT report a count — it didn't classify anything")
		})
	})
})
