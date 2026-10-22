package factstore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/context/factstore"
)

func newFileStore(root string) *factstore.FileFactStore {
	return factstore.NewFileFactStore(root)
}

func mkFact(text, source string, when time.Time) factstore.Fact {
	return factstore.Fact{
		Text:            text,
		SourceMessageID: source,
		CreatedAt:       when,
	}
}

func textsOf(facts []factstore.Fact) []string {
	out := make([]string, len(facts))
	for i := range facts {
		out[i] = facts[i].Text
	}
	return out
}

var _ = Describe("FileFactStore", func() {
	var (
		ctx       context.Context
		root      string
		sessionID string
		store     *factstore.FileFactStore
	)

	BeforeEach(func() {
		ctx = context.Background()
		root = GinkgoT().TempDir()
		sessionID = "sess-fact-1"
		store = newFileStore(root)
	})

	It("appends facts to a per-session JSONL file with 0o600 perms", func() {
		err := store.Append(ctx, sessionID,
			mkFact("user prefers terse responses", "msg-0", time.Unix(1, 0)),
			mkFact("the qdrant collection is named flowstate-recall", "msg-1", time.Unix(2, 0)),
		)
		Expect(err).NotTo(HaveOccurred())

		path := filepath.Join(root, sessionID, "facts.jsonl")
		info, statErr := os.Stat(path)
		Expect(statErr).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))

		body, readErr := os.ReadFile(path)
		Expect(readErr).NotTo(HaveOccurred())

		lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
		Expect(lines).To(HaveLen(2))
		Expect(lines[0]).To(ContainSubstring("user prefers terse responses"))
		Expect(lines[1]).To(ContainSubstring("flowstate-recall"))
	})

	It("dedups facts by ID across repeated Append calls", func() {
		fact := mkFact("always run gofmt before committing", "msg-0", time.Unix(1, 0))
		Expect(store.Append(ctx, sessionID, fact)).To(Succeed())
		Expect(store.Append(ctx, sessionID, fact)).To(Succeed())
		Expect(store.Append(ctx, sessionID, fact, fact)).To(Succeed())

		listed, err := store.List(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(1))
	})

	It("survives a process restart: List rereads the JSONL", func() {
		Expect(store.Append(ctx, sessionID,
			mkFact("the OAuth client_id is 7af9", "msg-0", time.Unix(1, 0)),
			mkFact("Use master as the trunk branch", "msg-1", time.Unix(2, 0)),
		)).To(Succeed())

		fresh := newFileStore(root)
		listed, err := fresh.List(ctx, sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(textsOf(listed)).To(ConsistOf(
			"the OAuth client_id is 7af9",
			"Use master as the trunk branch",
		))
	})

	It("ranks facts by query overlap and respects topK", func() {
		Expect(store.Append(ctx, sessionID,
			mkFact("the qdrant collection is named flowstate-recall", "m1", time.Unix(1, 0)),
			mkFact("user prefers terse responses", "m2", time.Unix(2, 0)),
			mkFact("the OAuth client_id is 7af9", "m3", time.Unix(3, 0)),
			mkFact("always run gofmt before committing", "m4", time.Unix(4, 0)),
		)).To(Succeed())

		hits, err := store.Recall(ctx, sessionID, "qdrant collection name", 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(2))
		Expect(hits[0].Text).To(ContainSubstring("qdrant collection"))
	})

	It("returns nil when topK is zero or negative", func() {
		Expect(store.Append(ctx, sessionID,
			mkFact("user prefers terse responses", "m1", time.Unix(1, 0)),
		)).To(Succeed())

		hits, err := store.Recall(ctx, sessionID, "anything", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(BeNil())
	})

	It("returns an empty list for an unknown session without error", func() {
		listed, err := store.List(ctx, "no-such-session")
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(BeEmpty())
	})

	It("exposes the on-disk path for the session", func() {
		Expect(store.Path(sessionID)).To(Equal(filepath.Join(root, sessionID, "facts.jsonl")))
	})
})
