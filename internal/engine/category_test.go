package engine_test

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

func TestCategoryDefaults(t *testing.T) {
	RegisterTestingT(t)

	routing := engine.DefaultCategoryRouting()

	Expect(routing).To(HaveLen(6))
	Expect(routing).To(HaveKey("quick"))
	Expect(routing).To(HaveKey("deep"))
	Expect(routing).To(HaveKey("visual-engineering"))
	Expect(routing).To(HaveKey("ultrabrain"))
	Expect(routing).To(HaveKey("unspecified-low"))
	Expect(routing).To(HaveKey("unspecified-high"))

	for _, key := range []string{"quick", "deep", "visual-engineering", "ultrabrain", "unspecified-low", "unspecified-high"} {
		Expect(routing[key].Model).NotTo(BeEmpty())
	}
}
