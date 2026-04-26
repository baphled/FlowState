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
	}
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
			} {
				_, ok := swarm.LookupSchema(name)
				Expect(ok).To(BeTrue(), "expected %q to be registered", name)
			}
		})
	})
})
