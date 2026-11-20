package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/server"
)

// runOutcome pairs one run's exit code with its stdout.
type runOutcome struct {
	code int
	out  string
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func serveStart() (string, func() int) {
	db := filepath.Join(GinkgoT().TempDir(), "serve.db")
	st, err := app.OpenStore(db)
	must(err)
	_, err = app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "wiring")
	must(err)
	must(st.Close())
	outR, outW, err := os.Pipe()
	must(err)
	exit := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		exit <- dispatchContexted(ctx, []string{"serve", "--addr", "127.0.0.1:0", "--db", db}, outW, outW)
	}()
	line, err := bufio.NewReader(outR).ReadString('\n')
	must(err)
	listen := map[string]string{}
	must(json.Unmarshal([]byte(line), &listen))
	return "http://" + listen["listen"], func() int {
		cancel()
		_ = outW.Close()
		code := <-exit
		return code
	}
}

var _ = Describe("Serve write port wiring", func() {
	seedStore := func() (*app.Store, *app.Session) {
		st, err := app.OpenStore(filepath.Join(GinkgoT().TempDir(), "wiring.db"))
		must(err)
		DeferCleanup(func() { must(st.Close()) })
		sess, err := app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "wiring")
		must(err)
		return st, sess
	}

	It("closes a session through the store-backed port", func() {
		st, sess := seedStore()
		closed, err := servePorts(st).CloseSession(context.Background(), sess.ID)
		must(err)
		Expect(closed.Status).To(Equal("cancelled"))
	})

	It("cancels a session through the store-backed port", func() {
		st, sess := seedStore()
		cancelled, err := servePorts(st).CancelSession(context.Background(), sess.ID)
		must(err)
		Expect(cancelled.ID).To(Equal(sess.ID))
	})

	It("rejects a terminal close through the shared error table", func() {
		st, sess := seedStore()
		_, err := servePorts(st).CloseSession(context.Background(), sess.ID)
		must(err)
		_, err = servePorts(st).CloseSession(context.Background(), sess.ID)
		Expect(servePorts(st).ErrCode(err)).To(Equal("session_terminal"))
	})

	It("diagnoses a session through the store-backed port", func() {
		st, sess := seedStore()
		d, err := servePorts(st).DiagnoseSession(context.Background(), sess.ID)
		must(err)
		Expect(d.Status).To(Equal("active"))
	})

	It("reports the budget overview through the store-backed port", func() {
		st, sess := seedStore()
		b, err := servePorts(st).BudgetOverview(context.Background(), sess.ID)
		must(err)
		Expect(b).To(Equal(server.Budget{SessionID: sess.ID}))
	})

	It("anchors a budget pool through the run budget seam", func() {
		st, sess := seedStore()
		b, err := servePorts(st).SetBudgetPool(context.Background(), sess.ID, 120000)
		must(err)
		want := int64(120000)
		Expect(b).To(Equal(server.Budget{SessionID: sess.ID, Pool: &want, Available: &want}))
	})

	It("maps an unknown session on the pool anchor onto session_not_found", func() {
		st, _ := seedStore()
		_, err := servePorts(st).SetBudgetPool(context.Background(), "01a1f000-0000-4000-8000-0000000000ff", 10)
		Expect(servePorts(st).ErrCode(err)).To(Equal("session_not_found"))
	})
})

var _ = Describe("Serve steering port wiring", func() {
	seedParentChild := func() (*app.Store, *app.Session, *app.Session) {
		st, err := app.OpenStore(filepath.Join(GinkgoT().TempDir(), "steering.db"))
		must(err)
		DeferCleanup(func() { must(st.Close()) })
		parent, err := app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "wiring")
		must(err)
		child, err := app.CreateChildSession(context.Background(), st, parent.ID,
			"z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "child task")
		must(err)
		return st, parent, child
	}

	It("sends a steering message from an ancestor through the store-backed port", func() {
		st, parent, child := seedParentChild()
		m, err := servePorts(st).SendSteeringMessage(context.Background(), child.ID, parent.ID, "steer left")
		must(err)
		Expect(m.SessionID).To(Equal(child.ID))
	})

	It("attributes the sent message to the ancestor sender", func() {
		st, parent, child := seedParentChild()
		m, err := servePorts(st).SendSteeringMessage(context.Background(), child.ID, parent.ID, "steer left")
		must(err)
		Expect(m.Sender).To(Equal(parent.ID))
	})

	// Real behaviour: the app layer wraps delegation-scope failures
	// into ErrUsage, so the shared table answers usage — not 404 — and
	// the endpoint documents and tests that as-is.
	It("maps a delegation-scope rejection onto usage", func() {
		st, _, child := seedParentChild()
		outsider, err := app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "outsider")
		must(err)
		_, err = servePorts(st).SendSteeringMessage(context.Background(), child.ID, outsider.ID, "steer left")
		Expect(servePorts(st).ErrCode(err)).To(Equal("usage"))
	})
})

var _ = Describe("Serve command wiring", Label("e2e"), func() {
	It("serves the seeded session list over a real store", func() {
		base, stop := serveStart()
		resp, err := http.Get(base + "/sessions")
		must(err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		must(err)
		rows := []struct {
			ID string "json:\"id\""
		}{}
		must(json.Unmarshal(body, &rows))
		stop()
		Expect(rows).To(HaveLen(1))
	})
	It("exits 4 config_invalid without serving when the database cannot open", func() {
		r := runArgv([]string{"serve", "--db", GinkgoT().TempDir()})
		Expect(runOutcome{r.exit, r.stdout}).To(Equal(runOutcome{exitConfigInvalid, ""}))
	})
})
