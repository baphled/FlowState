package engine_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("DefaultCategoryRouting", func() {
	It("returns all six expected categories", func() {
		routing := engine.DefaultCategoryRouting()

		Expect(routing).To(HaveLen(6))
		Expect(routing).To(HaveKey("quick"))
		Expect(routing).To(HaveKey("deep"))
		Expect(routing).To(HaveKey("visual-engineering"))
		Expect(routing).To(HaveKey("ultrabrain"))
		Expect(routing).To(HaveKey("unspecified-low"))
		Expect(routing).To(HaveKey("unspecified-high"))
	})

	It("provides a non-empty model for every default category", func() {
		routing := engine.DefaultCategoryRouting()

		for _, key := range []string{"quick", "deep", "visual-engineering", "ultrabrain", "unspecified-low", "unspecified-high"} {
			Expect(routing[key].Model).NotTo(BeEmpty(), "category %q has empty model", key)
		}
	})
})
