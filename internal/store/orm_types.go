package store

import "time"

// schemaMigrationRow mirrors schema_migrations (v1 bootstrap).
type schemaMigrationRow struct {
	Version      int64  `gorm:"column:version;primaryKey;not null;type:integer"`
	AppliedAtUTC string `gorm:"column:applied_at_utc;not null;type:text"`
}

// sessionRow mirrors the sessions table (v2 DDL plus v4 parent_id and
// v5 budget_pool); ParentID and BudgetPool are pointers (roots and
// unbudgeted sessions persist NULL, read back empty/nil). The status
// whitelist is domain-layer; no DB CHECK (see PR discussion).
type sessionRow struct {
	ID           string  `gorm:"column:id;primaryKey;not null;type:text"`
	CreatedAtUTC string  `gorm:"column:created_at_utc;not null;type:text"`
	UpdatedAtUTC string  `gorm:"column:updated_at_utc;not null;type:text"`
	Status       string  `gorm:"column:status;not null;type:text;index:idx_sessions_status"`
	Provider     string  `gorm:"column:provider;not null;type:text"`
	ModelID      string  `gorm:"column:model_id;not null;type:text"`
	Harness      string  `gorm:"column:harness;not null;type:text"`
	ConfigSHA    string  `gorm:"column:config_sha256;not null;type:text"`
	ParentID     *string `gorm:"column:parent_id;type:text"`
	BudgetPool   *int64  `gorm:"column:budget_pool;type:integer"`
}

// eventRow mirrors the events table (v2 DDL): composite PK
// (session_id, seq), canonical payload JSON and its sha256.
type eventRow struct {
	SessionID    string `gorm:"column:session_id;primaryKey;not null;type:text;index:idx_events_session_type"`
	Seq          int64  `gorm:"column:seq;primaryKey;not null;type:integer"`
	CreatedAtUTC string `gorm:"column:created_at_utc;not null;type:text"`
	Actor        string `gorm:"column:actor;not null;type:text"`
	Type         string `gorm:"column:type;not null;type:text;index:idx_events_session_type"`
	PayloadSHA   string `gorm:"column:payload_sha256;not null;type:text"`
	PayloadJSON  string `gorm:"column:payload_json;not null;type:text"`
}

// receiptRow mirrors the receipts table (v2 DDL); no DB-level
// gate_outcome CHECK (dropped to avoid legacy rebuilds).
type receiptRow struct {
	ID            string `gorm:"column:id;primaryKey;not null;type:text"`
	CapabilityID  string `gorm:"column:capability_id;not null;type:text"`
	CommitSHA     string `gorm:"column:commit_sha;not null;type:text"`
	ReceiptRef    string `gorm:"column:receipt_ref;not null;type:text"`
	ReceiptSHA256 string `gorm:"column:receipt_sha256;not null;type:text"`
	GateOutcome   string `gorm:"column:gate_outcome;not null;type:text"`
	RecordedAtUTC string `gorm:"column:recorded_at_utc;not null;type:text"`
}

// leaseRow mirrors the leases table (v3 DDL).
type leaseRow struct {
	Item          string `gorm:"column:item;primaryKey;not null;type:text"`
	Agent         string `gorm:"column:agent;not null;type:text"`
	Token         int64  `gorm:"column:token;not null;type:integer"`
	AcquiredAtUTC string `gorm:"column:acquired_at_utc;not null;type:text"`
}

// leaseHistoryRow mirrors the lease_history table (v3 DDL).
type leaseHistoryRow struct {
	Item  string `gorm:"column:item;primaryKey;not null;type:text"`
	Token int64  `gorm:"column:token;primaryKey;not null;type:integer"`
}

// budgetReservationRow mirrors the budget_reservations table (v5
// DDL): one durable grant per child session against the parent's
// declared pool. The status whitelist ('reserved','charged') is
// domain-layer; no DB CHECK (see sessionRow comment).
type budgetReservationRow struct {
	ID           string `gorm:"column:id;primaryKey;not null;type:text"`
	ParentID     string `gorm:"column:parent_id;not null;type:text"`
	ChildID      string `gorm:"column:child_id;not null;type:text;uniqueIndex:idx_budget_reservations_child_id"`
	Granted      int64  `gorm:"column:granted;not null;type:integer"`
	Consumed     int64  `gorm:"column:consumed;not null;default:0;type:integer"`
	Status       string `gorm:"column:status;not null;type:text"`
	CreatedAtUTC string `gorm:"column:created_at_utc;not null;type:text"`
}

// sessionMessageRow mirrors the session_messages table (v6): one
// durable steering directive addressed to a child session, delivered
// when the child's run drains it at the next turn boundary.
type sessionMessageRow struct {
	ID           string  `gorm:"column:id;primaryKey;not null;type:text"`
	SessionID    string  `gorm:"column:session_id;not null;type:text;index:idx_session_messages_session_id"`
	Sender       string  `gorm:"column:sender;not null;type:text"`
	Body         string  `gorm:"column:body;not null;type:text"`
	CreatedAtUTC string  `gorm:"column:created_at_utc;not null;type:text"`
	DeliveredAt  *string `gorm:"column:delivered_at_utc;type:text"`
}

// SessionSteeringMessage is the wire shape of one steering message.
type SessionSteeringMessage struct {
	ID        string
	SessionID string
	Sender    string
	Body      string
	CreatedAt time.Time
}

// swarmRunRow mirrors the swarm_runs table (v7): one durable record
// per configured-swarm run; the outcome whitelist is domain-layer
// (no DB CHECK, see sessionRow comment).
type swarmRunRow struct {
	ID             string `gorm:"column:id;primaryKey;not null;type:text"`
	Name           string `gorm:"column:name;not null;type:text"`
	ManifestSHA256 string `gorm:"column:manifest_sha256;not null;type:text"`
	Outcome        string `gorm:"column:outcome;not null;type:text"`
	CreatedAtUTC   string `gorm:"column:created_at_utc;not null;type:text"`
}

// swarmRunPacketRow mirrors the swarm_run_packets table (v7): one
// admission decision per packet; SQLite stores the boolean as 1/0
// and the run link carries no FK (run and rows persist together).
type swarmRunPacketRow struct {
	ID           string `gorm:"column:id;primaryKey;not null;type:text"`
	RunID        string `gorm:"column:run_id;not null;type:text;index:idx_swarm_run_packets_run_id"`
	PacketID     string `gorm:"column:packet_id;not null;type:text"`
	Admitted     int64  `gorm:"column:admitted;not null;type:integer"`
	Reason       string `gorm:"column:reason;not null;type:text"`
	CreatedAtUTC string `gorm:"column:created_at_utc;not null;type:text"`
}
