package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

// admitForLink admits packet id with a valid source digest as fixture
// setup, panicking on failure (never the spec's behavioural expectation).
func admitForLink(log *swarm.AdmissionLog, id string) {
	p := swarm.Packet{ID: id, Scope: "m2.runtime", Task: "t", SourceSHA256: bodyHashOf("src")}
	if err := log.Admit(p, []byte("src")); err != nil {
		panic("fixture admission failed: " + err.Error())
	}
}

// linkFixture is a valid ChildLink fixture for parent/child/packet.
func linkFixture(parent, child, packet string) swarm.ChildLink {
	return swarm.ChildLink{ParentSessionID: parent, ChildSessionID: child, PacketID: packet}
}

var _ = Describe("LinkChild", func() {
	It("links an admitted packet to a child session", func() {
		log := swarm.NewAdmissionLog()
		admitForLink(log, "pkt-1")
		linkedErr := log.LinkChild(linkFixture("sess-p", "sess-c", "pkt-1"))
		Expect(linkedErr).NotTo(HaveOccurred())

		kids := log.Children("sess-p")

		Expect(kids).To(HaveLen(1))
	})

	It("rejects a link with an empty parent session id", func() {
		log := swarm.NewAdmissionLog()

		err := log.LinkChild(swarm.ChildLink{ParentSessionID: "", ChildSessionID: "sess-c", PacketID: "pkt-1"})

		Expect(err).To(MatchError(ContainSubstring("parent and child session ids")))
	})

	It("rejects a link with an empty packet id", func() {
		log := swarm.NewAdmissionLog()

		err := log.LinkChild(swarm.ChildLink{ParentSessionID: "sess-p", ChildSessionID: "sess-c", PacketID: ""})

		Expect(err).To(MatchError(ContainSubstring("packet id must not be empty")))
	})

	It("rejects a link whose packet is not admitted", func() {
		log := swarm.NewAdmissionLog()

		err := log.LinkChild(linkFixture("sess-p", "sess-c", "ghost"))

		Expect(err).To(MatchError(ContainSubstring("not admitted")))
	})

	It("rejects linking the same child twice", func() {
		log := swarm.NewAdmissionLog()
		admitForLink(log, "pkt-1")
		if err := log.LinkChild(linkFixture("sess-p", "sess-c", "pkt-1")); err != nil {
			panic("fixture link failed: " + err.Error())
		}

		err := log.LinkChild(linkFixture("sess-p", "sess-c", "pkt-1"))

		Expect(err).To(MatchError(ContainSubstring("already linked")))
	})

	It("returns an empty slice for an unknown parent", func() {
		log := swarm.NewAdmissionLog()

		Expect(log.Children("nobody")).To(BeEmpty())
	})
})
