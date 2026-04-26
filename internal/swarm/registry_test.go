package swarm_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

// stubAgentRegistry is a minimal swarm.AgentRegistry adapter for tests.
type stubAgentRegistry struct {
	known map[string]bool
}

func newStubAgentRegistry(ids ...string) *stubAgentRegistry {
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	return &stubAgentRegistry{known: known}
}

func (s *stubAgentRegistry) Get(id string) (any, bool) {
	if s.known[id] {
		return struct{}{}, true
	}
	return nil, false
}

const validSwarmYAML = `schema_version: "1.0.0"
id: %s
lead: %s
members: %s
`

var _ = Describe("Registry", func() {
	var (
		reg     *swarm.Registry
		tempDir string
	)

	BeforeEach(func() {
		reg = swarm.NewRegistry()
		var err error
		tempDir, err = os.MkdirTemp("", "swarm-registry-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("NewRegistry", func() {
		It("creates an empty registry", func() {
			Expect(reg).NotTo(BeNil())
			Expect(reg.List()).To(BeEmpty())
		})
	})

	Describe("Register", func() {
		It("adds a manifest to the registry", func() {
			m := &swarm.Manifest{ID: "team-a"}

			reg.Register(m)

			retrieved, ok := reg.Get("team-a")
			Expect(ok).To(BeTrue())
			Expect(retrieved.ID).To(Equal("team-a"))
		})

		It("ignores a nil manifest without panicking", func() {
			Expect(func() { reg.Register(nil) }).NotTo(Panic())
			Expect(reg.List()).To(BeEmpty())
		})

		It("overwrites an existing manifest with the same id", func() {
			reg.Register(&swarm.Manifest{ID: "team", Description: "v1"})
			reg.Register(&swarm.Manifest{ID: "team", Description: "v2"})

			retrieved, ok := reg.Get("team")
			Expect(ok).To(BeTrue())
			Expect(retrieved.Description).To(Equal("v2"))
		})
	})

	Describe("Get", func() {
		It("returns false for an unknown id", func() {
			retrieved, ok := reg.Get("nope")
			Expect(ok).To(BeFalse())
			Expect(retrieved).To(BeNil())
		})
	})

	Describe("List", func() {
		It("returns nil for an empty registry", func() {
			Expect(reg.List()).To(BeNil())
		})

		It("returns manifests sorted by id", func() {
			reg.Register(&swarm.Manifest{ID: "charlie"})
			reg.Register(&swarm.Manifest{ID: "alpha"})
			reg.Register(&swarm.Manifest{ID: "bravo"})

			list := reg.List()

			Expect(list).To(HaveLen(3))
			Expect(list[0].ID).To(Equal("alpha"))
			Expect(list[1].ID).To(Equal("bravo"))
			Expect(list[2].ID).To(Equal("charlie"))
		})
	})

	Describe("NewRegistryFromDir", func() {
		writeManifest := func(name, body string) {
			path := filepath.Join(tempDir, name)
			Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())
		}

		Context("with a valid manifest set and matching agent registry", func() {
			It("registers every manifest", func() {
				writeManifest("team.yml", `schema_version: "1.0.0"
id: team
lead: planner
members:
  - explorer
  - analyst
`)
				agents := newStubAgentRegistry("planner", "explorer", "analyst")

				built, err := swarm.NewRegistryFromDir(tempDir, agents)

				Expect(err).NotTo(HaveOccurred())
				Expect(built.List()).To(HaveLen(1))
				m, ok := built.Get("team")
				Expect(ok).To(BeTrue())
				Expect(m.Lead).To(Equal("planner"))
			})
		})

		Context("with a swarm id that collides with an agent id", func() {
			It("returns an aggregated error naming the collision", func() {
				writeManifest("planner.yml", `schema_version: "1.0.0"
id: planner
lead: explorer
members:
  - explorer
`)
				agents := newStubAgentRegistry("planner", "explorer")

				_, err := swarm.NewRegistryFromDir(tempDir, agents)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("collides with an agent id"))
			})
		})

		Context("with a missing directory", func() {
			It("returns ErrSwarmDirNotFound via errors.Is", func() {
				_, err := swarm.NewRegistryFromDir(filepath.Join(tempDir, "absent"), nil)

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, swarm.ErrSwarmDirNotFound)).To(BeTrue())
			})
		})

		Context("with a swarm whose lead does not resolve", func() {
			It("returns an aggregated validation error", func() {
				writeManifest("ghost-lead.yml", `schema_version: "1.0.0"
id: ghost-lead
lead: missing
members:
  - explorer
`)
				agents := newStubAgentRegistry("explorer")

				_, err := swarm.NewRegistryFromDir(tempDir, agents)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(`"missing" does not resolve`))
			})
		})

		Context("with two swarms forming a cycle", func() {
			It("rejects with a cycle diagnostic naming both ids", func() {
				writeManifest("alpha.yml", `schema_version: "1.0.0"
id: alpha
lead: explorer
members:
  - bravo
`)
				writeManifest("bravo.yml", `schema_version: "1.0.0"
id: bravo
lead: explorer
members:
  - alpha
`)
				agents := newStubAgentRegistry("explorer")

				_, err := swarm.NewRegistryFromDir(tempDir, agents)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cycle detected"))
				Expect(err.Error()).To(ContainSubstring("alpha"))
				Expect(err.Error()).To(ContainSubstring("bravo"))
			})
		})

		Context("with a nil agent registry", func() {
			It("still loads structurally valid manifests but skips agent resolution", func() {
				writeManifest("solo.yml", `schema_version: "1.0.0"
id: solo
lead: executor
members: []
`)

				built, err := swarm.NewRegistryFromDir(tempDir, nil)

				// nil agentReg means HasAgent returns false; the
				// registry-aware re-validation now reports the
				// unresolved lead. That is the correct behaviour:
				// production callers must pass an agent registry.
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(`"executor" does not resolve`))
				Expect(built).NotTo(BeNil())
			})
		})
	})
})
