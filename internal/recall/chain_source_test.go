package recall_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

type stubChainStore struct {
	chainID  string
	messages []provider.Message
	err      error
}

func (s *stubChainStore) Append(_ string, msg provider.Message) error {
	s.messages = append(s.messages, msg)
	return nil
}

func (s *stubChainStore) Search(_ context.Context, _ string, _ int) ([]recall.SearchResult, error) {
	return nil, nil
}

func (s *stubChainStore) GetByAgent(_ string, last int) ([]provider.Message, error) {
	if s.err != nil {
		return nil, s.err
	}
	if last > 0 && len(s.messages) > last {
		return s.messages[len(s.messages)-last:], nil
	}
	return s.messages, nil
}

func (s *stubChainStore) ChainID() string { return s.chainID }

var _ = Describe("ChainSource", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns empty slice when store is nil", func() {
		source := recall.NewChainSource(nil)
		results, err := source.Query(ctx, "anything", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("maps chain messages to observations", func() {
		stub := &stubChainStore{
			chainID:  "chain-abc",
			messages: []provider.Message{{Role: "assistant", Content: "chain msg"}},
		}
		source := recall.NewChainSource(stub)
		results, err := source.Query(ctx, "", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Source).To(Equal("chain"))
		Expect(results[0].Content).To(Equal("chain msg"))
	})

	It("propagates errors from GetByAgent", func() {
		stub := &stubChainStore{err: errors.New("chain fail")}
		source := recall.NewChainSource(stub)
		_, err := source.Query(ctx, "", 10)
		Expect(err).To(MatchError("chain fail"))
	})

	It("returns empty when store has no messages", func() {
		stub := &stubChainStore{chainID: "c1"}
		source := recall.NewChainSource(stub)
		results, err := source.Query(ctx, "", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})
})
