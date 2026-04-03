package session_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

type noopStreamer struct{}

func (n *noopStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "", Done: true}
	close(ch)
	return ch, nil
}

var _ = Describe("Session hierarchy", Label("integration"), func() {
	var mgr *session.Manager

	BeforeEach(func() {
		mgr = session.NewManager(&noopStreamer{})
	})

	It("RegisterSession makes parent visible to ChildSessions as empty", func() {
		mgr.RegisterSession("parent-1", "coordinator")

		children, err := mgr.ChildSessions("parent-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(BeEmpty())
	})

	It("CreateWithParent requires parent to be registered first", func() {
		_, err := mgr.CreateWithParent("unregistered-parent", "worker")
		Expect(err).To(MatchError(session.ErrSessionNotFound))
	})

	It("CreateWithParent creates child with correct ParentID", func() {
		mgr.RegisterSession("parent-2", "coordinator")

		child, err := mgr.CreateWithParent("parent-2", "worker")
		Expect(err).NotTo(HaveOccurred())
		Expect(child).NotTo(BeNil())
		Expect(child.ParentID).To(Equal("parent-2"))
		Expect(child.AgentID).To(Equal("worker"))
	})

	It("ChildSessions returns all direct children", func() {
		mgr.RegisterSession("parent-3", "coordinator")

		child1, err := mgr.CreateWithParent("parent-3", "worker-a")
		Expect(err).NotTo(HaveOccurred())

		child2, err := mgr.CreateWithParent("parent-3", "worker-b")
		Expect(err).NotTo(HaveOccurred())

		children, err := mgr.ChildSessions("parent-3")
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(HaveLen(2))

		childIDs := []string{children[0].ID, children[1].ID}
		Expect(childIDs).To(ContainElement(child1.ID))
		Expect(childIDs).To(ContainElement(child2.ID))
	})

	It("ChildSessions returns empty for unregistered parent", func() {
		children, err := mgr.ChildSessions("never-registered")
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(BeEmpty())
	})

	It("ChildSessions returns empty when parent has no children", func() {
		mgr.RegisterSession("parent-4", "coordinator")

		children, err := mgr.ChildSessions("parent-4")
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(BeEmpty())
	})

	It("multiple children all returned for same parent", func() {
		mgr.RegisterSession("parent-5", "coordinator")

		expectedCount := 3
		for range expectedCount {
			_, err := mgr.CreateWithParent("parent-5", "worker")
			Expect(err).NotTo(HaveOccurred())
		}

		children, err := mgr.ChildSessions("parent-5")
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(HaveLen(expectedCount))
	})
})
