package factstore_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/context/factstore"
	"github.com/baphled/flowstate/internal/provider"
)

type stubExtractor struct {
	out []factstore.Fact
}

func (s stubExtractor) Extract(_ context.Context, sessionID string, _ []provider.Message) ([]factstore.Fact, error) {
	out := make([]factstore.Fact, len(s.out))
	for i, f := range s.out {
		f.SessionID = sessionID
		out[i] = f
	}
	return out, nil
}

var _ = Describe("Service", func() {
	var (
		ctx       context.Context
		root      string
		sessionID string
		svc       *factstore.Service
	)

	BeforeEach(func() {
		ctx = context.Background()
		root = GinkgoT().TempDir()
		sessionID = "sess-svc-1"
	})

	It("ingests a 5-message session and persists three stub facts to JSONL", func() {
		stub := stubExtractor{out: []factstore.Fact{
			{Text: "user prefers terse responses", SourceMessageID: "m0"},
			{Text: "the qdrant collection is named flowstate-recall", SourceMessageID: "m1"},
			{Text: "always run gofmt before committing", SourceMessageID: "m2"},
		}}
		svc = factstore.NewService(factstore.NewFileFactStore(root), stub, factstore.DefaultConfig())

		msgs := []provider.Message{
			userMsg("a"), assistantMsg("b"), userMsg("c"), assistantMsg("d"), userMsg("e"),
		}
		Expect(svc.IngestSession(ctx, sessionID, msgs)).To(Succeed())

		listed, err := svc.List(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(3))
	})

	It("recalls topK facts ranked by query overlap", func() {
		stub := stubExtractor{out: []factstore.Fact{
			{Text: "the qdrant collection is named flowstate-recall", SourceMessageID: "m1"},
			{Text: "user prefers terse responses", SourceMessageID: "m2"},
			{Text: "always run gofmt before committing", SourceMessageID: "m3"},
		}}
		cfg := factstore.DefaultConfig()
		cfg.RecallTopK = 2
		svc = factstore.NewService(factstore.NewFileFactStore(root), stub, cfg)

		Expect(svc.IngestSession(ctx, sessionID, []provider.Message{userMsg("seed")})).To(Succeed())

		hits, err := svc.Recall(ctx, sessionID, "qdrant collection", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(2))
		Expect(hits[0].Text).To(ContainSubstring("qdrant"))
	})

	It("falls back to cfg.RecallTopK when topK<=0", func() {
		stub := stubExtractor{out: []factstore.Fact{
			{Text: "fact one", SourceMessageID: "m1"},
			{Text: "fact two", SourceMessageID: "m2"},
		}}
		cfg := factstore.DefaultConfig()
		cfg.RecallTopK = 1
		svc = factstore.NewService(factstore.NewFileFactStore(root), stub, cfg)

		Expect(svc.IngestSession(ctx, sessionID, []provider.Message{userMsg("seed")})).To(Succeed())
		hits, err := svc.Recall(ctx, sessionID, "fact", -1)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
	})
})
