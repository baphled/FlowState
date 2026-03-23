package plan_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
)

var _ = Describe("ReferenceValidator", func() {
	var (
		validator   *plan.ReferenceValidator
		projectRoot string
	)

	BeforeEach(func() {
		validator = &plan.ReferenceValidator{}
		cwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		projectRoot, err = filepath.Abs(filepath.Join(cwd, "..", ".."))
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("valid references", func() {
		It("returns valid when all refs exist", func() {
			planText := "This plan references `internal/plan/schema_validator.go` for validation."

			result, err := validator.Validate(planText, projectRoot)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
			Expect(result.Score).To(Equal(1.0))
		})
	})

	Describe("invalid references", func() {
		It("returns invalid when a ref does not exist", func() {
			planText := "This plan references `internal/foo/nonexistent.go` for validation."
			result, err := validator.Validate(planText, projectRoot)
			Expect(err).To(HaveOccurred())
			Expect(result.Valid).To(BeFalse())
			Expect(result.Errors).NotTo(BeEmpty())
			Expect(result.Errors[0]).To(ContainSubstring("file not found"))
		})
	})

	Describe("no references", func() {
		It("returns valid and score 1.0 when no refs present", func() {
			planText := "This plan has no file references."
			result, err := validator.Validate(planText, projectRoot)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
			Expect(result.Score).To(Equal(1.0))
		})
	})

	Describe("mixed references", func() {
		It("returns invalid and score < 1.0 when some refs are invalid", func() {
			planText := "Valid: `internal/plan/schema_validator.go`, Invalid: `internal/foo/nonexistent.go`"
			result, err := validator.Validate(planText, projectRoot)
			Expect(err).To(HaveOccurred())
			Expect(result.Valid).To(BeFalse())
			Expect(result.Score).To(BeNumerically("<", 1.0))
			Expect(result.Errors).NotTo(BeEmpty())
		})
	})

	Describe("security: outside project root", func() {
		It("rejects references outside project root", func() {
			planText := "Reference: `../../../../etc/passwd.go`"
			result, err := validator.Validate(planText, projectRoot)
			Expect(err).To(HaveOccurred())
			Expect(result.Valid).To(BeFalse())
			Expect(result.Errors[0]).To(ContainSubstring("outside project root"))
		})
	})
})
