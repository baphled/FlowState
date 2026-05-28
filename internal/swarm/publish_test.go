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

			path, err := swarm.PublishPlanToVault(store, outputDir, "")
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

			path, err := swarm.PublishPlanToVault(store, outputDir, "")
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

			path, err := swarm.PublishPlanToVault(store, outputDir, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(outputDir, "chain-fallback-123.md")))
		})
	})

	Context("idempotency", func() {
		It("overwrites the same file cleanly when the same plan is re-published", func() {
			store := newGateStore(map[string][]byte{
				"repeat-chain/plan": []byte("# Repeatable Plan\n\nv1 body."),
			})

			first, err := swarm.PublishPlanToVault(store, outputDir, "")
			Expect(err).NotTo(HaveOccurred())

			// Same title → same slug → same file. Update the body and
			// re-publish; the file must be overwritten, not duplicated.
			Expect(store.Set("repeat-chain/plan", []byte("# Repeatable Plan\n\nv2 body."))).To(Succeed())
			second, err := swarm.PublishPlanToVault(store, outputDir, "")
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
			_, err := swarm.PublishPlanToVault(store, outputDir, "")
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
			path, err := swarm.PublishPlanToVault(store, outputDir, "")
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
			path, err := swarm.PublishPlanToVault(store, outputDir, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(BeEmpty())

			entries, _ := os.ReadDir(outputDir)
			Expect(entries).To(BeEmpty())
		})

		It("does nothing when no output dir is configured", func() {
			store := newGateStore(map[string][]byte{
				"some-chain/plan": []byte("# A Plan\n\nbody"),
			})
			path, err := swarm.PublishPlanToVault(store, "", "")
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
			path, err := swarm.PublishPlanToVault(store, outputDir, "")
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
			path, err := swarm.PublishPlanToVault(store, outputDir, "")
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
			path, err := swarm.PublishPlanToVault(store, unwritable, "")
			Expect(err).To(HaveOccurred(), "a write failure must be surfaced, not swallowed")
			Expect(path).To(BeEmpty())

			_, ok := readPublication(store, "fail-chain")
			Expect(ok).To(BeFalse(), "no publication record on a failed write")
		})
	})

	Context("targeted chainID resolution (Bug 1: wrong-chain targeting)", func() {
		It("publishes the NAMED chain, not an alphabetically-earlier one, in a multi-chain store", func() {
			// REGRESSION (Bug 1): the live coord-store carries many "*/plan"
			// keys across historical chains. A suffix-scan returns an
			// arbitrary (Go-map-iteration-order) stale chain, NOT the run's
			// plan. With an explicit chainID the publisher must read
			// "<chainID>/plan" directly and ignore every other chain.
			store := newGateStore(map[string][]byte{
				"3b1b4d8e-stale-chain/plan":      []byte("# Stale Plan\n\nDo not publish me."),
				"aaaa-earlier-chain/plan":        []byte("# Earlier Plan\n\nAlso wrong."),
				"mental-health-companion/plan":   []byte("# Mental Health Companion\n\nThe one the user wants."),
				"mental-health-companion/review": []byte(`{"verdict":"approve","confidence":0.95}`),
				"zzzz-later-chain/plan":          []byte("# Later Plan\n\nStill wrong."),
			})

			path, err := swarm.PublishPlanToVault(store, outputDir, "mental-health-companion")
			Expect(err).NotTo(HaveOccurred())

			expected := filepath.Join(outputDir, "mental-health-companion.md")
			Expect(path).To(Equal(expected), "the named chain's plan is published, not a stale one")

			body, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("The one the user wants."))
			Expect(string(body)).NotTo(ContainSubstring("Do not publish me."))
			Expect(string(body)).NotTo(ContainSubstring("Also wrong."))

			recorded, ok := readPublication(store, "mental-health-companion")
			Expect(ok).To(BeTrue(), "the record is written under the NAMED chain")
			Expect(recorded).To(Equal(path))

			// The stale chains must NOT receive a publication record.
			_, staleOK := readPublication(store, "3b1b4d8e-stale-chain")
			Expect(staleOK).To(BeFalse(), "no record fabricated against a stale chain")
		})

		It("honours the named chain's review approve-gate, not another chain's", func() {
			// The named chain is approved; a different chain is rejected.
			// Targeting must read the NAMED chain's review.
			store := newGateStore(map[string][]byte{
				"rejected-other/plan":    []byte("# Rejected Other\n\nbody"),
				"rejected-other/review":  []byte(`{"verdict":"reject"}`),
				"approved-target/plan":   []byte("# Approved Target\n\nbody"),
				"approved-target/review": []byte(`{"verdict":"approve"}`),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir, "approved-target")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(outputDir, "approved-target.md")))
		})

		It("is a no-op when the NAMED chain's review explicitly rejects", func() {
			store := newGateStore(map[string][]byte{
				"approved-other/plan":    []byte("# Approved Other\n\nbody"),
				"approved-other/review":  []byte(`{"verdict":"approve"}`),
				"rejected-target/plan":   []byte("# Rejected Target\n\nbody"),
				"rejected-target/review": []byte(`{"verdict":"reject"}`),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir, "rejected-target")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(BeEmpty(), "a rejected named chain is not published")
		})

		It("is a no-op when the NAMED chain has no plan key (does not fall back to suffix-scan)", func() {
			store := newGateStore(map[string][]byte{
				"some-other-chain/plan": []byte("# Other\n\nbody"),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir, "absent-chain")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(BeEmpty(), "a missing named-chain plan must NOT publish a different chain")
			entries, _ := os.ReadDir(outputDir)
			Expect(entries).To(BeEmpty())
		})
	})

	Context("structured-JSON plan body (Bug 2: JSON published raw)", func() {
		It("renders a structured-JSON plan to readable markdown, not raw JSON", func() {
			// REGRESSION (Bug 2): the live mental-health-companion plan body
			// is a structured JSON object (NOT the {markdown:...} envelope and
			// NOT markdown). Publishing it raw dumps JSON into the vault.
			jsonBody := `{` +
				`"purpose":"A supportive mental-health companion.",` +
				`"responsibilities":["Listen actively","Offer coping strategies","Signpost to professionals"],` +
				`"boundaries":{"must_not":["Diagnose conditions","Replace a clinician"]}` +
				`}`
			store := newGateStore(map[string][]byte{
				"mhc-2026-05-27/plan":   []byte(jsonBody),
				"mhc-2026-05-27/review": []byte(`{"verdict":"approve"}`),
			})

			path, err := swarm.PublishPlanToVault(store, outputDir, "mhc-2026-05-27")
			Expect(err).NotTo(HaveOccurred())

			body, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			rendered := string(body)

			// No raw JSON punctuation leaking into the vault.
			Expect(rendered).NotTo(ContainSubstring(`"purpose"`),
				"the file must hold rendered markdown, not raw JSON keys")
			Expect(rendered).NotTo(ContainSubstring(`"must_not"`))

			// Top-level keys become title-cased headings.
			Expect(rendered).To(ContainSubstring("## Purpose"))
			Expect(rendered).To(ContainSubstring("A supportive mental-health companion."))
			Expect(rendered).To(ContainSubstring("## Responsibilities"))
			// Array of strings → bullet list.
			Expect(rendered).To(ContainSubstring("- Listen actively"))
			Expect(rendered).To(ContainSubstring("- Offer coping strategies"))
			// Nested object → subheading + its fields.
			Expect(rendered).To(ContainSubstring("## Boundaries"))
			Expect(rendered).To(ContainSubstring("### Must Not"))
			Expect(rendered).To(ContainSubstring("- Diagnose conditions"))
		})

		It("renders deterministically (top-level key order is stable across runs)", func() {
			jsonBody := `{"alpha":"first","beta":"second","gamma":"third"}`
			store := newGateStore(map[string][]byte{
				"det-chain/plan": []byte(jsonBody),
			})
			first, err := swarm.PublishPlanToVault(store, outputDir, "det-chain")
			Expect(err).NotTo(HaveOccurred())
			body1, _ := os.ReadFile(first)

			second, err := swarm.PublishPlanToVault(store, outputDir, "det-chain")
			Expect(err).NotTo(HaveOccurred())
			body2, _ := os.ReadFile(second)

			Expect(string(body2)).To(Equal(string(body1)), "rendering is byte-stable")
		})

		It("derives the title from the JSON title/name/purpose for the slug", func() {
			jsonBody := `{"name":"Companion Charter","purpose":"Be kind."}`
			store := newGateStore(map[string][]byte{
				"name-chain/plan": []byte(jsonBody),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir, "name-chain")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(outputDir, "companion-charter.md")),
				"the JSON name field seeds the filename slug")
		})

		It("still handles the {markdown:...} envelope (existing behaviour preserved)", func() {
			store := newGateStore(map[string][]byte{
				"env-chain/plan": []byte(`{"markdown":"# Envelope Plan\n\nMd body.","title":"Envelope Plan"}`),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir, "env-chain")
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(outputDir, "envelope-plan.md")))
			body, _ := os.ReadFile(path)
			Expect(string(body)).To(Equal("# Envelope Plan\n\nMd body."))
		})

		It("still handles a raw-markdown body (existing behaviour preserved)", func() {
			store := newGateStore(map[string][]byte{
				"raw-chain/plan": []byte("# Raw Plan\n\nPlain markdown."),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir, "raw-chain")
			Expect(err).NotTo(HaveOccurred())
			body, _ := os.ReadFile(path)
			Expect(string(body)).To(Equal("# Raw Plan\n\nPlain markdown."))
		})

		It("prefers a <chainID>/plan-markdown key over the structured-JSON plan body", func() {
			jsonBody := `{"purpose":"JSON form","responsibilities":["a","b"]}`
			store := newGateStore(map[string][]byte{
				"pmd-chain/plan":          []byte(jsonBody),
				"pmd-chain/plan-markdown": []byte("# Curated Markdown\n\nThis curated body wins."),
			})
			path, err := swarm.PublishPlanToVault(store, outputDir, "pmd-chain")
			Expect(err).NotTo(HaveOccurred())
			body, _ := os.ReadFile(path)
			Expect(string(body)).To(Equal("# Curated Markdown\n\nThis curated body wins."),
				"plan-markdown takes precedence over the structured-JSON render")
			Expect(string(body)).NotTo(ContainSubstring("JSON form"))
		})
	})

	Context("integration with the artifact-published honesty gate", func() {
		It("verifies the threaded chain's publication when a chainID is supplied", func() {
			// Gate alignment (Bug 1): when a chainID is threaded, both the
			// publisher and the gate target the SAME chain. A multi-chain
			// store must not let the gate verify a stale chain.
			store := newGateStore(map[string][]byte{
				"stale-gate-chain/plan":  []byte("# Stale\n\nbody"),
				"target-gate-chain/plan": []byte(`{"markdown":"# Target Gate Plan\n\nbody","title":"Target Gate Plan"}`),
			})

			publishedPath, err := swarm.PublishPlanToVault(store, outputDir, "target-gate-chain")
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
				ChainID:    "target-gate-chain",
				CoordStore: store,
			}
			runner := swarm.NewArtifactPublishedRunner(outputDir, nil)
			Expect(runner.Run(nil, gate, args)).To(Succeed(),
				"the gate verifies the threaded chain's real publication")
		})

		It("publishes a real file the gate then verifies and passes", func() {
			// End-to-end: publish writes the plan + record, then the gate
			// (run with empty ChainID as the post-swarm dispatch does)
			// verifies the file exists under the output dir and passes.
			store := newGateStore(map[string][]byte{
				"e2e-chain/plan": []byte(`{"markdown":"# E2E Plan\n\nbody","title":"E2E Plan"}`),
			})

			publishedPath, err := swarm.PublishPlanToVault(store, outputDir, "")
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
