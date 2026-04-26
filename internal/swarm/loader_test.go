package swarm_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("Loader", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "swarm-loader-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("Load", func() {
		Context("with a valid YAML manifest", func() {
			It("parses and validates the manifest", func() {
				path := filepath.Join(tempDir, "team.yml")
				body := `schema_version: "1.0.0"
id: team
description: Senior tech-lead delegating to a domain swarm
lead: planner
members:
  - explorer
  - analyst
harness:
  parallel: false
  gates:
    - name: post-team-summary
      kind: builtin:result-schema
      schema_ref: review-verdict-v1
      when: post
context:
  chain_prefix: tech
`
				Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

				m, err := swarm.Load(path)

				Expect(err).NotTo(HaveOccurred())
				Expect(m.ID).To(Equal("team"))
				Expect(m.Lead).To(Equal("planner"))
				Expect(m.Members).To(ConsistOf("explorer", "analyst"))
				Expect(m.Harness.Gates).To(HaveLen(1))
				Expect(m.Harness.Gates[0].Kind).To(Equal("builtin:result-schema"))
				Expect(m.Context.ChainPrefix).To(Equal("tech"))
			})
		})

		Context("with malformed YAML", func() {
			It("returns a wrapped parse error", func() {
				path := filepath.Join(tempDir, "broken.yml")
				Expect(os.WriteFile(path, []byte("schema_version: \"1.0.0\"\nid: team\n  lead: oops\n"), 0o600)).To(Succeed())

				_, err := swarm.Load(path)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("parsing swarm manifest"))
			})
		})

		Context("with a missing lead", func() {
			It("returns a wrapped validation error", func() {
				path := filepath.Join(tempDir, "noLead.yml")
				body := `schema_version: "1.0.0"
id: team
members:
  - explorer
`
				Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

				_, err := swarm.Load(path)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("validating swarm manifest"))
				Expect(err.Error()).To(ContainSubstring("lead"))
			})
		})

		Context("with a self-referencing member (trivial cycle)", func() {
			It("returns a wrapped validation error at file-load time", func() {
				path := filepath.Join(tempDir, "loop.yml")
				body := `schema_version: "1.0.0"
id: loop
lead: planner
members:
  - loop
`
				Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

				_, err := swarm.Load(path)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("self-reference"))
			})
		})

		Context("with a missing file", func() {
			It("returns a wrapped read error", func() {
				_, err := swarm.Load(filepath.Join(tempDir, "absent.yml"))

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("reading swarm manifest"))
			})
		})
	})

	Describe("LoadDir", func() {
		Context("when the directory contains a mix of valid and invalid files", func() {
			It("returns the valid manifests and an aggregated error", func() {
				goodPath := filepath.Join(tempDir, "good.yml")
				badPath := filepath.Join(tempDir, "bad.yml")
				Expect(os.WriteFile(goodPath, []byte(`schema_version: "1.0.0"
id: good
lead: planner
members:
  - explorer
`), 0o600)).To(Succeed())
				Expect(os.WriteFile(badPath, []byte(`schema_version: "1.0.0"
id: bad
`), 0o600)).To(Succeed()) // missing lead

				manifests, err := swarm.LoadDir(tempDir)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("bad.yml"))
				Expect(manifests).To(HaveLen(1))
				Expect(manifests[0].ID).To(Equal("good"))
			})
		})

		Context("when the directory only has valid files", func() {
			It("returns every manifest sorted by id", func() {
				Expect(os.WriteFile(filepath.Join(tempDir, "alpha.yml"), []byte(`schema_version: "1.0.0"
id: alpha
lead: planner
members: []
`), 0o600)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(tempDir, "bravo.yaml"), []byte(`schema_version: "1.0.0"
id: bravo
lead: planner
members: []
`), 0o600)).To(Succeed())

				manifests, err := swarm.LoadDir(tempDir)

				Expect(err).NotTo(HaveOccurred())
				Expect(manifests).To(HaveLen(2))
				Expect(manifests[0].ID).To(Equal("alpha"))
				Expect(manifests[1].ID).To(Equal("bravo"))
			})
		})

		Context("when the directory does not exist", func() {
			It("returns a wrapped not-exist error", func() {
				_, err := swarm.LoadDir(filepath.Join(tempDir, "absent"))

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("does not exist"))
			})
		})

		Context("when the directory has no swarm manifests", func() {
			It("returns nil manifests and nil error", func() {
				manifests, err := swarm.LoadDir(tempDir)

				Expect(err).NotTo(HaveOccurred())
				Expect(manifests).To(BeEmpty())
			})
		})
	})
})
