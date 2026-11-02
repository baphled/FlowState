package storage_test

import (
	"context"
	"database/sql"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/storage"
)

func receiptRow() storage.Receipt {
	return storage.Receipt{
		ID: "2026-11-02-m1-dogfood", CapabilityID: "M1-DOGFOOD",
		CommitSHA:     "1cc6d53b00000000000000000000000000000000",
		ReceiptRef:    "/tmp/receipt.json",
		ReceiptSHA256: digest64("r"), GateOutcome: "pass",
	}
}

// receiptDB opens a migrated throwaway database as fixture setup.
func receiptDB() *sql.DB {
	root := GinkgoT().TempDir()
	db, _, err := storage.Open(root + "/test.db")
	Expect(err).NotTo(HaveOccurred())
	err = storage.Migrate(db)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = db.Close() })
	return db
}

// insertReceipt indexes a receipt as fixture setup.
func insertReceipt(ctx context.Context, db *sql.DB, r storage.Receipt) {
	err := storage.InsertReceipt(ctx, db, r)
	Expect(err).NotTo(HaveOccurred())
}

// gateOutcome reads the indexed receipts-table row as fixture setup.
func gateOutcome(ctx context.Context, db *sql.DB, id string) string {
	var outcome string
	err := db.QueryRowContext(ctx,
		`SELECT gate_outcome FROM receipts WHERE id=?`, id).Scan(&outcome)
	Expect(err).NotTo(HaveOccurred())
	return outcome
}

var _ = Describe("InsertReceipt", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("inserts a receipts-table index row after successful readback", func() {
		db := receiptDB()

		insertReceipt(ctx, db, receiptRow())

		Expect(gateOutcome(ctx, db, receiptRow().ID)).To(Equal("pass"))
	})

	It("rejects a gate outcome outside the schema enum", func() {
		db := receiptDB()

		err := storage.InsertReceipt(ctx, db, storage.Receipt{ID: "x", GateOutcome: "maybe"})
		Expect(err).To(MatchError(ContainSubstring("gate_outcome")))
	})
})

func digest64(s string) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = s[i%len(s)]
	}
	return string(out)
}
