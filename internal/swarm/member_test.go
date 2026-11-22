package swarm_test

import (
	"context"
	"time"

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
		rec, err := swarm.EmitMemberReceipt(context.Background(), auth, claim, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.EvidenceSHA256).To(HaveLen(64))
	})

	It("rejects a member the run did not admit", func() {
		auth := swarm.NewMemberAuthorizer("demo.swarm", []string{"p1"})
		bad := claim
		bad.Identity = swarm.MemberIdentity{RunName: "demo.swarm", PacketID: "p9"}
		_, err := swarm.EmitMemberReceipt(context.Background(), auth, bad, nil)
		Expect(err).To(MatchError(ContainSubstring("was not admitted")))
	})

	It("rejects a claim from a different run", func() {
		auth := swarm.NewMemberAuthorizer("other.swarm", []string{"p1"})
		_, err := swarm.EmitMemberReceipt(context.Background(), auth, claim, nil)
		Expect(err).To(MatchError(ContainSubstring("not this run")))
	})

	It("rejects an invalid outcome", func() {
		auth := swarm.NewMemberAuthorizer("demo.swarm", []string{"p1"})
		bad := claim
		bad.GateOutcome = "maybe"
		_, err := swarm.EmitMemberReceipt(context.Background(), auth, bad, nil)
		Expect(err).To(MatchError(ContainSubstring("pass or fail")))
	})

	Describe("the context handed to the sink", func() {
		var (
			sink *ctxRecordingSink
			auth swarm.MemberAuthorizer
		)

		BeforeEach(func() {
			sink = &ctxRecordingSink{}
			auth = swarm.NewMemberAuthorizer("demo.swarm", []string{"p1"})
		})

		It("is not cancelled when the caller context already is", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := swarm.EmitMemberReceipt(ctx, auth, claim, sink); err != nil {
				panic(err)
			}
			Expect(sink.ctx.Err()).NotTo(HaveOccurred())
		})

		It("carries the values of the caller context", func() {
			ctx := context.WithValue(context.Background(), ctxMarkerKey{}, "marker")
			if _, err := swarm.EmitMemberReceipt(ctx, auth, claim, sink); err != nil {
				panic(err)
			}
			Expect(sink.ctx.Value(ctxMarkerKey{})).To(Equal("marker"))
		})

		It("has no deadline when the caller context has one", func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			defer cancel()
			if _, err := swarm.EmitMemberReceipt(ctx, auth, claim, sink); err != nil {
				panic(err)
			}
			_, has := sink.ctx.Deadline()
			Expect(has).To(BeFalse())
		})

		It("is never reached for an unadmitted claim", func() {
			bad := claim
			bad.Identity.PacketID = "p9"
			_, _ = swarm.EmitMemberReceipt(context.Background(), auth, bad, sink)
			Expect(sink.ctx).To(BeNil())
		})
	})
})
