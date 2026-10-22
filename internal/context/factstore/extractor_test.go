package factstore_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/context/factstore"
	"github.com/baphled/flowstate/internal/provider"
)

func userMsg(text string) provider.Message {
	return provider.Message{Role: "user", Content: text}
}

func assistantMsg(text string) provider.Message {
	return provider.Message{Role: "assistant", Content: text}
}

var _ = Describe("RegexFactExtractor", func() {
	var (
		ctx context.Context
		ex  factstore.FactExtractor
	)

	BeforeEach(func() {
		ctx = context.Background()
		ex = factstore.NewRegexFactExtractor()
	})

	It("captures explicit always/never/remember statements", func() {
		msgs := []provider.Message{
			userMsg("Remember: the qdrant collection is named flowstate-recall."),
			assistantMsg("Got it."),
			userMsg("Always run gofmt before committing."),
			assistantMsg("Understood."),
			userMsg("Never push to main directly."),
		}

		facts, err := ex.Extract(ctx, "sess-1", msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(facts).To(HaveLen(3))
		Expect(facts[0].Text).To(ContainSubstring("flowstate-recall"))
		Expect(facts[1].Text).To(ContainSubstring("gofmt"))
		Expect(facts[2].Text).To(ContainSubstring("Never push to main"))
	})

	It("never extracts from tool-result messages", func() {
		msgs := []provider.Message{
			{Role: "tool", Content: "Always run gofmt before committing."},
		}

		facts, err := ex.Extract(ctx, "sess-2", msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(facts).To(BeEmpty())
	})

	It("returns no facts when nothing matches the patterns", func() {
		msgs := []provider.Message{
			userMsg("hi"),
			assistantMsg("hello"),
		}

		facts, err := ex.Extract(ctx, "sess-3", msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(facts).To(BeEmpty())
	})

	It("stamps SessionID and SourceMessageID on each emitted fact", func() {
		msgs := []provider.Message{
			userMsg("Remember: prefer functional core, imperative shell."),
		}

		facts, err := ex.Extract(ctx, "sess-4", msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(facts).To(HaveLen(1))
		Expect(facts[0].SessionID).To(Equal("sess-4"))
		Expect(facts[0].SourceMessageID).NotTo(BeEmpty())
	})
})
