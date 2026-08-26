package validation_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan/validation"
)

var _ = Describe("SchemaValidator", func() {
	var validator *validation.SchemaValidator

	BeforeEach(func() {
		validator = &validation.SchemaValidator{}
	})

	It("validates a correct plan (valid_plan.md)", func() {
		data, err := os.ReadFile("../testdata/valid_plan.md")
		Expect(err).NotTo(HaveOccurred())
		result, err := validator.Validate(string(data))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Valid).To(BeTrue())
		Expect(result.Score).To(BeNumerically(">=", 0.8))
	})

	It("fails on empty string", func() {
		result, err := validator.Validate("")
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("empty")))
	})

	It("fails on missing frontmatter (invalid_missing_frontmatter.md)", func() {
		data, err := os.ReadFile("../testdata/invalid_missing_frontmatter.md")
		Expect(err).NotTo(HaveOccurred())
		result, err := validator.Validate(string(data))
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("missing YAML frontmatter")))
	})

	It("fails on bad YAML (invalid_bad_yaml.md)", func() {
		data, err := os.ReadFile("../testdata/invalid_bad_yaml.md")
		Expect(err).NotTo(HaveOccurred())
		result, err := validator.Validate(string(data))
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("invalid YAML")))
	})

	It("fails on missing tasks (invalid_missing_tasks.md)", func() {
		data, err := os.ReadFile("../testdata/invalid_missing_tasks.md")
		Expect(err).NotTo(HaveOccurred())
		result, err := validator.Validate(string(data))
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("no tasks found")))
	})

	It("fails on whitespace-only input", func() {
		result, err := validator.Validate("   \n\t\n  ")
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("empty")))
		Expect(result.Score).To(Equal(0.0))
	})

	It("fails on frontmatter with missing id", func() {
		planText := "---\ntitle: My Plan\n---\n## Tasks\n- [ ] Do something\n"
		result, err := validator.Validate(planText)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("missing id")))
		Expect(result.Score).To(BeNumerically("<", 1.0))
	})

	It("fails on frontmatter with missing title", func() {
		planText := "---\nid: plan-001\n---\n## Tasks\n- [ ] Do something\n"
		result, err := validator.Validate(planText)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("missing title")))
		Expect(result.Score).To(BeNumerically("<", 1.0))
	})

	It("returns valid with score 1.0 for well-formed plan", func() {
		data, err := os.ReadFile("../testdata/valid_plan.md")
		Expect(err).NotTo(HaveOccurred())
		result, err := validator.Validate(string(data))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Valid).To(BeTrue())
		Expect(result.Score).To(Equal(1.0))
		Expect(result.Errors).To(BeEmpty())
	})
})
