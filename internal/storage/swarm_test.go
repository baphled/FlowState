package storage_test

import (
	"database/sql"

	sqlite "github.com/libtnb/sqlite"
	"gorm.io/gorm"

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
	rec, err := storage.NewSwarmReceipts(db)
	Expect(err).NotTo(HaveOccurred())
	err = rec.RecordAdmission("run", swarm.Packet{ID: "p1"}, true, "")
	Expect(err).NotTo(HaveOccurred())
	mapped, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	var row struct{ Outcome string }
	err = mapped.Table("swarm_admissions").Where(map[string]any{"packet_id": "p1"}).Take(&row).Error
	Expect(err).NotTo(HaveOccurred())
	return row.Outcome
}

// storedMemberDigest arranges a member receipt and reads its persisted digest.
func storedMemberDigest(db *sql.DB) string {
	rec, err := storage.NewSwarmReceipts(db)
	Expect(err).NotTo(HaveOccurred())
	err = rec.RecordMemberReceipt(swarm.MemberReceipt{
		Identity:    swarm.MemberIdentity{RunName: "run", PacketID: "p1"},
		GateOutcome: "pass", EvidenceSHA256: "known-digest",
	})
	Expect(err).NotTo(HaveOccurred())
	mapped, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	var row struct{ EvidenceSHA256 string }
	err = mapped.Table("swarm_member_receipts").Where(map[string]any{"packet_id": "p1"}).Take(&row).Error
	Expect(err).NotTo(HaveOccurred())
	return row.EvidenceSHA256
}

// storedSwarmGateOutcome verifies that a gate decision is persisted via GORM.
func storedSwarmGateOutcome(db *sql.DB) string {
	rec, err := storage.NewSwarmReceipts(db)
	Expect(err).NotTo(HaveOccurred())
	err = rec.RecordGates("run", swarm.GatesDecision{
		Outcomes: []swarm.GateOutcome{{Gate: swarm.Gate{Name: "lint"}, Passed: true}},
	})
	Expect(err).NotTo(HaveOccurred())
	mapped, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	var row struct{ Outcome string }
	err = mapped.Table("swarm_gate_receipts").Where(map[string]any{"gate_name": "lint"}).Take(&row).Error
	Expect(err).NotTo(HaveOccurred())
	return row.Outcome
}

// invalidSwarmMemberOutcome returns the database constraint error for an invalid state.
func invalidSwarmMemberOutcome(db *sql.DB) error {
	rec, err := storage.NewSwarmReceipts(db)
	Expect(err).NotTo(HaveOccurred())
	return rec.RecordMemberReceipt(swarm.MemberReceipt{
		Identity:    swarm.MemberIdentity{RunName: "run", PacketID: "bad"},
		GateOutcome: "neither-pass-nor-fail", EvidenceSHA256: "digest",
	})
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

	It("persists a passing gate decision", func() {
		Expect(storedSwarmGateOutcome(swarmDB())).To(Equal("pass"))
	})

	It("rejects an invalid receipt outcome at the database boundary", func() {
		Expect(invalidSwarmMemberOutcome(swarmDB())).To(HaveOccurred())
	})
})
