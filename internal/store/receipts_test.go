package store

import (
	"context"
	"database/sql"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// legacyReceiptRow is the shape the retired storage package wrote.
func legacyReceiptRow() Receipt {
	return Receipt{
		ID: "2026-11-02-m1-selftest", CapabilityID: "M1-SELFTEST",
		CommitSHA:     "1cc6d53b00000000000000000000000000000000",
		ReceiptRef:    "/dropbox/receipt.json",
		ReceiptSHA256: sha256Hex([]byte("receipt")), GateOutcome: "pass",
		RecordedAtUTC: "2026-11-02T10:00:00Z",
	}
}

// mustLegacyReceiptsDB opens a database holding the receipts table
// exactly as the retired storage migrations created it (v1 DDL with
// its gate_outcome CHECK, plus the v2 receipt_ref column).
func mustLegacyReceiptsDB() *sql.DB {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(GinkgoT().TempDir(), "r.db"))
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE receipts (
  id TEXT PRIMARY KEY, capability_id TEXT NOT NULL, commit_sha TEXT NOT NULL,
  receipt_sha256 TEXT NOT NULL,
  gate_outcome TEXT NOT NULL CHECK (gate_outcome IN ('pass','fail')),
  recorded_at_utc TEXT NOT NULL);
ALTER TABLE receipts ADD COLUMN receipt_ref TEXT NOT NULL DEFAULT ''`); err != nil {
		panic(err)
	}
	return db
}

// mustReceiptLog binds a log to db; panics are setup failures.
func mustReceiptLog(db *sql.DB) *ReceiptLog {
	log, err := NewReceiptLog(db)
	if err != nil {
		panic(err)
	}
	return log
}

// receiptColumn reads one column of the receipt row as fixture
// inspection; panics are setup failures.
func receiptColumn(db *sql.DB, column, id string) string {
	var v string
	row := db.QueryRow(`SELECT `+column+` FROM receipts WHERE id = ?`, id)
	if err := row.Scan(&v); err != nil {
		panic(err)
	}
	return v
}

// receiptCount counts stored receipt rows.
func receiptCount(db *sql.DB) int {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM receipts`).Scan(&n); err != nil {
		panic(err)
	}
	return n
}

var _ = Describe("ReceiptLog.Insert", func() {
	var (
		ctx context.Context
		db  *sql.DB
		log *ReceiptLog
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = mustLegacyReceiptsDB()
		log = mustReceiptLog(db)
	})

	DescribeTable("writes every column into the legacy receipts table unchanged",
		func(column, want string) {
			if err := log.Insert(ctx, legacyReceiptRow()); err != nil {
				panic(err)
			}

			Expect(receiptColumn(db, column, legacyReceiptRow().ID)).To(Equal(want))
		},
		Entry("capability_id", "capability_id", "M1-SELFTEST"),
		Entry("commit_sha", "commit_sha", "1cc6d53b00000000000000000000000000000000"),
		Entry("receipt_ref", "receipt_ref", "/dropbox/receipt.json"),
		Entry("receipt_sha256", "receipt_sha256", sha256Hex([]byte("receipt"))),
		Entry("gate_outcome", "gate_outcome", "pass"),
		Entry("recorded_at_utc", "recorded_at_utc", "2026-11-02T10:00:00Z"),
	)

	It("stamps an RFC 3339 UTC time when none is supplied", func() {
		r := legacyReceiptRow()
		r.RecordedAtUTC = ""
		if err := log.Insert(ctx, r); err != nil {
			panic(err)
		}

		Expect(receiptColumn(db, "recorded_at_utc", r.ID)).To(MatchRegexp(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`))
	})

	It("rejects a gate outcome outside pass and fail", func() {
		r := legacyReceiptRow()
		r.GateOutcome = "maybe"

		Expect(log.Insert(ctx, r)).To(MatchError(ErrReceiptOutcomeInvalid))
	})

	It("writes nothing for a rejected gate outcome", func() {
		r := legacyReceiptRow()
		r.GateOutcome = "maybe"
		_ = log.Insert(ctx, r)

		Expect(receiptCount(db)).To(BeZero())
	})

	It("rejects an empty receipt reference", func() {
		r := legacyReceiptRow()
		r.ReceiptRef = ""

		Expect(log.Insert(ctx, r)).To(MatchError(ErrReceiptRefMissing))
	})

	It("reports a duplicate receipt id as an insert failure", func() {
		if err := log.Insert(ctx, legacyReceiptRow()); err != nil {
			panic(err)
		}

		Expect(log.Insert(ctx, legacyReceiptRow())).To(MatchError(ContainSubstring("insert receipt")))
	})
})
