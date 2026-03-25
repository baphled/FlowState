package plan_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
)

var _ = Describe("OMOStyleSections", func() {
	It("adds OMO-style fields to plan files and tasks", func() {
		fileType := reflect.TypeOf(plan.File{})

		contextField, ok := fileType.FieldByName("Context")
		Expect(ok).To(BeTrue())
		Expect(contextField.Type.Kind()).To(Equal(reflect.Struct))
		Expect(contextField.Type.Name()).To(Equal("PlanContext"))

		workObjectivesField, ok := fileType.FieldByName("WorkObjectives")
		Expect(ok).To(BeTrue())
		Expect(workObjectivesField.Type.Kind()).To(Equal(reflect.Struct))
		Expect(workObjectivesField.Type.Name()).To(Equal("WorkObjectives"))

		reviewsField, ok := fileType.FieldByName("Reviews")
		Expect(ok).To(BeTrue())
		Expect(reviewsField.Type.Kind()).To(Equal(reflect.Slice))
		Expect(reviewsField.Type.Elem().Name()).To(Equal("ReviewResult"))

		tldrField, ok := fileType.FieldByName("TLDR")
		Expect(ok).To(BeTrue())
		Expect(tldrField.Type.Kind()).To(Equal(reflect.String))

		taskType := reflect.TypeOf(plan.Task{})

		fileChangesField, ok := taskType.FieldByName("FileChanges")
		Expect(ok).To(BeTrue())
		Expect(fileChangesField.Type.Kind()).To(Equal(reflect.Slice))
		Expect(fileChangesField.Type.Elem().Kind()).To(Equal(reflect.String))

		evidenceField, ok := taskType.FieldByName("Evidence")
		Expect(ok).To(BeTrue())
		Expect(evidenceField.Type.Kind()).To(Equal(reflect.String))

		categoryField, ok := taskType.FieldByName("Category")
		Expect(ok).To(BeTrue())
		Expect(categoryField.Type.Kind()).To(Equal(reflect.String))
	})

	It("leaves the new fields at zero values", func() {
		file := plan.File{}
		fileValue := reflect.ValueOf(file)

		Expect(fileValue.FieldByName("TLDR").String()).To(BeEmpty())
		Expect(fileValue.FieldByName("VerificationStrategy").String()).To(BeEmpty())
		Expect(fileValue.FieldByName("Context").IsZero()).To(BeTrue())
		Expect(fileValue.FieldByName("WorkObjectives").IsZero()).To(BeTrue())
		Expect(fileValue.FieldByName("Reviews").IsNil()).To(BeTrue())

		task := plan.Task{}
		taskValue := reflect.ValueOf(task)

		Expect(taskValue.FieldByName("FileChanges").IsNil()).To(BeTrue())
		Expect(taskValue.FieldByName("Evidence").String()).To(BeEmpty())
	})
})
