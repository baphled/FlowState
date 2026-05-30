package swarm_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

func mustValidate(name string, payload string) error {
	resolved, ok := swarm.LookupSchema(name)
	Expect(ok).To(BeTrue())
	var instance any
	Expect(json.Unmarshal([]byte(payload), &instance)).To(Succeed())
	return resolved.Validate(instance)
}

type schemaCase struct {
	schema  string
	valid   string
	invalid string
}

func planningSchemaCases() []schemaCase {
	return []schemaCase{
		{
			schema:  swarm.EvidenceBundleV1Name,
			valid:   `{"findings":[{"file":"internal/swarm/gates.go","summary":"GateRunner interface"}]}`,
			invalid: `{"findings":[{"summary":"missing file"}]}`,
		},
		{
			schema:  swarm.ExternalRefsV1Name,
			valid:   `{"references":[{"url":"https://example.com","title":"Example"}]}`,
			invalid: `{"references":[{"title":"missing url"}]}`,
		},
		{
			schema:  swarm.AnalysisBundleV1Name,
			valid:   `{"key_findings":["pattern A"],"recommendations":["use pattern A"]}`,
			invalid: `{"key_findings":["pattern A"]}`,
		},
		{
			schema:  swarm.PlanDocumentV1Name,
			valid:   `{"markdown":"# Plan\n...","id":"plan-1","title":"Sample"}`,
			invalid: `"# bare markdown string is not an object"`,
		},
		{
			schema:  swarm.ReviewVerdictV1Name,
			valid:   `{"verdict":"approve","reasoning":"shipping"}`,
			invalid: `{"reasoning":"missing verdict"}`,
		},
		{
			schema:  swarm.CodeReviewVerdictV1Name,
			valid:   codeReviewFullPayload(),
			invalid: `{"summary":"missing verdict"}`,
		},
		{
			schema:  swarm.SectionV1Name,
			valid:   `{"section":"architecture","title":"Architecture","body":"# Architecture\n...","key_points":["layered design"]}`,
			invalid: `{"section":"architecture","title":"Architecture","key_points":["missing body"]}`,
		},
	}
}

func codeReviewFullPayload() string {
	return `{
		"verdict": "request_changes",
		"summary": "Concerns around concurrency in cache.",
		"concerns": ["race in updateCache", "missing test"],
		"severity_breakdown": {"critical": 1, "major": 0, "minor": 2, "nit": 0},
		"references": [
			{"file": "internal/cache/cache.go", "line": 42, "snippet": "go func() { ... }"},
			{"file": "internal/cache/cache_test.go"}
		],
		"confidence": "high"
	}`
}

func codeReviewMinimalPayload() string {
	return `{"verdict":"approve","summary":"LGTM"}`
}

func codeReviewBadVerdictPayload() string {
	return `{"verdict":"merge","summary":"not in the enum"}`
}

func codeReviewMissingSummaryPayload() string {
	return `{"verdict":"approve"}`
}

