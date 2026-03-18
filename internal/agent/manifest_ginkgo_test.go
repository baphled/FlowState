package agent_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("AgentManifest", func() {
	Describe("DefaultContextManagement", func() {
		It("returns sensible defaults", func() {
			defaults := agent.DefaultContextManagement()

			Expect(defaults.MaxRecursionDepth).To(Equal(2))
			Expect(defaults.SummaryTier).To(Equal("quick"))
			Expect(defaults.SlidingWindowSize).To(Equal(10))
			Expect(defaults.CompactionThreshold).To(Equal(0.75))
			Expect(defaults.EmbeddingModel).To(Equal("nomic-embed-text"))
		})
	})

	Describe("Validate", func() {
		It("returns nil for valid manifest", func() {
			manifest := &agent.AgentManifest{
				ID:   "valid-agent",
				Name: "Valid Agent",
			}

			err := manifest.Validate()

			Expect(err).NotTo(HaveOccurred())
		})

		It("returns error when ID is empty", func() {
			manifest := &agent.AgentManifest{
				ID:   "",
				Name: "Agent Without ID",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			var validationErr *agent.ValidationError
			Expect(err).To(BeAssignableToTypeOf(validationErr))
			Expect(err.Error()).To(ContainSubstring("id"))
			Expect(err.Error()).To(ContainSubstring("required"))
		})

		It("returns error when Name is empty", func() {
			manifest := &agent.AgentManifest{
				ID:   "agent-without-name",
				Name: "",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			var validationErr *agent.ValidationError
			Expect(err).To(BeAssignableToTypeOf(validationErr))
			Expect(err.Error()).To(ContainSubstring("name"))
			Expect(err.Error()).To(ContainSubstring("required"))
		})

		It("returns ID error first when both ID and Name are empty", func() {
			manifest := &agent.AgentManifest{
				ID:   "",
				Name: "",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("id"))
		})
	})
})

var _ = Describe("ValidationError", func() {
	Describe("Error", func() {
		It("formats error message correctly", func() {
			err := &agent.ValidationError{
				Field:   "test_field",
				Message: "test message",
			}

			Expect(err.Error()).To(Equal("test_field: test message"))
		})
	})
})
