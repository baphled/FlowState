package store

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stRawExec runs one statement against the store's raw handle; the SQL
// contract guard only scans non-test files.
func stRawExec(st *Store, query string, args ...any) {
	if _, err := st.db.Exec(query, args...); err != nil {
		panic(err)
	}
}

var _ = Describe("Delegation chain", func() {
	seedChain := func() (*Store, *Session, *Session, *Session) {
		st, err := OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		ctx := context.Background()
		root, err := st.CreateSession(ctx, "chain-root", "prov", "model-x", "pi", "sha")
		if err != nil {
			panic(err)
		}
		child, err := st.CreateChildSession(ctx, "chain-child", root.ID, "prov", "model-x", "pi", "sha")
		if err != nil {
			panic(err)
		}
		nested, err := st.CreateChildSession(ctx, "chain-nested", child.ID, "prov", "model-x", "pi", "sha")
		if err != nil {
			panic(err)
		}
		return st, root, child, nested
	}

	It("returns an empty chain for a root session", func() {
		st, root, _, _ := seedChain()
		chain, err := st.DelegationChain(context.Background(), root.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(chain).To(BeEmpty())
	})

	It("returns the root as the only ancestor of a child", func() {
		st, root, _, _ := seedChain()
		chain, cerr := st.DelegationChain(context.Background(), "chain-child")
		if cerr != nil {
			panic(cerr)
		}
		ids := make([]string, len(chain))
		for i, row := range chain {
			ids[i] = row.ID
		}
		Expect(ids).To(Equal([]string{root.ID}))
	})

	It("returns root-first ancestors for a nested child", func() {
		st, root, child, _ := seedChain()
		chain, cerr := st.DelegationChain(context.Background(), "chain-nested")
		if cerr != nil {
			panic(cerr)
		}
		Expect([]string{chain[0].ID, chain[1].ID}).To(Equal([]string{root.ID, child.ID}))
	})

	It("fails closed with a cycle sentinel when a parent chain loops", func() {
		st, _, child, nested := seedChain()
		// Corrupt the store directly: child's parent becomes its own
		// descendant, which creation can never produce but external
		// tampering could. Raw SQL is permitted in test files.
		stRawExec(st, `UPDATE sessions SET parent_id=? WHERE id=?`, nested.ID, child.ID)
		_, err := st.DelegationChain(context.Background(), "chain-child")
		Expect(err).To(MatchError(ErrDelegationCycle))
	})

	It("stops defensive walks beyond the depth cap", func() {
		st, root, _, _ := seedChain()
		// A self-referential root loops immediately; the walk must stop
		// at the cap rather than spin, surfacing the cycle sentinel.
		stRawExec(st, `UPDATE sessions SET parent_id=? WHERE id=?`, root.ID, root.ID)
		_, err := st.DelegationChain(context.Background(), "chain-child")
		Expect(err).To(MatchError(ErrDelegationCycle))
	})

	It("keeps the chain durable across a reopen", func() {
		ctx := context.Background()
		path := filepath.Join(GinkgoT().TempDir(), "chain.db")
		st, err := Open(path)
		if err != nil {
			panic(err)
		}
		root, err := st.CreateSession(ctx, "reopen-root", "prov", "model-x", "pi", "sha")
		if err != nil {
			panic(err)
		}
		child, err := st.CreateChildSession(ctx, "reopen-child", root.ID, "prov", "model-x", "pi", "sha")
		if err != nil {
			panic(err)
		}
		if err := st.Close(); err != nil {
			panic(err)
		}
		st2, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st2.Close() })
		chain, cerr := st2.DelegationChain(ctx, child.ID)
		if cerr != nil {
			panic(cerr)
		}
		Expect([]string{chain[0].ID}).To(Equal([]string{root.ID}))
	})
})
