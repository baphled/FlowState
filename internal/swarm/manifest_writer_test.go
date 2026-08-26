package swarm_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("ManifestWriter", func() {
	var (
		dir    string
		writer *swarm.ManifestWriter
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		writer = swarm.NewManifestWriter(dir)
	})

	Describe("Write", func() {
		Context("when the directory already exists", func() {
			It("serialises the manifest to <name>.yml under the configured directory", func() {
				m := &swarm.Manifest{
					SchemaVersion: swarm.SchemaVersionV1,
					ID:            "alpha",
					Lead:          "planner",
					Members:       []string{"explorer"},
				}

				Expect(writer.Write("alpha", m)).To(Succeed())

				path := filepath.Join(dir, "alpha.yml")
				body, err := os.ReadFile(path)
				Expect(err).NotTo(HaveOccurred())

				var roundTrip swarm.Manifest
				Expect(yaml.Unmarshal(body, &roundTrip)).To(Succeed())
				Expect(roundTrip.ID).To(Equal("alpha"))
				Expect(roundTrip.Lead).To(Equal("planner"))
				Expect(roundTrip.Members).To(ConsistOf("explorer"))
			})

			It("overwrites an existing manifest with the same id", func() {
				path := filepath.Join(dir, "bravo.yml")
				Expect(os.WriteFile(path, []byte("placeholder"), 0o600)).To(Succeed())

				m := &swarm.Manifest{
					SchemaVersion: swarm.SchemaVersionV1,
					ID:            "bravo",
					Lead:          "planner",
				}
				Expect(writer.Write("bravo", m)).To(Succeed())

				body, err := os.ReadFile(path)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(body)).NotTo(ContainSubstring("placeholder"))
				Expect(string(body)).To(ContainSubstring("id: bravo"))
			})
		})

		Context("when the directory does not yet exist", func() {
			It("creates the directory tree before writing", func() {
				nested := filepath.Join(dir, "nested", "swarms")
				w := swarm.NewManifestWriter(nested)

				m := &swarm.Manifest{SchemaVersion: swarm.SchemaVersionV1, ID: "charlie", Lead: "planner"}
				Expect(w.Write("charlie", m)).To(Succeed())

				Expect(filepath.Join(nested, "charlie.yml")).To(BeARegularFile())
			})
		})

		Context("when the writer was constructed with an empty directory", func() {
			It("returns an error and does not touch the filesystem", func() {
				w := swarm.NewManifestWriter("")
				err := w.Write("delta", &swarm.Manifest{SchemaVersion: swarm.SchemaVersionV1, ID: "delta", Lead: "planner"})
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, swarm.ErrManifestWriterNoDir)).To(BeTrue())
			})
		})

		Context("when name is empty", func() {
			It("returns an error and does not write a file", func() {
				err := writer.Write("", &swarm.Manifest{SchemaVersion: swarm.SchemaVersionV1, ID: "x", Lead: "planner"})
				Expect(err).To(HaveOccurred())

				entries, readErr := os.ReadDir(dir)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(entries).To(BeEmpty())
			})
		})

		Context("when the manifest pointer is nil", func() {
			It("returns an error and does not write a file", func() {
				err := writer.Write("echo", nil)
				Expect(err).To(HaveOccurred())

				entries, readErr := os.ReadDir(dir)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(entries).To(BeEmpty())
			})
		})
	})

	Describe("Delete", func() {
		It("removes the manifest file when present", func() {
			m := &swarm.Manifest{SchemaVersion: swarm.SchemaVersionV1, ID: "foxtrot", Lead: "planner"}
			Expect(writer.Write("foxtrot", m)).To(Succeed())
			Expect(filepath.Join(dir, "foxtrot.yml")).To(BeARegularFile())

			Expect(writer.Delete("foxtrot")).To(Succeed())
			Expect(filepath.Join(dir, "foxtrot.yml")).NotTo(BeARegularFile())
		})

		It("is idempotent when the manifest does not exist", func() {
			Expect(writer.Delete("never-existed")).To(Succeed())
		})

		Context("when name is empty", func() {
			It("returns an error", func() {
				Expect(writer.Delete("")).To(HaveOccurred())
			})
		})

		Context("when the writer was constructed with an empty directory", func() {
			It("returns an error", func() {
				w := swarm.NewManifestWriter("")
				err := w.Delete("anything")
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, swarm.ErrManifestWriterNoDir)).To(BeTrue())
			})
		})
	})

	Describe("Path", func() {
		It("returns the file path the writer would target without touching disk", func() {
			path := writer.Path("golf")
			Expect(path).To(Equal(filepath.Join(dir, "golf.yml")))
			Expect(path).NotTo(BeARegularFile())
		})
	})
})
