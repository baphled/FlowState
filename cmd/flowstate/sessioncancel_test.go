package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

// seedUnsettleableChild seeds the database at db with a child whose
// reservation cannot be settled (a trigger aborts every reservation
// update) and returns the child's id.
func seedUnsettleableChild(db string) string {
	ctx := context.Background()
	st, err := app.OpenStore(db)
	must(err)
	parent, err := app.CreateSession(ctx, st, "zai", "glm-5.3", app.DefaultHarness, "sha", "parent")
	must(err)
	must(st.SetBudgetPool(ctx, parent.ID, 100))
	child, err := app.CreateChildSession(ctx, st, parent.ID, "zai", "glm-5.3", app.DefaultHarness, "sha", "child")
	must(err)
	must(app.ReserveChildBudget(ctx, st, parent.ID, child.ID, 40))
	must(st.Close())
	raw, err := sql.Open("sqlite", "file:"+db)
	must(err)
	defer func() { must(raw.Close()) }()
	_, err = raw.Exec(`CREATE TRIGGER no_settle BEFORE UPDATE ON budget_reservations ` +
		`BEGIN SELECT RAISE(ABORT, 'settle blocked'); END`)
	must(err)
	return child.ID
}

var _ = Describe("session cancel and SIGINT", func() {
	var dir, db string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		db = filepath.Join(dir, "test.db")
	})

	// newTestSession creates an active session and returns its id.
	newTestSession := func() (string, error) {
		res := runArgv([]string{"session", "new", "--db", db,
			"--provider", "zai", "--model", "glm-5.3", "--task", "hi"})
		if res.exit != 0 {
			return "", errors.New(res.stderr)
		}
		var rec newRecord
		if err := json.Unmarshal([]byte(firstLine(res.stdout)), &rec); err != nil {
			return "", err
		}
		return rec.ID, nil
	}

	It("exits 2 without a session id", func() {
		Expect(runArgv([]string{"session", "cancel", "--db", db}).exit).To(Equal(2))
	})

	It("exits 1 on an unknown session", func() {
		Expect(runArgv([]string{"session", "cancel", "--db", db, "nope"}).exit).To(Equal(1))
	})

	It("cancels an active session with exit 0", func() {
		id, newErr := newTestSession()
		Expect(newErr).NotTo(HaveOccurred())
		Expect(runArgv([]string{"session", "cancel", "--db", db, id}).exit).To(Equal(0))
	})

	It("exits 6 cancelling a cancelled session", func() {
		id, newErr := newTestSession()
		Expect(newErr).NotTo(HaveOccurred())
		_ = runArgv([]string{"session", "cancel", "--db", db, id})
		Expect(runArgv([]string{"session", "cancel", "--db", db, id}).exit).To(Equal(6))
	})

	It("exits 130 on the SIGINT cancel path", func() {
		id, newErr := newTestSession()
		Expect(newErr).NotTo(HaveOccurred())
		ctxMarked := contextWithSigint()
		outR, outW, _ := os.Pipe()
		errR, errW, _ := os.Pipe()
		_ = outW.Close()
		exit := dispatchContext(ctxMarked, []string{"session", "cancel", id, "--db", db}, outW, errW)
		_ = errW.Close()
		_ = outR.Close()
		_ = errR.Close()
		Expect(exit).To(Equal(130))
	})

	unsettleableChild := func() string { return seedUnsettleableChild(db) }

	It("reports a failed budget settle on stderr", func() {
		res := runArgv([]string{"session", "cancel", "--db", db, unsettleableChild()})
		Expect(res.stderr).To(ContainSubstring("reservation_settle_failed"))
	})

	It("exits 1 when the budget settle fails after the cancel", func() {
		res := runArgv([]string{"session", "cancel", "--db", db, unsettleableChild()})
		Expect(res.exit).To(Equal(1))
	})

	It("still reports the cancelled state when the budget settle fails", func() {
		res := runArgv([]string{"session", "cancel", "--db", db, unsettleableChild()})
		Expect(res.stdout).To(ContainSubstring(`"status":"cancelled"`))
	})

	It("keeps exit 130 on the SIGINT path when the budget settle fails", func() {
		id := unsettleableChild()
		errFile, err := os.Create(filepath.Join(dir, "stderr"))
		must(err)
		DeferCleanup(func() { must(errFile.Close()) })
		exit := dispatchContext(contextWithSigint(), []string{"session", "cancel", id, "--db", db}, errFile, errFile)
		Expect(exit).To(Equal(130))
	})
})
