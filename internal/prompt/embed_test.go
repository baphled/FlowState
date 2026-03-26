package prompt_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/prompt"
)

var _ = Describe("Embed", func() {
	Describe("GetPrompt", func() {
		It("returns the default-assistant prompt content", func() {
			content, err := prompt.GetPrompt("default-assistant")
			Expect(err).NotTo(HaveOccurred())
			Expect(content).NotTo(BeEmpty())
			Expect(content).To(ContainSubstring("general-purpose AI assistant"))
		})

		It("returns an error for nonexistent prompt", func() {
			content, err := prompt.GetPrompt("nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(content).To(BeEmpty())
		})
	})

	Describe("HasPrompt", func() {
		It("returns true for default-assistant prompt", func() {
			Expect(prompt.HasPrompt("default-assistant")).To(BeTrue())
		})

		It("returns false for nonexistent prompt", func() {
			Expect(prompt.HasPrompt("nonexistent")).To(BeFalse())
		})
	})

	Describe("ListPrompts", func() {
		It("returns a slice containing default-assistant", func() {
			prompts := prompt.ListPrompts()
			Expect(prompts).To(ContainElement("default-assistant"))
		})

		It("returns a non-empty slice", func() {
			prompts := prompt.ListPrompts()
			Expect(prompts).NotTo(BeEmpty())
		})
	})
})
