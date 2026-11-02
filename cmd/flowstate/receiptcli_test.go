package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// validReceipt builds a schema-valid M1 acceptance receipt.
func validReceipt() map[string]any {
	return map[string]any{
		"schema_version": "m1-acceptance-receipt-v2",
		"capability_id":  "M1-EVENTS",
		"repo_state": map[string]any{
			"commit_sha":      "0123456789abcdef0123456789abcdef01234567",
			"dirty":           false,
			"manifest_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"job_id":          "job-1",
		},
		"author_identity": "author",
		"evidence": []any{map[string]any{
			"command":            "make test",
			"expected_exit_code": 0,
			"exit_code":          0,
			"stdout":             map[string]any{"path": "out.txt", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			"stderr":             map[string]any{"path": "err.txt", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			"captured_at_utc":    "2026-11-02T00:00:00Z",
		}},
		"gate_outcome": "pass",
		"observed_model": map[string]any{
			"provider": "none", "model_id": "none", "harness": "none", "source": "test",
		},
		"review": map[string]any{
			"reviewer_identity": "reviewer",
			"evidence_ref":      "dropbox:/review",
			"candidate_sha":     "0123456789abcdef0123456789abcdef01234567",
			"verdict":           "accept",
		},
		"validator_run": map[string]any{
			"exit_code":                 0,
			"docs_pack_manifest_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"artifact":                  map[string]any{"path": "art.json", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		},
		"metadata_ref":  "dropbox:/FlowState/Metadata/attempts/x/receipt.json",
		"artifact_root": "dropbox:/FlowState/Metadata/attempts/x",
	}
}

// writeReceipt marshals doc to a temp file and returns its path; a
// write failure panics because the spec cannot proceed without its
// fixture.
func writeReceipt(doc map[string]any) string {
	p := filepath.Join(GinkgoT().TempDir(), "receipt.json")
	b, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		panic(err)
	}
	return p
}

var _ = Describe("receipt validate", func() {
	It("exits 0 on a valid receipt", func() {
		p := writeReceipt(validReceipt())
		res := runArgv([]string{"receipt", "validate", p})
		Expect(res.exit).To(Equal(0))
	})

	It("exits 8 on a tampered receipt digest", func() {
		doc := validReceipt()
		doc["repo_state"].(map[string]any)["manifest_sha256"] = "tampered"
		p := writeReceipt(doc)
		res := runArgv([]string{"receipt", "validate", p})
		Expect(res.exit).To(Equal(8))
	})

	It("exits 8 on a pass gate with a rejecting review", func() {
		doc := validReceipt()
		doc["review"].(map[string]any)["verdict"] = "reject"
		p := writeReceipt(doc)
		res := runArgv([]string{"receipt", "validate", p})
		Expect(res.exit).To(Equal(8))
	})

	It("exits 8 on a missing evidence array", func() {
		doc := validReceipt()
		delete(doc, "evidence")
		p := writeReceipt(doc)
		res := runArgv([]string{"receipt", "validate", p})
		Expect(res.exit).To(Equal(8))
	})

	It("exits 2 with no receipt paths", func() {
		Expect(runArgv([]string{"receipt", "validate"}).exit).To(Equal(2))
	})
})

var _ = Describe("replay", func() {
	var db string

	BeforeEach(func() {
		db = filepath.Join(GinkgoT().TempDir(), "test.db")
	})

	It("exits 2 without a session id", func() {
		Expect(runArgv([]string{"replay", "--db", db}).exit).To(Equal(2))
	})

	It("exits 1 on an unknown session", func() {
		Expect(runArgv([]string{"replay", "--db", db, "nope"}).exit).To(Equal(1))
	})

	It("exits 0 and prints the deterministic transcript", func() {
		res := runArgv([]string{"session", "new", "--db", db,
			"--provider", "zai", "--model", "glm-4.6", "--task", "hi"})
		if res.exit != 0 {
			panic(res.stderr)
		}
		var rec newRecord
		if err := json.Unmarshal([]byte(firstLine(res.stdout)), &rec); err != nil {
			panic(err)
		}
		out := runArgv([]string{"replay", "--db", db, rec.ID})
		Expect(out.exit).To(Equal(0))
	})

	It("exits 7 on a tampered event payload", func() {
		tamperedDB := filepath.Join(GinkgoT().TempDir(), "t.db")
		st, openErr := store.Open(tamperedDB)
		if openErr != nil {
			panic(openErr)
		}
		ctx := context.Background()
		sess, createErr := st.CreateSession(ctx, "sid-replay-tamper", "p", "m", "h", "sha")
		if createErr != nil {
			panic(createErr)
		}
		w, writerErr := st.NewEventWriter(ctx, sess.ID)
		if writerErr != nil {
			panic(writerErr)
		}
		if _, appendErr := w.Append(ctx, "user", "turn.start", map[string]any{"task": "t"}); appendErr != nil {
			panic(appendErr)
		}
		if closeErr := w.Close(); closeErr != nil {
			panic(closeErr)
		}
		if tpErr := st.TamperEventPayload(ctx, sess.ID, 0, `{"task":"tampered"}`); tpErr != nil {
			panic(tpErr)
		}
		if stCloseErr := st.Close(); stCloseErr != nil {
			panic(stCloseErr)
		}
		Expect(runArgv([]string{"replay", "--db", tamperedDB, sess.ID}).exit).To(Equal(7))
	})
})
