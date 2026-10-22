package swarm_test

import (
	"os"
	"path/filepath"
	"time"

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

		It("accepts inline swarm prompt appends", func() {
			m := validBase()
			m.Prompt.LeadAppend = "Lead-only instructions"
			m.Prompt.MemberAppends = map[string]swarm.PromptAppendConfig{
				"explorer": {Append: "Member-only instructions"},
			}

			Expect(m.Validate(nil)).To(Succeed())
		})

		It("rejects a relative prompt file when the manifest has no source dir", func() {
			m := validBase()
			m.Prompt.LeadAppendFile = "lead.md"

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("relative prompt file"))
		})

		It("accepts a relative prompt file when it exists under the manifest source dir", func() {
			dir := GinkgoT().TempDir()
			path := filepath.Join(dir, "lead.md")
			Expect(os.WriteFile(path, []byte("Lead prompt"), 0o600)).To(Succeed())

			m := validBase()
			m.SourceDir = dir
			m.Prompt.LeadAppendFile = "lead.md"

			Expect(m.Validate(nil)).To(Succeed())
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

	// Member-timeout bounds the per-delegate await loop so a stalled
	// child cannot hang the parent coordinator forever. Mirrors the
	// GateSpec.Timeout precedent (zero = no deadline, positive = applied,
	// negative = rejected).
	//
	// Symptom this guards against: session 3255e2ee-12a8-4cda-b0c6-
	// 8be5ade06cad — coordinator dispatched two members in parallel; the
	// executor went silent mid-stream and the parent's session stayed
	// active indefinitely because DelegateTool.collectWithProgress had no
	// time.After branch in its select.
	Context("with harness.member_timeout", func() {
		It("accepts a manifest where member_timeout is omitted (zero — no deadline)", func() {
			m := validBase()

			err := m.Validate(nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(m.Harness.MemberTimeout).To(BeZero())
		})

		It("accepts a manifest with a positive member_timeout", func() {
			m := validBase()
			m.Harness.MemberTimeout = 5 * time.Minute

			err := m.Validate(nil)

			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a manifest with a negative member_timeout", func() {
			m := validBase()
			m.Harness.MemberTimeout = -1 * time.Second

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			var verr *swarm.ValidationError
			Expect(err).To(BeAssignableToTypeOf(verr))
			Expect(err.Error()).To(ContainSubstring("harness.member_timeout"))
		})

		It("parses harness.member_timeout from a duration scalar via YAML", func() {
			body := []byte(`schema_version: "1.0.0"
id: timeout-swarm
lead: planner
members:
  - reviewer
harness:
  parallel: true
  member_timeout: 90s
`)

			m, err := swarm.UnmarshalManifest(body)

			Expect(err).NotTo(HaveOccurred())
			Expect(m.Harness.MemberTimeout).To(Equal(90 * time.Second))
		})
	})
})

var _ = Describe("Manifest.Warnings (advisory, non-fatal)", func() {
	// Warnings surface the chain_prefix != id footgun class at validate
	// time (make check / `flowstate swarm validate`) BEFORE a run. Per-run
	// chain assignment now anchors the per-run namespace under the pinned
	// prefix, so a differing prefix is SAFE — hence a WARNING (not an
	// error): it explains the implication without failing the build, so the
	// legitimate embedded swarms that intentionally pin a differing prefix
	// (planning-loop, plan-sme-swarm, meta-swarm) keep validating cleanly.

	manifestWithPrefix := func(id, prefix string) *swarm.Manifest {
		return &swarm.Manifest{
			SchemaVersion: swarm.SchemaVersionV1,
			ID:            id,
			Lead:          "planner",
			Members:       []string{"reviewer"},
			Context:       swarm.ContextConfig{ChainPrefix: prefix},
		}
	}

	It("warns when chain_prefix differs from the swarm id", func() {
		m := manifestWithPrefix("mental-health-swarm", "mental-health")

		warnings := m.Warnings()

		Expect(warnings).NotTo(BeEmpty(),
			"a differing chain_prefix is the historical footgun class and must be surfaced")
		var found bool
		for _, w := range warnings {
			if w.Field == "context.chain_prefix" {
				found = true
				Expect(w.Message).To(ContainSubstring("mental-health"))
				Expect(w.Message).To(ContainSubstring("mental-health-swarm"))
			}
		}
		Expect(found).To(BeTrue(), "the warning names the context.chain_prefix field")
	})

	It("does NOT warn when chain_prefix equals the swarm id", func() {
		m := manifestWithPrefix("dev-swarm", "dev-swarm")

		Expect(m.Warnings()).To(BeEmpty(),
			"a prefix matching the id is the canonical default — no footgun, no warning")
	})

	It("does NOT warn when chain_prefix is omitted (defaults to the id)", func() {
		m := manifestWithPrefix("a-team", "")

		Expect(m.Warnings()).To(BeEmpty(),
			"an empty prefix defaults to the swarm id at NewContext — no differing prefix")
	})

	It("does NOT turn a warning into a validation error", func() {
		m := manifestWithPrefix("mental-health-swarm", "mental-health")

		Expect(m.Validate(nil)).To(Succeed(),
			"a differing prefix is advisory only — it must never fail validation")
	})
})
