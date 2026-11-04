package app_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("SwarmEmitMemberReceipt", func() {
	var claim swarm.MemberClaim

	BeforeEach(func() {
		claim = swarm.MemberClaim{
			Identity:    swarm.MemberIdentity{RunName: "demo.swarm", PacketID: "p1"},
			GateOutcome: "pass",
			Evidence:    "make check green",
		}
	})

	It("persists a member receipt for an admitted packet", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		rec, err := app.SwarmEmitMemberReceipt(dbPath, "demo.swarm", []string{"p1"}, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Identity.PacketID).To(Equal("p1"))
	})
})
