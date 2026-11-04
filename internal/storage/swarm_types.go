package storage

import "gorm.io/gorm"

// SwarmReceipts implements swarm persistence using mapped GORM models.
type SwarmReceipts struct {
	db *gorm.DB
}

// swarmAdmission represents a durable packet admission decision.
type swarmAdmission struct {
	RunID         string `gorm:"column:run_id;type:TEXT;not null"`
	PacketID       string `gorm:"column:packet_id;type:TEXT;not null"`
	Outcome       string `gorm:"column:outcome;type:TEXT;not null;check:chk_swarm_admission_outcome,outcome IN ('admit','reject')"`
	Reason        string `gorm:"column:reason;type:TEXT;not null"`
	RecordedAtUTC  string `gorm:"column:recorded_at_utc;type:TEXT;not null"`
}

// swarmGateReceipt represents one durable gate result.
type swarmGateReceipt struct {
	RunID         string `gorm:"column:run_id;type:TEXT;not null"`
	GateName      string `gorm:"column:gate_name;type:TEXT;not null"`
	Outcome       string `gorm:"column:outcome;type:TEXT;not null;check:chk_swarm_gate_outcome,outcome IN ('pass','fail')"`
	RecordedAtUTC string `gorm:"column:recorded_at_utc;type:TEXT;not null"`
}

// swarmMemberReceipt records an admitted member's evidence digest.
type swarmMemberReceipt struct {
	RunName        string `gorm:"column:run_name;type:TEXT;not null"`
	PacketID      string `gorm:"column:packet_id;type:TEXT;not null"`
	GateOutcome    string `gorm:"column:gate_outcome;type:TEXT;not null;check:chk_swarm_member_outcome,gate_outcome IN ('pass','fail')"`
	EvidenceSHA256 string `gorm:"column:evidence_sha256;type:TEXT;not null"`
	RecordedAtUTC string `gorm:"column:recorded_at_utc;type:TEXT;not null"`
}
