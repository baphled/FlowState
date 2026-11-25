package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Copied verbatim (tabs included) from the committed v1–v3 migrations
// so the fixture upgrades a byte-faithful legacy database.
const legacyBootstrapDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version     INTEGER PRIMARY KEY,
		applied_at_utc TEXT NOT NULL
	)`

const legacySessionsDDL = `CREATE TABLE sessions (
				id            TEXT PRIMARY KEY,
				created_at_utc TEXT NOT NULL,
				updated_at_utc TEXT NOT NULL,
				status        TEXT NOT NULL CHECK (status IN ('active','paused','completed','cancelled','failed')),
				provider      TEXT NOT NULL,
				model_id      TEXT NOT NULL,
				harness       TEXT NOT NULL,
				config_sha256 TEXT NOT NULL
			)`

const legacyEventsDDL = `CREATE TABLE events (
				session_id    TEXT NOT NULL REFERENCES sessions(id),
				seq           INTEGER NOT NULL,
				created_at_utc TEXT NOT NULL,
				actor         TEXT NOT NULL,
				type          TEXT NOT NULL,
				payload_sha256 TEXT NOT NULL,
				payload_json  TEXT NOT NULL,
				PRIMARY KEY (session_id, seq)
			) WITHOUT ROWID`

const legacyReceiptsDDL = `CREATE TABLE receipts (
				id            TEXT PRIMARY KEY,
				capability_id TEXT NOT NULL,
				commit_sha    TEXT NOT NULL,
				receipt_ref   TEXT NOT NULL,
				receipt_sha256 TEXT NOT NULL,
				gate_outcome  TEXT NOT NULL CHECK (gate_outcome IN ('pass','fail')),
				recorded_at_utc TEXT NOT NULL
			)`

const legacyEventsIndexDDL = `CREATE INDEX idx_events_session_type ON events(session_id, type)`

const legacySessionsStatusIndexDDL = `CREATE INDEX idx_sessions_status ON sessions(status)`

const legacyLeasesDDL = `CREATE TABLE leases (
				item            TEXT PRIMARY KEY,
				agent           TEXT NOT NULL,
				token           INTEGER NOT NULL,
				acquired_at_utc TEXT NOT NULL
			) WITHOUT ROWID`

const legacyLeaseHistoryDDL = `CREATE TABLE lease_history (
				item     TEXT NOT NULL,
				token    INTEGER NOT NULL,
				PRIMARY KEY (item, token)
			) WITHOUT ROWID`

// legacySessionSnapshot is the raw committed sessions row.
type legacySessionSnapshot struct {
	ID, CreatedAtUTC, UpdatedAtUTC, Status, Provider, ModelID, Harness, ConfigSHA string
}

// legacyEventSnapshot is one raw committed events row.
type legacyEventSnapshot struct {
	SessionID, CreatedAtUTC, Actor, Type, PayloadSHA, PayloadJSON string
	Seq                                                           int64
}

const legacySessionID = "legacy-0193abab-c0ffee-7000-0000-000000000001"

// seedLegacyV3 seeds a committed v3-shape database and snapshots every
// table DDL. Raw SQL is permitted in test files (guard scans
// non-test files only).
func seedLegacyV3() (path string, before map[string]string) {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	path = filepath.Join(GinkgoT().TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	must(err)
	mustExec := func(q string, args ...any) {
		_, err := db.Exec(q, args...)
		must(err)
	}
	mustExec(legacyBootstrapDDL)
	for _, stmt := range []string{legacySessionsDDL, legacyEventsDDL, legacyReceiptsDDL,
		legacyEventsIndexDDL, legacySessionsStatusIndexDDL, legacyLeasesDDL, legacyLeaseHistoryDDL} {
		mustExec(stmt)
	}
	for v := 1; v <= 3; v++ {
		mustExec(`INSERT INTO schema_migrations (version, applied_at_utc) VALUES (?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, v)
	}
	mustExec(`INSERT INTO sessions (id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256)
	VALUES (?, '2026-11-17T10:00:00Z', '2026-11-17T10:05:00Z', 'active', 'zai', 'glm-5.3', 'pi', 'sha-abc')`, legacySessionID)
	for i, payload := range []string{`{"a":1}`, `{"b":2}`} {
		mustExec(`INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		VALUES (?, ?, '2026-11-17T10:01:0' || ? || 'Z', 'user', 'turn.tick', ?, ?)`,
			legacySessionID, i, string(rune('0'+i)), sha256Hex([]byte(payload)), payload)
	}
	before = map[string]string{}
	for _, table := range []string{"schema_migrations", "sessions", "events", "receipts", "leases", "lease_history"} {
		before[table] = tableDDL(db, table)
	}
	must(db.Close())
	return path, before
}

