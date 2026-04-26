// Package agent_test — H3 audit coverage for per-agent
// CompactionThreshold range validation.
//
// The manifest field has always been declared but never read; H3
// wires it into engine.autoCompactionThreshold as a per-agent
// override. For that override to be safe the loader must range-
// validate the field on load — same (0, 1] rule the global
// auto-compaction threshold is held to.
package agent_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

// writeManifestWithThreshold writes a minimal manifest JSON with the
// supplied compaction_threshold value to a fresh temp directory and
// returns the path. Centralised because every spec below shares this
// fixture-build shape.
func writeManifestWithThreshold(value any) string {
	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "m.json")
	body := map[string]any{
		"id":   "agent-test",
		"name": "Agent under test",
		"context_management": map[string]any{
			"compaction_threshold": value,
		},
	}
	data, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred(), "marshal")
	Expect(os.WriteFile(path, data, 0o600)).To(Succeed(), "write")
	return path
}

var _ = Describe("LoadManifestJSON CompactionThreshold range validation (H3)", func() {
	// RejectsOutOfRange covers both edges of the (0, 1] rule: a
	// negative value (ratios are inherently non-negative) and a value
	// above one (the budget IS the token budget — there is no load
	// above it to measure). The loader must refuse, surfacing at load
	// time so the operator sees a loud error with the file path
	// rather than a silent slide through runtime.
	DescribeTable("rejects out-of-range thresholds with an error mentioning compaction_threshold",
		func(value any) {
			path := writeManifestWithThreshold(value)

			_, err := agent.LoadManifestJSON(path)
			Expect(err).To(HaveOccurred(),
				"LoadManifestJSON(%v) = nil; want error", value)
			Expect(err.Error()).To(ContainSubstring("compaction_threshold"))
		},
		Entry("negative", -0.1),
		Entry("above one", 1.5),
	)

	// AcceptsZero pins the zero case. Zero means "inherit global" —
	// the loader must preserve the caller's choice to opt out of a
	// per-agent override. The default-applier currently fills 0 with
	// 0.75, but the semantic under H3 is "zero is legal as input";
	// the applier's behaviour is orthogonal and tested separately.
	It("accepts zero (inherit global)", func() {
		path := writeManifestWithThreshold(0.0)

		_, err := agent.LoadManifestJSON(path)
		Expect(err).NotTo(HaveOccurred(),
			"LoadManifestJSON(0.0) = %v; want nil", err)
	})

	// AcceptsBoundaryOne pins the inclusive upper bound. 1.0 is legal
	// — it means "compact when recent load equals the full budget".
	// Conservative, but legal.
	It("accepts the boundary value 1.0 and round-trips it", func() {
		path := writeManifestWithThreshold(1.0)

		m, err := agent.LoadManifestJSON(path)
		Expect(err).NotTo(HaveOccurred(),
			"LoadManifestJSON(1.0) = %v; want nil", err)
		Expect(m.ContextManagement.CompactionThreshold).To(Equal(1.0))
	})

	// CompactionThresholdErrorMessageGuidance pins the richer
	// operator-facing error wording. The (0, 1] range rule is the
	// same one enforced by compression.auto_compaction.threshold, so
	// the manifest error should mirror its actionable phrasing:
	// operators need to know that values <= 0 never trigger and
	// values > 1 trigger every turn, otherwise they have no way to
	// diagnose their mistake from the error alone.
	DescribeTable("error message carries diagnostic guidance fragments",
		func(value any) {
			path := writeManifestWithThreshold(value)

			_, err := agent.LoadManifestJSON(path)
			Expect(err).To(HaveOccurred(),
				"LoadManifestJSON(%v) = nil; want error", value)

			msg := err.Error()
			// Each fragment is one piece of diagnostic information
			// operators rely on to fix a misconfigured manifest
			// without reading source.
			for _, want := range []string{
				"compaction_threshold", // field name (grep-able)
				"(0.0, 1.0]",           // sibling notation with global validator
				"never trigger",        // guidance for <=0
				"every turn",           // guidance for >1
			} {
				Expect(msg).To(ContainSubstring(want),
					"error = %q; want substring %q", msg, want)
			}
		},
		Entry("negative", -0.1),
		Entry("above one", 1.5),
	)
})
