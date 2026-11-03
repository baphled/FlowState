package swarm

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LinkChild", func() {
	It("links an admitted packet to a child session", func() {
		log := NewAdmissionLog()
		p := Packet{ID: "pkt-1", Scope: "m2.runtime", Task: "t", SourceSHA256: sha256Hex([]byte("src"))}
		Expect(log.Admit(p, []byte("src"))).To(Succeed())
		err := log.LinkChild(ChildLink{ParentSessionID: "sess-p", ChildSessionID: "sess-c", PacketID: "pkt-1"})
		Expect(err).NotTo(HaveOccurred())
	})

	It("records the child so Children returns it", func() {
		log := NewAdmissionLog()
		p := Packet{ID: "pkt-1", Scope: "m2.runtime", Task: "t", SourceSHA256: sha256Hex([]byte("src"))}
		Expect(log.Admit(p, []byte("src"))).To(Succeed())
		_ = log.LinkChild(ChildLink{ParentSessionID: "sess-p", ChildSessionID: "sess-c", PacketID: "pkt-1"})
		Expect(log.Children("sess-p")).To(HaveLen(1))
	})

	It("rejects a link with an empty parent session id", func() {
		log := NewAdmissionLog()
		err := log.LinkChild(ChildLink{ParentSessionID: "", ChildSessionID: "sess-c", PacketID: "pkt-1"})
		Expect(err).To(MatchError(ContainSubstring("parent and child session ids")))
	})

	It("rejects a link with an empty packet id", func() {
		log := NewAdmissionLog()
		err := log.LinkChild(ChildLink{ParentSessionID: "sess-p", ChildSessionID: "sess-c", PacketID: ""})
		Expect(err).To(MatchError(ContainSubstring("packet id must not be empty")))
	})

	It("rejects a link whose packet is not admitted", func() {
		log := NewAdmissionLog()
		err := log.LinkChild(ChildLink{ParentSessionID: "sess-p", ChildSessionID: "sess-c", PacketID: "ghost"})
		Expect(err).To(MatchError(ContainSubstring("not admitted")))
	})

	It("rejects linking the same child twice", func() {
		log := NewAdmissionLog()
		p := Packet{ID: "pkt-1", Scope: "m2.runtime", Task: "t", SourceSHA256: sha256Hex([]byte("src"))}
		Expect(log.Admit(p, []byte("src"))).To(Succeed())
		_ = log.LinkChild(ChildLink{ParentSessionID: "sess-p", ChildSessionID: "sess-c", PacketID: "pkt-1"})
		err := log.LinkChild(ChildLink{ParentSessionID: "sess-p", ChildSessionID: "sess-c", PacketID: "pkt-1"})
		Expect(err).To(MatchError(ContainSubstring("already linked")))
	})

	It("returns an empty slice for an unknown parent", func() {
		log := NewAdmissionLog()
		Expect(log.Children("nobody")).To(BeEmpty())
	})
})
