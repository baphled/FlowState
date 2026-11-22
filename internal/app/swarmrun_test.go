package app_test

import (
	"context"
	"database/sql"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"
)

// receiptRow reads the first row of query from the receipts database at
// dbPath as strings; it panics on any setup failure.
func receiptRow(dbPath, query string, cols int) []string {
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		panic(err)
	}
	defer func() { _ = db.Close() }()
	out := make([]string, cols)
	dest := make([]any, cols)
	for i := range out {
		dest[i] = &out[i]
	}
	if err = db.QueryRow(query).Scan(dest...); err != nil {
		panic(err)
	}
	return out
}

var _ = Describe("SwarmRun", func() {
	var manifest swarm.Manifest

	BeforeEach(func() {
		manifest = swarm.Manifest{
			Name:  "demo.swarm",
			Scope: "demo.swarm",
			Packets: []swarm.Packet{{
				ID:           "p1",
				Scope:        "demo.swarm.plan",
				Task:         "plan the work",
				SourceSHA256: swarm.SHA256HexForTest([]byte("plan the work")),
			}},
			Gates: []swarm.Gate{{Name: "go", Description: "go", Command: "true"}},
		}
	})

	It("runs a manifest end to end with persistent records", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		res, runErr := app.SwarmRunManifest(context.Background(), dbPath, manifest)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Admitted).To(Equal([]string{"p1"}))
	})

	It("stores the admission row with its packet and outcome", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		if _, err := app.SwarmRunManifest(context.Background(), dbPath, manifest); err != nil {
			panic(err)
		}
		row := receiptRow(dbPath, "SELECT run_id, packet_id, outcome, reason FROM swarm_admissions", 4)
		Expect(row).To(Equal([]string{"demo.swarm", "p1", "admit", ""}))
	})

	It("stores the gate row with its name and outcome", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		if _, err := app.SwarmRunManifest(context.Background(), dbPath, manifest); err != nil {
			panic(err)
		}
		row := receiptRow(dbPath, "SELECT run_id, gate_name, outcome FROM swarm_gate_receipts", 3)
		Expect(row).To(Equal([]string{"demo.swarm", "go", "pass"}))
	})

	It("stores a failed gate as outcome fail", func() {
		bad := manifest
		bad.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		_, _ = app.SwarmRunManifest(context.Background(), dbPath, bad)
		row := receiptRow(dbPath, "SELECT gate_name, outcome FROM swarm_gate_receipts", 2)
		Expect(row).To(Equal([]string{"broken", "fail"}))
	})

	It("accepts only when every gate passes", func() {
		bad := manifest
		bad.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		_, runErr := app.SwarmRunManifest(context.Background(), dbPath, bad)
		Expect(runErr).To(MatchError(app.ErrSwarmNotAccepted))
	})

	It("names the failed gate in the not-accepted error", func() {
		bad := manifest
		bad.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		_, runErr := app.SwarmRunManifest(context.Background(), dbPath, bad)
		Expect(runErr).To(MatchError(ContainSubstring("failed gates [broken]")))
	})

	It("classes a failed gate as receipt_invalid", func() {
		bad := manifest
		bad.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		_, runErr := app.SwarmRunManifest(context.Background(), dbPath, bad)
		Expect(app.ErrorCode(runErr)).To(Equal(app.CodeReceiptInvalid))
	})

	It("reports a rejected packet as not accepted", func() {
		bad := manifest
		bad.Packets = []swarm.Packet{{ID: "p1", Scope: "demo.swarm.plan", Task: "plan the work",
			SourceSHA256: swarm.SHA256HexForTest([]byte("different body"))}}
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		_, runErr := app.SwarmRunManifest(context.Background(), dbPath, bad)
		Expect(runErr).To(MatchError(app.ErrSwarmNotAccepted))
	})

	Context("when the caller's context is cancelled", func() {
		var runErr error

		BeforeEach(func() {
			cctx, cancel := context.WithCancel(context.Background())
			cancel()
			dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
			_, runErr = app.SwarmRunManifest(cctx, dbPath, manifest)
		})

		It("stops the run with an error", func() {
			Expect(runErr).To(HaveOccurred())
		})

		It("maps the cancellation onto the interrupt error", func() {
			Expect(runErr).To(MatchError(app.ErrSessionCancelled))
		})

		It("keeps the interrupt class rather than receipt_invalid", func() {
			Expect(app.ErrorCode(runErr)).To(Equal(app.CodeSessionCancelled))
		})

		It("keeps the cancellation cause", func() {
			Expect(runErr).To(MatchError(context.Canceled))
		})
	})
})
