package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

// cancelFixture links parent->child->grandchild under admitted packets.
type cancelFixture struct {
	log    *swarm.AdmissionLog
	parent string
}

// newCancelFixture builds a two-level chain of linked sessions.
func newCancelFixture() cancelFixture {
	log := swarm.NewAdmissionLog()
	f := cancelFixture{log: log, parent: "sess-root"}
	root := swarm.Packet{ID: "pkt-root", Scope: "m2", Task: "root",
		SourceSHA256: bodyHashOf("root-src")}
	if err := log.Admit(root, []byte("root-src")); err != nil {
		panic("fixture admission failed: " + err.Error())
	}
	for _, spec := range []struct{ packet, parent, child string }{
		{"pkt-a", "sess-root", "sess-child"},
		{"pkt-b", "sess-child", "sess-grand"},
	} {
		p := swarm.Packet{ID: spec.packet, Scope: "m2.runtime", Task: "t",
			SourceSHA256: bodyHashOf("src"), ParentID: "pkt-root"}
		if err := log.Admit(p, []byte("src")); err != nil {
			panic("fixture admission failed: " + err.Error())
		}
		if err := log.LinkChild(swarm.ChildLink{ParentSessionID: spec.parent, ChildSessionID: spec.child, PacketID: spec.packet}); err != nil {
			panic("fixture link failed: " + err.Error())
		}
	}
	return f
}

var _ = Describe("CancelTree", func() {
	It("cascades cancellation to linked child sessions", func() {
		f := newCancelFixture()
		t := swarm.NewCancelTree()
		cancelErr := t.Cancel(f.parent, f.log)

		Expect(t.Cancelled("sess-grand")).To(BeTrue())
		Expect(cancelErr).NotTo(HaveOccurred())
	})

	It("keeps unrelated sessions running", func() {
		f := newCancelFixture()
		t := swarm.NewCancelTree()
		if err := t.Cancel(f.parent, f.log); err != nil {
			panic("fixture cancel failed: " + err.Error())
		}

		Expect(t.Cancelled("sess-other")).To(BeFalse())
	})

	It("tolerates cancelling the same session twice", func() {
		f := newCancelFixture()
		t := swarm.NewCancelTree()
		if err := t.Cancel(f.parent, f.log); err != nil {
			panic("fixture cancel failed: " + err.Error())
		}
		err := t.Cancel(f.parent, f.log)

		Expect(t.Cancelled(f.parent)).To(BeTrue())
		Expect(err).NotTo(HaveOccurred())
	})

	It("fails closed on an empty session id", func() {
		t := swarm.NewCancelTree()
		err := t.Cancel("", newCancelFixture().log)

		Expect(err).To(MatchError(ContainSubstring("session id must not be empty")))
	})

	It("fails closed on a nil admission log", func() {
		t := swarm.NewCancelTree()
		err := t.Cancel("sess-root", nil)

		Expect(err).To(MatchError(ContainSubstring("admission log must not be nil")))
	})
})
