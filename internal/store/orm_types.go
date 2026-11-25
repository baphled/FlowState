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

	// Parent declares the parent_id -> sessions.id foreign key; roots
	// persist a NULL parent_id, which a foreign key does not check.
	Parent *sessionRow `gorm:"foreignKey:ParentID;references:ID"`
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

	// Session declares the session_id -> sessions.id foreign key.
	Session *sessionRow `gorm:"foreignKey:SessionID;references:ID"`
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

// leaseRow mirrors the leases table (v3 DDL). Machine, Host and Pid arrive in
// v10 (ADR-011) with defaults, so AutoMigrate can add them to rows that exist.
// Nothing writes or reads them yet: a row keeps Machine = "".
type leaseRow struct {
	Item          string `gorm:"column:item;primaryKey;not null;type:text"`
	Agent         string `gorm:"column:agent;not null;type:text"`
	Token         int64  `gorm:"column:token;not null;type:integer"`
	AcquiredAtUTC string `gorm:"column:acquired_at_utc;not null;type:text"`
	Machine       string `gorm:"column:machine;not null;type:text;default:''"`
	Host          string `gorm:"column:host;not null;type:text;default:''"`
	Pid           int64  `gorm:"column:pid;not null;type:integer;default:0"`
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
	ID              string `gorm:"column:id;primaryKey;not null;type:text"`
	ParentSessionID string `gorm:"column:parent_id;not null;type:text"`
	ChildID         string `gorm:"column:child_id;not null;type:text;uniqueIndex:idx_budget_reservations_child_id"`
	Granted         int64  `gorm:"column:granted;not null;type:integer"`
	Consumed        int64  `gorm:"column:consumed;not null;default:0;type:integer"`
	Status          string `gorm:"column:status;not null;type:text"`
	CreatedAtUTC    string `gorm:"column:created_at_utc;not null;type:text"`

	// Parent declares the parent_id -> sessions.id foreign key. The Go
	// field is ParentSessionID, not ParentID: GORM resolves the relation
	// by field name and would otherwise read sessions.parent_id as the
	// key of a has-one relation in the opposite direction.
	// ReserveBudget loads the parent session in the same transaction.
	// child_id stays unconstrained: a reservation names a child id the
	// caller may not have created yet and ReserveBudget never checks it.
	Parent *sessionRow `gorm:"foreignKey:ParentSessionID;references:ID"`
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

	// Session declares the session_id -> sessions.id foreign key:
	// SendMessage resolves the delegation chain of the addressee first.
	// sender stays unconstrained: it is checked against that chain.
	Session *sessionRow `gorm:"foreignKey:SessionID;references:ID"`
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

// swarmRunChildRow mirrors the swarm_run_children table: one child
// session per packet a run delegated; run_id is a foreign key to
// swarm_runs (written in the run's transaction). session_id stays
// unconstrained: the run record is a historical receipt and the
// referenced child session may be an id the caller supplied.
type swarmRunChildRow struct {
	ID           string `gorm:"column:id;primaryKey;not null;type:text"`
	RunID        string `gorm:"column:run_id;not null;type:text;index:idx_swarm_run_children_run_id"`
	PacketID     string `gorm:"column:packet_id;not null;type:text"`
	SessionID    string `gorm:"column:session_id;not null;type:text"`
	CreatedAtUTC string `gorm:"column:created_at_utc;not null;type:text"`

	// Run declares the run_id -> swarm_runs.id foreign key.
	Run *swarmRunRow `gorm:"foreignKey:RunID;references:ID"`
}

// swarmRunPacketRow mirrors the swarm_run_packets table (v7): one
// admission decision per packet; SQLite stores the boolean as 1/0
// and run_id is a foreign key to swarm_runs (same transaction).
type swarmRunPacketRow struct {
	ID           string `gorm:"column:id;primaryKey;not null;type:text"`
	RunID        string `gorm:"column:run_id;not null;type:text;index:idx_swarm_run_packets_run_id"`
	PacketID     string `gorm:"column:packet_id;not null;type:text"`
	Admitted     int64  `gorm:"column:admitted;not null;type:integer"`
	Reason       string `gorm:"column:reason;not null;type:text"`
	CreatedAtUTC string `gorm:"column:created_at_utc;not null;type:text"`

	// Run declares the run_id -> swarm_runs.id foreign key.
	Run *swarmRunRow `gorm:"foreignKey:RunID;references:ID"`
}
