package metadata_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/metadata"
	"github.com/baphled/flowstate/internal/storage"
)

func TestMetadata(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metadata Suite")
}

// withRoot points FLOWSTATE_METADATA_ROOT at a throwaway temp dir;
// tests never touch the real default Dropbox path.
func withRoot() string {
	root := GinkgoT().TempDir()
	err := os.Setenv("FLOWSTATE_METADATA_ROOT", root)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.Unsetenv("FLOWSTATE_METADATA_ROOT") })
	return root
}

func mustDB() *sql.DB {
	root := GinkgoT().TempDir()
	db, _, err := storage.Open(root + "/test.db")
	Expect(err).NotTo(HaveOccurred())
	err = storage.Migrate(db)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = db.Close() })
	return db
}

// emit runs an emission as fixture setup, failing the spec if it errors.
func emit(ctx context.Context, db *sql.DB, a metadata.Attempt) *metadata.AttemptRecord {
	rec, err := metadata.Emit(ctx, db, a)
	Expect(err).NotTo(HaveOccurred())
	return rec
}

// receiptDoc reads and decodes the emitted receipt document as fixture setup.
func receiptDoc(rec *metadata.AttemptRecord) map[string]any {
	raw, err := os.ReadFile(filepath.Clean(rec.ReceiptPath))
	Expect(err).NotTo(HaveOccurred())
	var doc map[string]any
	err = json.Unmarshal(raw, &doc)
	Expect(err).NotTo(HaveOccurred())
	return doc
}

// gateOutcome reads the indexed receipts-table row as fixture setup.
func gateOutcome(ctx context.Context, db *sql.DB, id string) string {
	var outcome string
	err := db.QueryRowContext(ctx,
		`SELECT gate_outcome FROM receipts WHERE id=?`, id).Scan(&outcome)
	Expect(err).NotTo(HaveOccurred())
	return outcome
}

func sampleAttempt() metadata.Attempt {
	return metadata.Attempt{
		ID: "2026-11-02-m1-selftest", CapabilityID: "M1-SELFTEST",
		CommitSHA:   "1cc6d53b00000000000000000000000000000000",
		GateOutcome: "pass",
		Attribution: metadata.Attribution{
			Provider: "anthropic", ModelID: "observed-model-x",
			Harness: "flowstate-cli", AuthorIdentity: "baphled",
			Source: "runtime",
		},
		Artefacts: []metadata.Artefact{{Name: "attempt-record.json", Body: []byte(`{}`)}},
	}
}

var _ = Describe("Emit", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("writes the attempt dir under the metadata root", func() {
		root := withRoot()
		db := mustDB()

		rec := emit(ctx, db, sampleAttempt())

		Expect(rec.AttemptDir).To(Equal(filepath.Join(root, "attempts", "2026-11-02-m1-selftest")))
	})

	It("records a sha-256 digest for the emitted receipt", func() {
		withRoot()
		db := mustDB()

		rec := emit(ctx, db, sampleAttempt())

		Expect(rec.ReceiptSHA256).To(HaveLen(64))
	})

	It("records the capability id in the receipt document", func() {
		withRoot()
		db := mustDB()

		rec := emit(ctx, db, sampleAttempt())

		Expect(receiptDoc(rec)["capability_id"]).To(Equal("M1-SELFTEST"))
	})

	It("indexes the gate outcome in the receipts table", func() {
		withRoot()
		db := mustDB()

		emit(ctx, db, sampleAttempt())

		Expect(gateOutcome(ctx, db, "2026-11-02-m1-selftest")).To(Equal("pass"))
	})

	It("rejects rewriting an existing attempt dir as immutable", func() {
		withRoot()
		db := mustDB()

		emit(ctx, db, sampleAttempt())
		_, err := metadata.Emit(ctx, db, sampleAttempt())
		Expect(err).To(MatchError(metadata.ErrAttemptExists))
	})

	It("rejects attribution that is not runtime-observed", func() {
		withRoot()
		db := mustDB()

		a := sampleAttempt()
		a.Attribution.ModelID = ""
		_, err := metadata.Emit(ctx, db, a)
		Expect(err).To(MatchError(metadata.ErrUsage))
	})
})
