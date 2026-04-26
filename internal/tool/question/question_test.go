package question_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/question"
)

// Question tool tests cover metadata reporting (name, description, schema)
// and the synchronous execution path that returns a structured result with
// the question text and the allow_multiple flag mirrored into Metadata.
var _ = Describe("Question tool", func() {
	Describe("metadata", func() {
		var toolUnderTest *question.Tool

		BeforeEach(func() {
			toolUnderTest = question.New()
		})

		It("reports its name as 'question'", func() {
			Expect(toolUnderTest.Name()).To(Equal("question"))
		})

		It("provides a non-empty description", func() {
			Expect(toolUnderTest.Description()).NotTo(BeEmpty())
		})

		It("declares an object-typed schema", func() {
			Expect(toolUnderTest.Schema().Type).To(Equal("object"))
		})
	})

	Describe("Execute", func() {
		It("returns a result with the question metadata populated", func() {
			result, err := question.New().Execute(context.Background(), tool.Input{
				Name: "question",
				Arguments: map[string]any{
					"question":       "What should I do next?",
					"options":        []any{"Plan", "Build"},
					"allow_multiple": true,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Title).To(Equal("Question"))
			Expect(result.Metadata).To(HaveKeyWithValue("question", "What should I do next?"))
			Expect(result.Metadata).To(HaveKeyWithValue("allow_multiple", true))
		})
	})
})
