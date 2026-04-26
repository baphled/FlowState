package app_test

import (
	"io/fs"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("EmbeddedSwarmsFS", func() {
	Context("when calling EmbeddedSwarmsFS", func() {
		It("returns a non-nil fs.FS", func() {
			Expect(app.EmbeddedSwarmsFS()).NotTo(BeNil())
		})

		It("contains the bundled planning-loop.yml", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(swarmsDir, "planning-loop.yml")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("id: planning-loop"))
		})

		It("contains the bundled solo.yml", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(swarmsDir, "solo.yml")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("id: solo"))
		})

		It("parses planning-loop.yml as a structurally valid swarm manifest", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "planning-loop.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			Expect(m.SchemaVersion).To(Equal(swarm.SchemaVersionV1))
			Expect(m.ID).To(Equal("planning-loop"))
			Expect(m.Lead).To(Equal("planner"))
			Expect(m.Members).To(ContainElements("explorer", "librarian", "analyst", "plan-writer", "plan-reviewer"))
			Expect(m.Validate(nil)).To(Succeed())
		})

		It("parses solo.yml as a structurally valid swarm manifest", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "solo.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			Expect(m.ID).To(Equal("solo"))
			Expect(m.Lead).To(Equal("executor"))
			Expect(m.Validate(nil)).To(Succeed())
		})
	})
})
