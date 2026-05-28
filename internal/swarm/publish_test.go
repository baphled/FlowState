package swarm_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
)

// readPublication decodes the "<chainID>/plan_publication" record the
// publisher writes so specs can assert the recorded path matches the file
// actually written.
func readPublication(store coordination.Store, chainID string) (vaultPath string, ok bool) {
	raw, err := store.Get(chainID + "/plan_publication")
	if err != nil {
		return "", false
	}
	var rec struct {
		VaultPath   string `json:"vault_path"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", false
	}
	return rec.VaultPath, true
}

var _ = Describe("PublishPlanToVault (deterministic post-swarm publisher)", func() {
	var outputDir string

	BeforeEach(func() {
		outputDir = GinkgoT().TempDir()
	})

	Context("with an approved JSON-envelope plan (the live planning-loop shape)", func() {
		It("writes the markdown body to a title-slugged file and records the real path", func() {
			envelope := `{"markdown":"# Add /readyz Readiness Endpoint\n\nBody text.","id":"readyz-2026-05-28","title":"Add /readyz Readiness Endpoint"}`
			store := newGateStore(map[string][]byte{
				"readyz-2026-05-28/plan":   []byte(envelope),
				"readyz-2026-05-28/review": []byte(`{"verdict":"approve","confidence":0.9}`),
			})

			path, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())

			expected := filepath.Join(outputDir, "add-readyz-readiness-endpoint.md")
			Expect(path).To(Equal(expected), "filename is the slugified envelope title")

			body, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("# Add /readyz Readiness Endpoint"))
			Expect(string(body)).To(ContainSubstring("Body text."))
			Expect(string(body)).NotTo(ContainSubstring(`"markdown"`),
				"the file holds the markdown body, not the JSON envelope")

			recorded, ok := readPublication(store, "readyz-2026-05-28")
			Expect(ok).To(BeTrue(), "a publication record must be written")
			Expect(recorded).To(Equal(path), "the record points at the real path just written")
		})
	})

	Context("with a raw-markdown plan (no JSON envelope)", func() {
		It("treats the raw value as the body and slugs from the first H1", func() {
			store := newGateStore(map[string][]byte{
				"auth-hardening/plan": []byte("# Auth Hardening Plan\n\nDetails here."),
			})

			path, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(outputDir, "auth-hardening-plan.md")))

			body, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(body)).To(Equal("# Auth Hardening Plan\n\nDetails here."))
		})
	})

	Context("when neither title nor H1 is available", func() {
		It("falls back to the chainID for the filename", func() {
			store := newGateStore(map[string][]byte{
				"chain-fallback-123/plan": []byte("Just a body with no heading at all."),
			})

			path, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(outputDir, "chain-fallback-123.md")))
		})
	})

	Context("idempotency", func() {
		It("overwrites the same file cleanly when the same plan is re-published", func() {
			store := newGateStore(map[string][]byte{
				"repeat-chain/plan": []byte("# Repeatable Plan\n\nv1 body."),
			})

			first, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())

			// Same title → same slug → same file. Update the body and
			// re-publish; the file must be overwritten, not duplicated.
			Expect(store.Set("repeat-chain/plan", []byte("# Repeatable Plan\n\nv2 body."))).To(Succeed())
			second, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(second).To(Equal(first), "the same plan title yields the same filename")

			entries, readErr := os.ReadDir(outputDir)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1), "re-publishing must not leave a partial/temp file behind")

			body, readErr := os.ReadFile(second)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("v2 body."), "the file reflects the latest plan")
		})

		It("leaves no temp litter on a successful write", func() {
			store := newGateStore(map[string][]byte{
				"clean-chain/plan": []byte("# Clean Write\n\nbody"),
			})
			_, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())

			entries, readErr := os.ReadDir(outputDir)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Name()).To(HaveSuffix(".md"),
				"only the final .md file remains; no .tmp temp file")
		})
	})

	Context("honest no-ops (no fabricated record)", func() {
		It("does nothing when the plan key is missing", func() {
			store := newGateStore(map[string][]byte{})
			path, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(BeEmpty())

			entries, _ := os.ReadDir(outputDir)
			Expect(entries).To(BeEmpty(), "no file written")
			_, ok := readPublication(store, "missing-chain")
			Expect(ok).To(BeFalse(), "no fabricated publication record")
		})

		It("does nothing when the plan key is present but empty", func() {
			store := newGateStore(map[string][]byte{
				"empty-chain/plan": []byte("   "),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(BeEmpty())

			entries, _ := os.ReadDir(outputDir)
			Expect(entries).To(BeEmpty())
		})

		It("does nothing when no output dir is configured", func() {
			store := newGateStore(map[string][]byte{
				"some-chain/plan": []byte("# A Plan\n\nbody"),
			})
			path, err := swarm.PublishPlanToVault(store, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(BeEmpty())
			_, ok := readPublication(store, "some-chain")
			Expect(ok).To(BeFalse())
		})

		It("does not publish a plan the reviewer explicitly rejected", func() {
			store := newGateStore(map[string][]byte{
				"rejected-chain/plan":   []byte("# Rejected Plan\n\nbody"),
				"rejected-chain/review": []byte(`{"verdict":"reject","confidence":0.8}`),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(BeEmpty(), "a rejected plan must not reach the vault")

			entries, _ := os.ReadDir(outputDir)
			Expect(entries).To(BeEmpty())
			_, ok := readPublication(store, "rejected-chain")
			Expect(ok).To(BeFalse())
		})

		It("publishes when the review record is absent (post-member gate already ran)", func() {
			store := newGateStore(map[string][]byte{
				"no-review-chain/plan": []byte("# No Review Recorded\n\nbody"),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(path).NotTo(BeEmpty(), "an absent review does not block publication")
		})
	})

	Context("write failure", func() {
		It("returns an error and leaves no partial file when the output dir is unwritable", func() {
			// Point the output dir at a path whose parent is a FILE, so
			// MkdirAll cannot create it — the publish must surface the
			// error (so the post-swarm gate then fails) rather than
			// swallowing it.
			blocker := filepath.Join(outputDir, "not-a-dir")
			Expect(os.WriteFile(blocker, []byte("x"), 0o644)).To(Succeed())
			unwritable := filepath.Join(blocker, "plans")

			store := newGateStore(map[string][]byte{
				"fail-chain/plan": []byte("# Will Fail\n\nbody"),
			})
			path, err := swarm.PublishPlanToVault(store, unwritable)
			Expect(err).To(HaveOccurred(), "a write failure must be surfaced, not swallowed")
			Expect(path).To(BeEmpty())

			_, ok := readPublication(store, "fail-chain")
			Expect(ok).To(BeFalse(), "no publication record on a failed write")
		})
	})

	Context("integration with the artifact-published honesty gate", func() {
		It("publishes a real file the gate then verifies and passes", func() {
			// End-to-end: publish writes the plan + record, then the gate
			// (run with empty ChainID as the post-swarm dispatch does)
			// verifies the file exists under the output dir and passes.
			store := newGateStore(map[string][]byte{
				"e2e-chain/plan": []byte(`{"markdown":"# E2E Plan\n\nbody","title":"E2E Plan"}`),
			})

			publishedPath, err := swarm.PublishPlanToVault(store, outputDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(publishedPath).To(BeAnExistingFile())

			gate := swarm.GateSpec{
				Name:      "post-swarm-plan-published",
				Kind:      "builtin:artifact-published",
				When:      "post",
				OutputKey: "{chainID}/plan",
			}
			args := swarm.GateArgs{
				SwarmID:    "planning-loop",
				CoordStore: store,
				// Empty ChainID — the post-swarm dispatch path. Both the
				// publisher and the gate resolve by suffix-scan, so the
				// gate finds the record the publisher just wrote.
			}
			runner := swarm.NewArtifactPublishedRunner(outputDir, nil)
			Expect(runner.Run(nil, gate, args)).To(Succeed(),
				"the gate passes only because the publisher wrote a real file")
		})
	})
})
