package store

import "gorm.io/gorm"

// SwarmReceiptLog writes the swarm admission, gate and member receipt
// tables through the ORM onto a caller-owned database handle; the
// caller controls the handle's lifecycle. Call MigrateSwarmReceipts
// first on a database that does not hold the tables yet.
type SwarmReceiptLog struct {
	gdb *gorm.DB
}

// GateResult is one gate outcome to record: the gate name and whether
// it passed.
type GateResult struct {
	Name   string
	Passed bool
}

// MemberReceiptRecord is one admitted member's receipt: the run and
// packet it belongs to, the gate outcome (pass or fail) and the
// SHA-256 of its evidence.
type MemberReceiptRecord struct {
	RunName        string
	PacketID       string
	GateOutcome    string
	EvidenceSHA256 string
}

// swarmAdmissionRow mirrors the swarm_admissions table. The outcome
// CHECK of the legacy DDL is enforced in Go, not by a tag.
type swarmAdmissionRow struct {
	RunID         string `gorm:"column:run_id;type:TEXT;not null"`
	PacketID      string `gorm:"column:packet_id;type:TEXT;not null"`
	Outcome       string `gorm:"column:outcome;type:TEXT;not null"`
	Reason        string `gorm:"column:reason;type:TEXT;not null"`
	RecordedAtUTC string `gorm:"column:recorded_at_utc;type:TEXT;not null"`
}

// swarmGateReceiptRow mirrors the swarm_gate_receipts table.
type swarmGateReceiptRow struct {
	RunID         string `gorm:"column:run_id;type:TEXT;not null"`
	GateName      string `gorm:"column:gate_name;type:TEXT;not null"`
	Outcome       string `gorm:"column:outcome;type:TEXT;not null"`
	RecordedAtUTC string `gorm:"column:recorded_at_utc;type:TEXT;not null"`
}

// swarmMemberReceiptRow mirrors the swarm_member_receipts table.
type swarmMemberReceiptRow struct {
	RunName        string `gorm:"column:run_name;type:TEXT;not null"`
	PacketID       string `gorm:"column:packet_id;type:TEXT;not null"`
	GateOutcome    string `gorm:"column:gate_outcome;type:TEXT;not null"`
	EvidenceSHA256 string `gorm:"column:evidence_sha256;type:TEXT;not null"`
	RecordedAtUTC  string `gorm:"column:recorded_at_utc;type:TEXT;not null"`
}
