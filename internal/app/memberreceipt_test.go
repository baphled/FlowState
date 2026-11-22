package app_test

import (
	"context"
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
		rec, err := app.SwarmEmitMemberReceipt(context.Background(), dbPath, "demo.swarm", []string{"p1"}, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Identity.PacketID).To(Equal("p1"))
	})

	It("stores the member receipt row with its outcome and evidence digest", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		rec, err := app.SwarmEmitMemberReceipt(context.Background(), dbPath, "demo.swarm", []string{"p1"}, claim)
		if err != nil {
			panic(err)
		}
		row := receiptRow(dbPath,
			"SELECT run_name, packet_id, gate_outcome, evidence_sha256 FROM swarm_member_receipts", 4)
		Expect(row).To(Equal([]string{"demo.swarm", "p1", "pass", rec.EvidenceSHA256}))
	})

	It("stores a failing member outcome as fail", func() {
		claim.GateOutcome = "fail"
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		if _, err := app.SwarmEmitMemberReceipt(context.Background(), dbPath, "demo.swarm", []string{"p1"}, claim); err != nil {
			panic(err)
		}
		row := receiptRow(dbPath, "SELECT gate_outcome FROM swarm_member_receipts", 1)
		Expect(row).To(Equal([]string{"fail"}))
	})

	It("records a finished packet's receipt after the caller context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		if _, err := app.SwarmEmitMemberReceipt(ctx, dbPath, "demo.swarm", []string{"p1"}, claim); err != nil {
			panic(err)
		}
		row := receiptRow(dbPath, "SELECT packet_id FROM swarm_member_receipts", 1)
		Expect(row).To(Equal([]string{"p1"}))
	})
})
