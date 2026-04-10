package recall_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

var _ = Describe("SessionSource", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns empty slice when store is nil", func() {
		source := recall.NewSessionSource(nil)
		results, err := source.Query(ctx, "anything", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("maps stored messages to observations", func() {
		store := recall.NewEmptyContextStore("test-model")
		store.Append(provider.Message{Role: "user", Content: "hello"})
		source := recall.NewSessionSource(store)
		results, err := source.Query(ctx, "", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Source).To(Equal("session"))
		Expect(results[0].Content).To(Equal("hello"))
	})

	It("respects the limit parameter", func() {
		store := recall.NewEmptyContextStore("test-model")
		store.Append(provider.Message{Role: "user", Content: "msg1"})
		store.Append(provider.Message{Role: "user", Content: "msg2"})
		store.Append(provider.Message{Role: "user", Content: "msg3"})
		source := recall.NewSessionSource(store)
		results, err := source.Query(ctx, "", 2)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
	})

	It("returns all messages when limit is zero", func() {
		store := recall.NewEmptyContextStore("test-model")
		store.Append(provider.Message{Role: "user", Content: "a"})
		store.Append(provider.Message{Role: "user", Content: "b"})
		source := recall.NewSessionSource(store)
		results, err := source.Query(ctx, "", 0)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
	})
})
