package store

import (
	"context"
	"database/sql"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// legacySwarmReceiptDDL must match the DDL the legacy
// internal/storage.MigrateSwarmTables produces (CHECK constraints
// included) until that package is deleted.
const legacySwarmReceiptDDL = "CREATE TABLE `swarm_admissions` (`run_id` TEXT NOT NULL,`packet_id` TEXT NOT NULL," +
	"`outcome` TEXT NOT NULL,`reason` TEXT NOT NULL,`recorded_at_utc` TEXT NOT NULL," +
	"CONSTRAINT `chk_swarm_admission_outcome` CHECK (outcome IN ('admit','reject')));" +
	"CREATE TABLE `swarm_gate_receipts` (`run_id` TEXT NOT NULL,`gate_name` TEXT NOT NULL," +
	"`outcome` TEXT NOT NULL,`recorded_at_utc` TEXT NOT NULL," +
	"CONSTRAINT `chk_swarm_gate_outcome` CHECK (outcome IN ('pass','fail')));" +
	"CREATE TABLE `swarm_member_receipts` (`run_name` TEXT NOT NULL,`packet_id` TEXT NOT NULL," +
	"`gate_outcome` TEXT NOT NULL,`evidence_sha256` TEXT NOT NULL,`recorded_at_utc` TEXT NOT NULL," +
	"CONSTRAINT `chk_swarm_member_outcome` CHECK (gate_outcome IN ('pass','fail')))"

// mustSwarmReceiptDB opens a fresh database; legacy true creates the
// tables with the legacy DDL. Panics are setup failures.
func mustSwarmReceiptDB(legacy bool) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(GinkgoT().TempDir(), "s.db"))
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = db.Close() })
	if legacy {
		if _, err := db.Exec(legacySwarmReceiptDDL); err != nil {
			panic(err)
		}
	}
	return db
}

// mustSwarmReceiptLog binds a log to db; panics are setup failures.
func mustSwarmReceiptLog(db *sql.DB) *SwarmReceiptLog {
	log, err := NewSwarmReceiptLog(db)
	if err != nil {
		panic(err)
	}
	return log
}

// swarmReceiptColumn reads one column of the only row of table as
// fixture inspection; panics are setup failures.
func swarmReceiptColumn(db *sql.DB, table, column string) string {
	var v string
	if err := db.QueryRow(`SELECT ` + column + ` FROM ` + table).Scan(&v); err != nil {
		panic(err)
	}
	return v
}

// swarmReceiptCount counts the rows of table.
func swarmReceiptCount(db *sql.DB, table string) int {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		panic(err)
	}
	return n
}

