package memory_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/memory"
)

var _ = Describe("EntityConventions", func() {
	Describe("EntityType validation", func() {
		It("accepts only the 4 allowed entity types", func() {
			// Should pass for Agent, Project, Concept, Tool
			Expect(memory.ValidateEntityType("Agent")).To(BeTrue())
			Expect(memory.ValidateEntityType("Project")).To(BeTrue())
			Expect(memory.ValidateEntityType("Concept")).To(BeTrue())
			Expect(memory.ValidateEntityType("Tool")).To(BeTrue())
		})
		It("rejects invalid entity types", func() {
			// Should fail for e.g. 'Person', 'Task', ''
			Expect(memory.ValidateEntityType("Person")).To(BeFalse())
			Expect(memory.ValidateEntityType("Task")).To(BeFalse())
			Expect(memory.ValidateEntityType("")).To(BeFalse())
			Expect(memory.ValidateEntityType("agent")).To(BeFalse()) // case-sensitive
		})
	})

	Describe("RelationType validation", func() {
		It("accepts only the 5 allowed relation types", func() {
			Expect(memory.ValidateRelationType("uses")).To(BeTrue())
			Expect(memory.ValidateRelationType("implements")).To(BeTrue())
			Expect(memory.ValidateRelationType("related_to")).To(BeTrue())
			Expect(memory.ValidateRelationType("depends_on")).To(BeTrue())
			Expect(memory.ValidateRelationType("created_by")).To(BeTrue())
		})
		It("rejects invalid relation types", func() {
			Expect(memory.ValidateRelationType("owns")).To(BeFalse())
			Expect(memory.ValidateRelationType("manages")).To(BeFalse())
			Expect(memory.ValidateRelationType("")).To(BeFalse())
			Expect(memory.ValidateRelationType("Uses")).To(BeFalse()) // case-sensitive
		})
	})

	Describe("ObservationTag validation", func() {
		It("accepts only the 6 allowed observation tags", func() {
			Expect(memory.ValidateObservationTag("DISCOVERY")).To(BeTrue())
			Expect(memory.ValidateObservationTag("CHANGE")).To(BeTrue())
			Expect(memory.ValidateObservationTag("IMPLICATION")).To(BeTrue())
			Expect(memory.ValidateObservationTag("BEHAVIOR")).To(BeTrue())
			Expect(memory.ValidateObservationTag("CAPABILITY")).To(BeTrue())
			Expect(memory.ValidateObservationTag("LIMITATION")).To(BeTrue())
		})
		It("rejects invalid observation tags", func() {
			Expect(memory.ValidateObservationTag("NOTE")).To(BeFalse())
			Expect(memory.ValidateObservationTag("COMMENT")).To(BeFalse())
			Expect(memory.ValidateObservationTag("")).To(BeFalse())
			Expect(memory.ValidateObservationTag("discovery")).To(BeFalse()) // case-sensitive
		})
	})
})
