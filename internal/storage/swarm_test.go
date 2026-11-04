package storage_test

import (
	"database/sql"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/storage"
	"github.com/baphled/flowstate/internal/swarm"
)

// swarmDB returns a migrated disposable SQLite database.
func swarmDB() *sql.DB {
	db := receiptDB()
	err := storage.MigrateSwarmTables(db)
	Expect(err).NotTo(HaveOccurred())
	return db
}

// storedAdmission arranges an admitted packet and reads its persisted outcome.
func storedAdmission(db *sql.DB) string {
	rec := storage.NewSwarmReceipts(db)
	err := rec.RecordAdmission("run", swarm.Packet{ID: "p1"}, true, "")
	Expect(err).NotTo(HaveOccurred())
	var outcome string
	err = db.QueryRow("SELECT outcome FROM swarm_admissions WHERE packet_id = ?", "p1").Scan(&outcome)
	Expect(err).NotTo(HaveOccurred())
	return outcome
}

// storedMemberDigest arranges a member receipt and reads its persisted digest.
func storedMemberDigest(db *sql.DB) string {
	rec := storage.NewSwarmReceipts(db)
	err := rec.RecordMemberReceipt(swarm.MemberReceipt{
		Identity:    swarm.MemberIdentity{RunName: "run", PacketID: "p1"},
		GateOutcome: "pass", EvidenceSHA256: "known-digest",
	})
	Expect(err).NotTo(HaveOccurred())
	var digest string
	err = db.QueryRow("SELECT evidence_sha256 FROM swarm_member_receipts WHERE packet_id = ?", "p1").Scan(&digest)
	Expect(err).NotTo(HaveOccurred())
	return digest
}

var _ = Describe("Swarm receipt persistence", func() {
	It("reapplying the swarm schema does not fail", func() {
		db := swarmDB()
		Expect(storage.MigrateSwarmTables(db)).To(Succeed())
	})

	It("persists an admitted packet", func() {
		Expect(storedAdmission(swarmDB())).To(Equal("admit"))
	})

	It("persists a member receipt with its evidence digest", func() {
		Expect(storedMemberDigest(swarmDB())).To(Equal("known-digest"))
	})
})
