package invalid_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/invalid"
)

// Invalid tool tests verify the sentinel "invalid" tool reports the correct
// metadata and always returns a non-nil result.Error so callers can detect
// invalid tool calls without relying on Go errors.
var _ = Describe("Invalid tool", func() {
	It("reports its name as 'invalid'", func() {
		toolUnderTest := invalid.New()
		Expect(toolUnderTest.Name()).To(Equal("invalid"))
	})

	It("returns a non-nil result.Error when executed", func() {
		result, err := invalid.New().Execute(context.Background(), tool.Input{Name: "invalid"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).To(HaveOccurred())
	})
})