// swarmReceiptDDL returns the stored CREATE statement of table.
func swarmReceiptDDL(db *sql.DB, table string) string {
	var v string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = ?`, table).Scan(&v); err != nil {
		panic(err)
	}
	return v
}

var _ = Describe("SwarmReceiptLog", func() {
	var (
		ctx context.Context
		db  *sql.DB
		log *SwarmReceiptLog
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = mustSwarmReceiptDB(true)
		log = mustSwarmReceiptLog(db)
	})

	DescribeTable("RecordAdmission writes every column into the legacy table",
		func(column, want string) {
			if err := log.RecordAdmission(ctx, "run-1", "p1", false, "no scope"); err != nil {
				panic(err)
			}

			Expect(swarmReceiptColumn(db, "swarm_admissions", column)).To(Equal(want))
		},
		Entry("run_id", "run_id", "run-1"),
		Entry("packet_id", "packet_id", "p1"),
		Entry("outcome", "outcome", "reject"),
		Entry("reason", "reason", "no scope"),
	)

	It("stores an admitted packet as admit", func() {
		if err := log.RecordAdmission(ctx, "run-1", "p1", true, ""); err != nil {
			panic(err)
		}

		Expect(swarmReceiptColumn(db, "swarm_admissions", "outcome")).To(Equal("admit"))
	})

	It("stamps admissions in the sortable UTC format", func() {
		if err := log.RecordAdmission(ctx, "run-1", "p1", true, ""); err != nil {
			panic(err)
		}

		Expect(swarmReceiptColumn(db, "swarm_admissions", "recorded_at_utc")).
			To(MatchRegexp(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`))
	})

	It("does not persist an admission once the context is cancelled", func() {
		cctx, cancel := context.WithCancel(ctx)
		cancel()

		Expect(log.RecordAdmission(cctx, "run-1", "p1", true, "")).To(MatchError(context.Canceled))
	})

	DescribeTable("RecordGates writes every column into the legacy table",
		func(column, want string) {
			if err := log.RecordGates(ctx, "run-1", []GateResult{{Name: "lint", Passed: true}}); err != nil {
				panic(err)
			}

			Expect(swarmReceiptColumn(db, "swarm_gate_receipts", column)).To(Equal(want))
		},
		Entry("run_id", "run_id", "run-1"),
		Entry("gate_name", "gate_name", "lint"),
		Entry("outcome", "outcome", "pass"),
	)

	It("stores a failing gate as fail", func() {
		if err := log.RecordGates(ctx, "run-1", []GateResult{{Name: "lint"}}); err != nil {
			panic(err)
		}

		Expect(swarmReceiptColumn(db, "swarm_gate_receipts", "outcome")).To(Equal("fail"))
	})

	Context("when a later gate of the run cannot be written", func() {
		var err error

		BeforeEach(func() {
			if _, e := db.Exec(`CREATE TRIGGER fail_gate BEFORE INSERT ON swarm_gate_receipts
				WHEN NEW.gate_name = 'bad' BEGIN SELECT RAISE(ABORT, 'forced gate failure'); END`); e != nil {
				panic(e)
			}
			err = log.RecordGates(ctx, "run-1", []GateResult{{Name: "a", Passed: true}, {Name: "b"}, {Name: "bad"}, {Name: "d"}})
		})

		It("reports the failure", func() {
			Expect(err).To(HaveOccurred())
		})

		It("writes none of the run's gates, including the ones before the failure", func() {
			Expect(swarmReceiptCount(db, "swarm_gate_receipts")).To(BeZero())
		})
	})

	It("stamps gate receipts in the sortable UTC format", func() {
		if err := log.RecordGates(ctx, "run-1", []GateResult{{Name: "lint", Passed: true}}); err != nil {
			panic(err)
		}

		Expect(swarmReceiptColumn(db, "swarm_gate_receipts", "recorded_at_utc")).
			To(MatchRegexp(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`))
	})

	It("records one row per gate", func() {
		gates := []GateResult{{Name: "lint", Passed: true}, {Name: "test"}}
		if err := log.RecordGates(ctx, "run-1", gates); err != nil {
			panic(err)
		}

		Expect(swarmReceiptCount(db, "swarm_gate_receipts")).To(Equal(2))
	})

	It("does not persist gate receipts once the context is cancelled", func() {
		cctx, cancel := context.WithCancel(ctx)
		cancel()

		Expect(log.RecordGates(cctx, "run-1", []GateResult{{Name: "lint"}})).To(MatchError(context.Canceled))
	})

	DescribeTable("RecordMemberReceipt writes every column into the legacy table",
		func(column, want string) {
			rec := MemberReceiptRecord{
				RunName: "run-1", PacketID: "p1", GateOutcome: "pass", EvidenceSHA256: "known-digest",
			}
			if err := log.RecordMemberReceipt(ctx, rec); err != nil {
				panic(err)
			}

			Expect(swarmReceiptColumn(db, "swarm_member_receipts", column)).To(Equal(want))
		},
		Entry("run_name", "run_name", "run-1"),
		Entry("packet_id", "packet_id", "p1"),
		Entry("gate_outcome", "gate_outcome", "pass"),
		Entry("evidence_sha256", "evidence_sha256", "known-digest"),
	)

	It("stamps member receipts in the sortable UTC format", func() {
		rec := MemberReceiptRecord{RunName: "run-1", PacketID: "p1", GateOutcome: "fail"}
		if err := log.RecordMemberReceipt(ctx, rec); err != nil {
			panic(err)
		}

		Expect(swarmReceiptColumn(db, "swarm_member_receipts", "recorded_at_utc")).
			To(MatchRegexp(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`))
	})

	It("rejects a member receipt outcome outside pass and fail", func() {
		rec := MemberReceiptRecord{RunName: "run-1", PacketID: "p1", GateOutcome: "neither"}

		Expect(log.RecordMemberReceipt(ctx, rec)).To(MatchError(ErrSwarmGateOutcomeInvalid))
	})

	It("writes nothing for a rejected member receipt, whatever the table's own constraints", func() {
		// On a database the store migrated there is no CHECK to fall back
		// on: only the Go validation can keep the row out.
		fresh := mustSwarmReceiptDB(false)
		if err := MigrateSwarmReceipts(fresh); err != nil {
			panic(err)
		}
		log = mustSwarmReceiptLog(fresh)
		db = fresh
		_ = log.RecordMemberReceipt(ctx, MemberReceiptRecord{RunName: "run-1", GateOutcome: "neither"})

		Expect(swarmReceiptCount(db, "swarm_member_receipts")).To(BeZero())
	})
})

