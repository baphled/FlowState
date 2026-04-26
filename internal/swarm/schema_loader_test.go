package swarm_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

func writeSchemaFile(dir, name, body string) {
	GinkgoHelper()
	path := filepath.Join(dir, name)
	Expect(os.WriteFile(path, []byte(body), 0o644)).To(Succeed())
}

func validVerdictSchema() string {
	return `{
        "type": "object",
        "properties": {
            "verdict": {"type": "string"}
        },
        "required": ["verdict"]
    }`
}

func customGreetingSchema() string {
	return `{
        "type": "object",
        "properties": {
            "greeting": {"type": "string"}
        },
        "required": ["greeting"]
    }`
}

var _ = Describe("schema directory loader (T-swarm-3 Phase 2)", func() {
	var schemaDir string

	BeforeEach(func() {
		swarm.ClearSchemasForTest()
		var err error
		schemaDir, err = os.MkdirTemp("", "swarm-schemas-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.RemoveAll(schemaDir)
	})

	Describe("ResolveSchemaDir", func() {
		It("returns the override verbatim when set", func() {
			Expect(swarm.ResolveSchemaDir("/tmp/cfg", "/custom/schemas")).To(Equal("/custom/schemas"))
		})

		It("falls back to ${ConfigDir}/schemas when no override is set", func() {
			Expect(swarm.ResolveSchemaDir("/tmp/cfg", "")).To(Equal("/tmp/cfg/schemas"))
		})

		It("returns an empty string when both arguments are empty", func() {
			Expect(swarm.ResolveSchemaDir("", "")).To(Equal(""))
		})
	})

	Describe("LoadSchemasFromDir", func() {
		It("returns an empty summary when the directory is missing", func() {
			absent := filepath.Join(schemaDir, "does-not-exist")

			summary, err := swarm.LoadSchemasFromDir(absent)

			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Registered).To(Equal(0))
			Expect(summary.Failed).To(Equal(0))
		})

		It("returns an empty summary when the directory is empty", func() {
			summary, err := swarm.LoadSchemasFromDir(schemaDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Registered).To(Equal(0))
			Expect(summary.Names).To(BeEmpty())
		})

		It("registers a valid JSON file under its basename", func() {
			writeSchemaFile(schemaDir, "custom-greeting.json", customGreetingSchema())

			summary, err := swarm.LoadSchemasFromDir(schemaDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Registered).To(Equal(1))
			Expect(summary.Names).To(ConsistOf("custom-greeting"))
			schema, ok := swarm.LookupSchema("custom-greeting")
			Expect(ok).To(BeTrue())
			Expect(schema).NotTo(BeNil())
		})

		It("skips invalid JSON files with a WARN and continues with the rest", func() {
			writeSchemaFile(schemaDir, "broken.json", "{not json")
			writeSchemaFile(schemaDir, "good.json", customGreetingSchema())

			summary, err := swarm.LoadSchemasFromDir(schemaDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Registered).To(Equal(1))
			Expect(summary.Failed).To(Equal(1))
			Expect(summary.Names).To(ConsistOf("good"))
			_, ok := swarm.LookupSchema("broken")
			Expect(ok).To(BeFalse())
		})

		It("ignores non-JSON files in the directory", func() {
			writeSchemaFile(schemaDir, "notes.txt", "hello")
			writeSchemaFile(schemaDir, "good.json", customGreetingSchema())

			summary, err := swarm.LoadSchemasFromDir(schemaDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Registered).To(Equal(1))
			Expect(summary.Names).To(ConsistOf("good"))
		})

		It("does NOT recurse into subdirectories", func() {
			nested := filepath.Join(schemaDir, "nested")
			Expect(os.Mkdir(nested, 0o755)).To(Succeed())
			writeSchemaFile(nested, "buried.json", customGreetingSchema())

			summary, err := swarm.LoadSchemasFromDir(schemaDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Registered).To(Equal(0))
			_, ok := swarm.LookupSchema("buried")
			Expect(ok).To(BeFalse())
		})

		It("lets file-based registrations override programmatic seeds", func() {
			Expect(swarm.SeedDefaultSchemas()).To(Succeed())
			seeded, ok := swarm.LookupSchema(swarm.ReviewVerdictV1Name)
			Expect(ok).To(BeTrue())
			Expect(seeded).NotTo(BeNil())

			writeSchemaFile(schemaDir, "review-verdict-v1.json", customGreetingSchema())

			summary, err := swarm.LoadSchemasFromDir(schemaDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Registered).To(Equal(1))
			overridden, ok := swarm.LookupSchema(swarm.ReviewVerdictV1Name)
			Expect(ok).To(BeTrue())
			Expect(overridden).NotTo(BeNil())
			Expect(overridden).NotTo(BeIdenticalTo(seeded),
				"file-based registration must replace the programmatic seed")
		})
	})
})
