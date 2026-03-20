package prompt_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/prompt"
)

var _ = Describe("Embed", func() {
	Describe("GetPrompt", func() {
		It("returns the placeholder prompt content", func() {
			content, err := prompt.GetPrompt("placeholder")
			Expect(err).NotTo(HaveOccurred())
			Expect(content).NotTo(BeEmpty())
			Expect(content).To(ContainSubstring("placeholder"))
		})

		It("returns an error for nonexistent prompt", func() {
			content, err := prompt.GetPrompt("nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(content).To(BeEmpty())
		})
	})

	Describe("HasPrompt", func() {
		It("returns true for placeholder prompt", func() {
			Expect(prompt.HasPrompt("placeholder")).To(BeTrue())
		})

		It("returns false for nonexistent prompt", func() {
			Expect(prompt.HasPrompt("nonexistent")).To(BeFalse())
		})
	})

	Describe("ListPrompts", func() {
		It("returns a slice containing placeholder", func() {
			prompts := prompt.ListPrompts()
			Expect(prompts).To(ContainElement("placeholder"))
		})

		It("returns a non-empty slice", func() {
			prompts := prompt.ListPrompts()
			Expect(prompts).NotTo(BeEmpty())
		})
	})
})
