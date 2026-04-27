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
})