// tableDDL reads the stored CREATE statement from sqlite_master.
func tableDDL(db *sql.DB, table string) string {
	var ddl string
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&ddl)
	if err != nil {
		panic(err)
	}
	return ddl
}

// readLegacySession snapshots the committed sessions row.
func readLegacySession(db *sql.DB) legacySessionSnapshot {
	var r legacySessionSnapshot
	err := db.QueryRow(`SELECT id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256
	FROM sessions WHERE id=?`, legacySessionID).
		Scan(&r.ID, &r.CreatedAtUTC, &r.UpdatedAtUTC, &r.Status, &r.Provider, &r.ModelID, &r.Harness, &r.ConfigSHA)
	if err != nil {
		panic(err)
	}
	return r
}

// readLegacyEvents snapshots both committed events rows in seq order.
func readLegacyEvents(db *sql.DB) []legacyEventSnapshot {
	rows, err := db.Query(`SELECT session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json
	FROM events WHERE session_id=? ORDER BY seq`, legacySessionID)
	if err != nil {
		panic(err)
	}
	defer func() { _ = rows.Close() }()
	var out []legacyEventSnapshot
	for rows.Next() {
		var e legacyEventSnapshot
		if err := rows.Scan(&e.SessionID, &e.Seq, &e.CreatedAtUTC, &e.Actor, &e.Type, &e.PayloadSHA, &e.PayloadJSON); err != nil {
			panic(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	return out
}

// legacyFixture snapshots the seeded legacy database before migration:
// its path, every table DDL, and the committed rows.
type legacyFixture struct {
	path    string
	before  map[string]string
	session legacySessionSnapshot
	events  []legacyEventSnapshot
}

// snapshotLegacy seeds the v3 fixture and captures it pre-migration;
// panics are setup failures, not behavioural expectations.
func snapshotLegacy() legacyFixture {
	path, before := seedLegacyV3()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		panic(err)
	}
	f := legacyFixture{path: path, before: before,
		session: readLegacySession(db), events: readLegacyEvents(db)}
	if err := db.Close(); err != nil {
		panic(err)
	}
	return f
}

var _ = Describe("Legacy v3 database upgrade", func() {
	var (
		f     legacyFixture
		store *Store
	)

	BeforeEach(func() {
		f = snapshotLegacy()
		s, err := Open(f.path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		store = s
	})

	It("records schema version 10 after the first migrated open", func() {
		v, err := store.SchemaVersion()
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(Equal(int64(10)))
	})

	It("takes the backup before any schema change", func() {
		db, err := sql.Open("sqlite", "file:"+f.path+".pre-v9.bak")
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = db.Close() })
		Expect(tableDDL(db, "sessions")).To(Equal(f.before["sessions"]))
	})

	It("appends versions 4 through 10 to the recorded schema_migrations", func() {
		// version is INTEGER PRIMARY KEY, so count 10 with max 10 pins {1..10}.
		var n, maxV int64
		err := store.db.QueryRow(`SELECT count(*), max(version) FROM schema_migrations`).Scan(&n, &maxV)
		Expect(err).NotTo(HaveOccurred())
		Expect([2]int64{n, maxV}).To(Equal([2]int64{10, 10}))
	})

	It("creates the swarm_run_children table on a migrated legacy database", func() {
		var n int
		err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='swarm_run_children'`).Scan(&n)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
	})

	It("preserves the committed session row field-for-field", func() {
		Expect(readLegacySession(store.db)).To(Equal(f.session))
	})

	It("serves the legacy session through GetSession with identical fields", func() {
		sess, err := store.GetSession(context.Background(), legacySessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{sess.ID, sess.Status, sess.Provider, sess.ModelID, sess.Harness, sess.ConfigSHA, sess.ParentID,
			sess.CreatedAt.UTC().Format(time.RFC3339Nano), sess.UpdatedAt.UTC().Format(time.RFC3339Nano)}).
			To(Equal([]string{f.session.ID, f.session.Status, f.session.Provider, f.session.ModelID,
				f.session.Harness, f.session.ConfigSHA, "", f.session.CreatedAtUTC, f.session.UpdatedAtUTC}))
	})

	It("preserves the committed event rows verbatim", func() {
		Expect(readLegacyEvents(store.db)).To(Equal(f.events))
	})

	It("replays both committed events with unchanged payload digests", func() {
		replayed, err := store.Replay(context.Background(), legacySessionID)
		Expect(err).NotTo(HaveOccurred())
		digests := make([]string, len(replayed))
		for i, e := range replayed {
			digests[i] = e.PayloadSHA
		}
		Expect(digests).To(Equal([]string{f.events[0].PayloadSHA, f.events[1].PayloadSHA}))
	})

	It("adds a nullable parent_id column", func() {
		var notnull int
		err := store.db.QueryRow(
			`SELECT "notnull" FROM pragma_table_info('sessions') WHERE name='parent_id'`).Scan(&notnull)
		Expect(err).NotTo(HaveOccurred())
		Expect(notnull).To(BeZero(), "sessions.parent_id column missing or made NOT NULL")
	})

	It("holds NULL parent_id for the existing session row", func() {
		var parentID any
		err := store.db.QueryRow(`SELECT parent_id FROM sessions WHERE id=?`, legacySessionID).Scan(&parentID)
		Expect(err).NotTo(HaveOccurred())
		Expect(parentID).To(BeNil())
	})

	It("keeps every legacy sessions column and adds parent_id and budget_pool", func() {
		var names string
		err := store.db.QueryRow(`SELECT group_concat(name) FROM pragma_table_info('sessions')`).Scan(&names)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal("id,created_at_utc,updated_at_utc,status,provider,model_id,harness,config_sha256," +
			"parent_id,budget_pool"))
	})

	// The v9 migration cannot tell an unnamed legacy key from none, so it rebuilds events with the named
	// key the model declares; the row layout stays WITHOUT ROWID.
	It("keeps events WITHOUT ROWID", func() {
		Expect(tableDDL(store.db, "events")).To(ContainSubstring("WITHOUT ROWID"))
	})

	It("keeps exactly one foreign key on events", func() {
		var n int
		err := store.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('events')`).Scan(&n)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
	})

	It("gives the rebuilt sessions table its parent_id key", func() {
		var n int
		err := store.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('sessions')`).Scan(&n)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
	})

	It("declares the session foreign key on the session_messages table created by the upgrade", func() {
		var n int
		err := store.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('session_messages')`).Scan(&n)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
	})

	It("keeps the leases table WITHOUT ROWID after it gains its v10 columns", func() {
		Expect(tableDDL(store.db, "leases")).To(HaveSuffix("WITHOUT ROWID"))
	})

	It("adds the v10 lease columns to the legacy leases table", func() {
		var n int
		err := store.db.QueryRow(`SELECT count(*) FROM pragma_table_info('leases') WHERE name IN ('machine','host','pid')`).Scan(&n)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
	})

	It("keeps the lease_history DDL untouched and WITHOUT ROWID", func() {
		Expect(tableDDL(store.db, "lease_history")).To(Equal(f.before["lease_history"]))
	})

	It("keeps the receipts DDL untouched (no named-constraint rebuild)", func() {
		Expect(tableDDL(store.db, "receipts")).To(Equal(f.before["receipts"]))
	})

	It("creates the v5 through v7 tables on the legacy database", func() {
		var n int
		err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('budget_reservations','session_messages','swarm_runs','swarm_run_packets')`).Scan(&n)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(4))
	})
})
