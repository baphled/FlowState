package plan_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
)

var _ = Describe("AssertionValidator", func() {
	var validator *plan.AssertionValidator

	BeforeEach(func() {
		validator = &plan.AssertionValidator{}
	})

	It("validates a correct plan with proper dependencies", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "A", Dependencies: []string{}, EstimatedEffort: "1h"},
				{Title: "B", Dependencies: []string{"A"}, EstimatedEffort: "2h"},
				{Title: "C", Dependencies: []string{"B"}, EstimatedEffort: "1h"},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Valid).To(BeTrue())
		Expect(result.Errors).To(BeEmpty())
		Expect(result.Score).To(Equal(1.0))
	})

	It("detects circular dependencies", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "A", Dependencies: []string{"C"}, EstimatedEffort: "1h"},
				{Title: "B", Dependencies: []string{"A"}, EstimatedEffort: "2h"},
				{Title: "C", Dependencies: []string{"B"}, EstimatedEffort: "1h"},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).NotTo(BeEmpty())
		Expect(result.Errors[0]).To(ContainSubstring("circular dependency"))
		Expect(result.Score).To(BeNumerically("<", 1.0))
	})

	It("detects duplicate task titles", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "A", Dependencies: []string{}, EstimatedEffort: "1h"},
				{Title: "A", Dependencies: []string{}, EstimatedEffort: "2h"},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).NotTo(BeEmpty())
		Expect(result.Errors[0]).To(ContainSubstring("duplicate task title"))
		Expect(result.Score).To(BeNumerically("<", 1.0))
	})

	It("detects invalid dependency references", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "A", Dependencies: []string{"B"}, EstimatedEffort: "1h"},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).NotTo(BeEmpty())
		Expect(result.Errors[0]).To(ContainSubstring("unknown dependency"))
		Expect(result.Score).To(BeNumerically("<", 1.0))
	})

	It("detects missing estimated effort", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "A", Dependencies: []string{}, EstimatedEffort: ""},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).NotTo(BeEmpty())
		Expect(result.Errors[0]).To(ContainSubstring("missing estimated effort"))
		Expect(result.Score).To(BeNumerically("<", 1.0))
	})

	It("validates an empty tasks slice as valid", func() {
		file := &plan.File{
			Tasks: []plan.Task{},
		}
		result, err := validator.Validate(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Valid).To(BeTrue())
		Expect(result.Score).To(Equal(1.0))
	})

	It("detects self-referencing dependency", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "A", Dependencies: []string{"A"}, EstimatedEffort: "1h"},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("circular dependency")))
	})

	It("accumulates multiple errors and clamps score to zero", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "A", Dependencies: []string{}, EstimatedEffort: ""},
				{Title: "A", Dependencies: []string{"Z"}, EstimatedEffort: ""},
				{Title: "B", Dependencies: []string{}, EstimatedEffort: ""},
				{Title: "B", Dependencies: []string{}, EstimatedEffort: ""},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).To(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(len(result.Errors)).To(BeNumerically(">=", 3))
		Expect(result.Score).To(BeNumerically(">=", 0.0))
	})

	It("validates a single task with no dependencies", func() {
		file := &plan.File{
			Tasks: []plan.Task{
				{Title: "Solo task", Dependencies: []string{}, EstimatedEffort: "30m"},
			},
		}
		result, err := validator.Validate(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Valid).To(BeTrue())
		Expect(result.Errors).To(BeEmpty())
		Expect(result.Score).To(Equal(1.0))
	})
})
