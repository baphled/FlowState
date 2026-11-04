package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("Member receipts", func() {
	var claim swarm.MemberClaim

	BeforeEach(func() {
		claim = swarm.MemberClaim{
			Identity:    swarm.MemberIdentity{RunName: "demo.swarm", PacketID: "p1"},
			GateOutcome: "pass",
			Evidence:    "make check green on abc123",
		}
	})

	It("emits a receipt for an admitted member", func() {
		auth := swarm.NewMemberAuthorizer("demo.swarm", []string{"p1"})
		rec, err := swarm.EmitMemberReceipt(auth, claim, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.EvidenceSHA256).To(HaveLen(64))
	})

	It("rejects a member the run did not admit", func() {
		auth := swarm.NewMemberAuthorizer("demo.swarm", []string{"p1"})
		bad := claim
		bad.Identity = swarm.MemberIdentity{RunName: "demo.swarm", PacketID: "p9"}
		_, err := swarm.EmitMemberReceipt(auth, bad, nil)
		Expect(err).To(MatchError(ContainSubstring("was not admitted")))
	})

	It("rejects a claim from a different run", func() {
		auth := swarm.NewMemberAuthorizer("other.swarm", []string{"p1"})
		_, err := swarm.EmitMemberReceipt(auth, claim, nil)
		Expect(err).To(MatchError(ContainSubstring("not this run")))
	})

	It("rejects an invalid outcome", func() {
		auth := swarm.NewMemberAuthorizer("demo.swarm", []string{"p1"})
		bad := claim
		bad.GateOutcome = "maybe"
		_, err := swarm.EmitMemberReceipt(auth, bad, nil)
		Expect(err).To(MatchError(ContainSubstring("pass or fail")))
	})
})