var _ = Describe("planning-loop schemas", func() {
	BeforeEach(func() {
		swarm.ClearSchemasForTest()
		Expect(swarm.SeedDefaultSchemas()).To(Succeed())
	})

	for _, tc := range planningSchemaCases() {
		tc := tc
		Describe(tc.schema, func() {
			It("accepts a representative valid payload", func() {
				Expect(mustValidate(tc.schema, tc.valid)).To(Succeed())
			})

			It("rejects a malformed payload", func() {
				Expect(mustValidate(tc.schema, tc.invalid)).To(HaveOccurred())
			})
		})
	}

	Describe("SeedDefaultSchemas", func() {
		It("registers every planning-loop schema name", func() {
			for _, name := range []string{
				swarm.ReviewVerdictV1Name,
				swarm.EvidenceBundleV1Name,
				swarm.ExternalRefsV1Name,
				swarm.AnalysisBundleV1Name,
				swarm.PlanDocumentV1Name,
				swarm.CodeReviewVerdictV1Name,
				swarm.SectionV1Name,
			} {
				_, ok := swarm.LookupSchema(name)
				Expect(ok).To(BeTrue(), "expected %q to be registered", name)
			}
		})

		It("exposes code-review-verdict-v1 via RegisteredSchemaNames", func() {
			Expect(swarm.RegisteredSchemaNames()).To(ContainElement(swarm.CodeReviewVerdictV1Name))
		})
	})

	Describe(swarm.CodeReviewVerdictV1Name+" edge cases", func() {
		It("accepts a minimal payload with only verdict and summary", func() {
			Expect(mustValidate(swarm.CodeReviewVerdictV1Name, codeReviewMinimalPayload())).To(Succeed())
		})

		It("rejects a verdict outside the enum", func() {
			Expect(mustValidate(swarm.CodeReviewVerdictV1Name, codeReviewBadVerdictPayload())).To(HaveOccurred())
		})

		It("rejects a payload missing summary", func() {
			Expect(mustValidate(swarm.CodeReviewVerdictV1Name, codeReviewMissingSummaryPayload())).To(HaveOccurred())
		})
	})

	Describe(swarm.PlanDocumentV1Name+" renderable-plan contract", func() {
		// The member plan-document gate must ACCEPT any body the post-swarm
		// publisher can render into a non-empty plan document, and REJECT
		// only genuinely non-renderable bodies — so its verdict is identical
		// to the publisher's (and to the post-swarm honesty gate, which
		// already render-mirrors via resolveValidationBody). The single
		// source of truth is swarm.ValidatePlanDocumentBody, which the
		// result-schema runner calls for plan-document-v1; these specs assert
		// that real-gate predicate directly rather than the raw JSON schema
		// (which is no longer the authority for this gate — see
		// gate_result_schema.go).
		It("accepts a body with a non-empty markdown string", func() {
			Expect(swarm.ValidatePlanDocumentBody(
				[]byte(`{"markdown":"# Plan\n\nbody","id":"plan-1","title":"Sample"}`))).To(Succeed())
		})

		It("accepts a body with a non-empty plan string (alternate key)", func() {
			Expect(swarm.ValidatePlanDocumentBody(
				[]byte(`{"plan":"# Plan\n\nbody","title":"Sample"}`))).To(Succeed())
		})

		It("accepts the structured content-object body the publisher renders", func() {
			// FLIPPED (was: rejects). The ACTUAL emitted shape from the failing
			// run — real plan prose nested under `content` with NO top-level
			// markdown/plan string. renderStructuredPlan turns it into a
			// coherent plan document, so the publisher succeeds; the member
			// gate must therefore ACCEPT it. Rejecting it (the pre-fix schema)
			// halted the run BEFORE publish could render the very body it can
			// render — the Part 1 / Part 2 incoherence this fix closes.
			body := `{"id":"pmr-1","title":"Permission-Mode Redesign","status":"draft",` +
				`"content":{"executive_summary":"Redesign permission mode.",` +
				`"phased_slices":[{"title":"Slice 1","description":"Do the thing."}]}}`
			Expect(swarm.ValidatePlanDocumentBody([]byte(body))).To(Succeed())
		})

		It("rejects a body with an empty markdown string", func() {
			Expect(swarm.ValidatePlanDocumentBody(
				[]byte(`{"markdown":"","title":"Sample"}`))).To(HaveOccurred())
		})

		It("rejects a metadata-only body with no markdown, plan, or renderable content", func() {
			Expect(swarm.ValidatePlanDocumentBody(
				[]byte(`{"id":"x","title":"y","status":"draft"}`))).To(HaveOccurred())
		})

		It("rejects a structured content body that carries no renderable prose", func() {
			// content present but empty — nothing for renderStructuredPlan to
			// salvage, so the publisher refuses it and the gate must too.
			Expect(swarm.ValidatePlanDocumentBody(
				[]byte(`{"title":"Empty","content":{"executive_summary":"","phased_slices":[]}}`))).To(HaveOccurred())
		})
	})
})
