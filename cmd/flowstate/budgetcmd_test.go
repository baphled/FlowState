package main

import (
	"context"
	"encoding/json"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("Session budget CLI", Label("e2e"), func() {
	// B1 oracle recorded at base b97401f3 before the reroute through
	// app.BudgetOverview (throwaway binary, temp db): exit 1 and stderr
	// code session_not_found must stay identical after the change —
	// these specs replay both halves at the same process boundary.
	It("exits 1 for an unknown session id", func() {
		db := filepath.Join(GinkgoT().TempDir(), "budget-unknown.db")
		r := e2eRun("", "session", "budget", "01a1f000-0000-4000-8000-000000000000", "--db", db)
		Expect(r.exit).To(Equal(1))
	})

	It("reports stderr code session_not_found for an unknown session id", func() {
		db := filepath.Join(GinkgoT().TempDir(), "budget-unknown.db")
		r := e2eRun("", "session", "budget", "01a1f000-0000-4000-8000-000000000000", "--db", db)
		var e struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(firstLine(r.stderr)), &e); err != nil {
			panic(err)
		}
		Expect(e.Code).To(Equal("session_not_found"))
	})

	It("prints the budget overview JSON shape for a pooled session", func() {
		nr := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "budget overview")
		if nr.exit != 0 {
			panic(nr.stderr)
		}
		id := decodeNew(nr.stdout).ID
		st, err := app.OpenStore(dbFor())
		if err != nil {
			panic(err)
		}
		if err := st.SetBudgetPool(context.Background(), id, 100); err != nil {
			panic(err)
		}
		if err := st.Close(); err != nil {
			panic(err)
		}
		r := runArgv([]string{"session", "budget", id, "--db", dbFor()})
		if r.exit != 0 {
			panic(r.stderr)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
			panic(err)
		}
		Expect(rec).To(Equal(map[string]any{
			"session_id": id, "reserved": float64(0),
			"budget_pool": float64(100), "available": float64(100),
		}))
	})
})
