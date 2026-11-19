package store

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreateChildSession", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	It("returns the created child linked to its parent", func() {
		s := newTestStore()
		parent := mustSession(s)
		child, err := s.CreateChildSession(ctx, "child-link", parent, "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.ParentID).To(Equal(parent))
	})

	It("reads the parent linkage back through GetSession", func() {
		s := newTestStore()
		parent := mustSession(s)
		_, err := s.CreateChildSession(ctx, "child-read", parent, "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		sess, err := s.GetSession(ctx, "child-read")
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.ParentID).To(Equal(parent))
	})

	It("lists child sessions with their parent linkage", func() {
		s := newTestStore()
		parent := mustSession(s)
		_, err := s.CreateChildSession(ctx, "child-list", parent, "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		linked := ""
		for _, row := range all {
			if row.ID == "child-list" {
				linked = row.ParentID
			}
		}
		Expect(linked).To(Equal(parent))
	})

	It("creates a root session when the parent id is empty", func() {
		s := newTestStore()
		child, err := s.CreateChildSession(ctx, "child-root", "", "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.ParentID).To(Equal(""))
	})
})
