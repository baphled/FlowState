package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

// stubValidator is a Validator that resolves a fixed set of agent and
// swarm ids. Used to exercise the registry-aware branches of
// Manifest.Validate without standing up a full registry.
type stubValidator struct {
	agents map[string]bool
	swarms map[string]bool
}

func newStubValidator(agents, swarms []string) stubValidator {
	a := make(map[string]bool, len(agents))
	for _, id := range agents {
		a[id] = true
	}
	s := make(map[string]bool, len(swarms))
	for _, id := range swarms {
		s[id] = true
	}
	return stubValidator{agents: a, swarms: s}
}

func (s stubValidator) HasAgent(id string) bool { return s.agents[id] }
func (s stubValidator) HasSwarm(id string) bool { return s.swarms[id] }

var _ = Describe("Manifest.Validate", func() {
	validBase := func() *swarm.Manifest {
		return &swarm.Manifest{
			SchemaVersion: swarm.SchemaVersionV1,
			ID:            "team",
			Lead:          "planner",
			Members:       []string{"explorer", "analyst"},
		}
	}

	Context("with no validator (file-load mode)", func() {
		It("accepts a structurally valid manifest", func() {
			m := validBase()

			err := m.Validate(nil)

			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a manifest with a blank schema_version", func() {
			m := validBase()
			m.SchemaVersion = "  "

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			var verr *swarm.ValidationError
			Expect(err).To(BeAssignableToTypeOf(verr))
			Expect(err.Error()).To(ContainSubstring("schema_version"))
		})

		It("rejects an unsupported schema_version", func() {
			m := validBase()
			m.SchemaVersion = "2.0.0"

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported version"))
		})

		It("rejects a manifest with an empty id", func() {
			m := validBase()
			m.ID = ""

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("id"))
		})

		It("rejects a manifest with an empty lead", func() {
			m := validBase()
			m.Lead = ""

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("lead"))
		})

		It("rejects a self-reference via lead", func() {
			m := validBase()
			m.Lead = m.ID

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("self-reference"))
		})

		It("rejects a self-reference via members", func() {
			m := validBase()
			m.Members = append(m.Members, m.ID)

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("self-reference"))
		})

		It("rejects a gate without a builtin: or ext: prefix", func() {
			m := validBase()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "bad", Kind: "result-schema"},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must start with"))
		})

		It("accepts a builtin: gate kind", func() {
			m := validBase()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "ok", Kind: "builtin:result-schema", When: "post"},
			}

			err := m.Validate(nil)

			Expect(err).NotTo(HaveOccurred())
		})

		It("accepts an ext: gate kind", func() {
			m := validBase()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "ok", Kind: "ext:my-summariser", When: "post"},
			}

			err := m.Validate(nil)

			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a gate with no name", func() {
			m := validBase()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "", Kind: "builtin:result-schema"},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("name"))
		})
	})

	Context("with a registry-aware validator", func() {
		It("rejects a lead that resolves to neither an agent nor a swarm", func() {
			m := validBase()
			m.Lead = "ghost"
			v := newStubValidator([]string{"explorer", "analyst"}, []string{"team"})

			err := m.Validate(v)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`"ghost" does not resolve`))
		})

		It("accepts a lead resolving to a swarm (sub-swarm composition)", func() {
			m := validBase()
			m.Lead = "qa-swarm"
			v := newStubValidator([]string{"explorer", "analyst"}, []string{"team", "qa-swarm"})

			err := m.Validate(v)

			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a member that resolves to nothing", func() {
			m := validBase()
			m.Members = append(m.Members, "missing")
			v := newStubValidator([]string{"planner", "explorer", "analyst"}, []string{"team"})

			err := m.Validate(v)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`"missing" does not resolve`))
		})

		It("accepts an all-resolved manifest", func() {
			m := validBase()
			v := newStubValidator([]string{"planner", "explorer", "analyst"}, []string{"team"})

			err := m.Validate(v)

			Expect(err).NotTo(HaveOccurred())
		})
	})
})