var _ = Describe("MigrateSwarmReceipts", func() {
	var db *sql.DB

	BeforeEach(func() {
		db = mustSwarmReceiptDB(false)
	})

	DescribeTable("creates a missing table",
		func(table string) {
			if err := MigrateSwarmReceipts(db); err != nil {
				panic(err)
			}

			Expect(swarmReceiptDDL(db, table)).To(ContainSubstring("CREATE TABLE"))
		},
		Entry("swarm_admissions", "swarm_admissions"),
		Entry("swarm_gate_receipts", "swarm_gate_receipts"),
		Entry("swarm_member_receipts", "swarm_member_receipts"),
	)

	DescribeTable("creates every column of a missing table",
		func(table, column string) {
			if err := MigrateSwarmReceipts(db); err != nil {
				panic(err)
			}
			var n int
			err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
			if err != nil {
				panic(err)
			}
			Expect(n).To(Equal(1))
		},
		Entry("swarm_admissions run_id", "swarm_admissions", "run_id"),
		Entry("swarm_admissions packet_id", "swarm_admissions", "packet_id"),
		Entry("swarm_admissions outcome", "swarm_admissions", "outcome"),
		Entry("swarm_admissions reason", "swarm_admissions", "reason"),
		Entry("swarm_admissions recorded_at_utc", "swarm_admissions", "recorded_at_utc"),
		Entry("swarm_gate_receipts run_id", "swarm_gate_receipts", "run_id"),
		Entry("swarm_gate_receipts gate_name", "swarm_gate_receipts", "gate_name"),
		Entry("swarm_gate_receipts outcome", "swarm_gate_receipts", "outcome"),
		Entry("swarm_gate_receipts recorded_at_utc", "swarm_gate_receipts", "recorded_at_utc"),
		Entry("swarm_member_receipts run_name", "swarm_member_receipts", "run_name"),
		Entry("swarm_member_receipts packet_id", "swarm_member_receipts", "packet_id"),
		Entry("swarm_member_receipts gate_outcome", "swarm_member_receipts", "gate_outcome"),
		Entry("swarm_member_receipts evidence_sha256", "swarm_member_receipts", "evidence_sha256"),
		Entry("swarm_member_receipts recorded_at_utc", "swarm_member_receipts", "recorded_at_utc"),
	)

	It("creates tables a log can write into", func() {
		if err := MigrateSwarmReceipts(db); err != nil {
			panic(err)
		}

		Expect(mustSwarmReceiptLog(db).RecordAdmission(context.Background(), "r", "p", true, "")).To(Succeed())
	})

	It("succeeds when applied twice", func() {
		if err := MigrateSwarmReceipts(db); err != nil {
			panic(err)
		}

		Expect(MigrateSwarmReceipts(db)).To(Succeed())
	})

	It("leaves a legacy table and its rows untouched", func() {
		legacy := mustSwarmReceiptDB(true)
		if _, err := legacy.Exec("INSERT INTO swarm_admissions VALUES ('r','p','admit','','t')"); err != nil {
			panic(err)
		}
		if err := MigrateSwarmReceipts(legacy); err != nil {
			panic(err)
		}

		Expect(swarmReceiptCount(legacy, "swarm_admissions")).To(Equal(1))
	})

	It("does not rewrite a legacy table definition", func() {
		legacy := mustSwarmReceiptDB(true)
		before := swarmReceiptDDL(legacy, "swarm_gate_receipts")
		if err := MigrateSwarmReceipts(legacy); err != nil {
			panic(err)
		}

		Expect(swarmReceiptDDL(legacy, "swarm_gate_receipts")).To(Equal(before))
	})
})
