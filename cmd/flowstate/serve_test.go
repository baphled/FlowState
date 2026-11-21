package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
		closed, err := servePorts(st, &flags{}).CloseSession(context.Background(), sess.ID)
		must(err)
		Expect(closed.Status).To(Equal("cancelled"))
	})

	It("cancels a session through the store-backed port", func() {
		st, sess := seedStore()
		cancelled, err := servePorts(st, &flags{}).CancelSession(context.Background(), sess.ID)
		must(err)
		Expect(cancelled.ID).To(Equal(sess.ID))
	})

	It("reports a failed budget settle on cancel as an error naming the cancelled session", func() {
		db := filepath.Join(GinkgoT().TempDir(), "settle.db")
		id := seedUnsettleableChild(db)
		st, err := app.OpenStore(db)
		must(err)
		DeferCleanup(func() { must(st.Close()) })
		_, err = servePorts(st, &flags{}).CancelSession(context.Background(), id)
		Expect(err).To(MatchError(ContainSubstring("is cancelled")))
	})

	It("rejects a terminal close through the shared error table", func() {
		st, sess := seedStore()
		_, err := servePorts(st, &flags{}).CloseSession(context.Background(), sess.ID)
		must(err)
		_, err = servePorts(st, &flags{}).CloseSession(context.Background(), sess.ID)
		Expect(servePorts(st, &flags{}).ErrCode(err)).To(Equal("session_terminal"))
	})

	It("diagnoses a session through the store-backed port", func() {
		st, sess := seedStore()
		d, err := servePorts(st, &flags{}).DiagnoseSession(context.Background(), sess.ID)
		must(err)
		Expect(d.Status).To(Equal("active"))
	})

	// truncatedDiagnosis diagnoses, through the store-backed port, a
	// session whose last turn.end was cut off at the token limit.
	truncatedDiagnosis := func() server.Diagnosis {
		st, sess := seedStore()
		w, werr := st.NewEventWriter(context.Background(), sess.ID)
		must(werr)
		_, aerr := w.Append(context.Background(), "engine", "turn.end",
			map[string]any{"status": "completed", "finish_reason": "length"})
		must(aerr)
		must(w.Close())
		d, err := servePorts(st, &flags{}).DiagnoseSession(context.Background(), sess.ID)
		must(err)
		return d
	}

	It("carries a truncated final turn through the store-backed doctor port", func() {
		Expect(truncatedDiagnosis().Truncated).To(BeTrue())
	})

	It("carries the finish reason through the store-backed doctor port", func() {
		Expect(truncatedDiagnosis().FinishReason).To(Equal("length"))
	})

	It("reports the budget overview through the store-backed port", func() {
		st, sess := seedStore()
		b, err := servePorts(st, &flags{}).BudgetOverview(context.Background(), sess.ID)
		must(err)
		Expect(b).To(Equal(server.Budget{SessionID: sess.ID}))
	})

	It("anchors a budget pool through the run budget seam", func() {
		st, sess := seedStore()
		b, err := servePorts(st, &flags{}).SetBudgetPool(context.Background(), sess.ID, 120000)
		must(err)
		want := int64(120000)
		Expect(b).To(Equal(server.Budget{SessionID: sess.ID, Pool: &want, Available: &want}))
	})

	It("maps an unknown session on the pool anchor onto session_not_found", func() {
		st, _ := seedStore()
		_, err := servePorts(st, &flags{}).SetBudgetPool(context.Background(), "01a1f000-0000-4000-8000-0000000000ff", 10)
		Expect(servePorts(st, &flags{}).ErrCode(err)).To(Equal("session_not_found"))
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
		m, err := servePorts(st, &flags{}).SendSteeringMessage(context.Background(), child.ID, parent.ID, "steer left")
		must(err)
		Expect(m.SessionID).To(Equal(child.ID))
	})

	It("attributes the sent message to the ancestor sender", func() {
		st, parent, child := seedParentChild()
		m, err := servePorts(st, &flags{}).SendSteeringMessage(context.Background(), child.ID, parent.ID, "steer left")
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
		_, err = servePorts(st, &flags{}).SendSteeringMessage(context.Background(), child.ID, outsider.ID, "steer left")
		Expect(servePorts(st, &flags{}).ErrCode(err)).To(Equal("usage"))
	})
})

var _ = Describe("Serve swarm port wiring", func() {
	writeManifest := func(body string) string {
		p := filepath.Join(GinkgoT().TempDir(), "swarm.yaml")
		must(os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	var passingManifest string
	BeforeEach(func() {
		passingManifest = "name: demo.swarm\nscope: demo.swarm\npackets:\n  - id: p1\n    scope: demo.swarm.build\n    task: build it\n    source_sha256: " +
			strings.Repeat("b", 64) + "\ngates:\n  - name: ok\n    description: trivially pass\n    command: true\n"
	})
	seedSwarmParent := func() (*app.Store, *app.Session) {
		st, err := app.OpenStore(filepath.Join(GinkgoT().TempDir(), "swarmport.db"))
		must(err)
		DeferCleanup(func() { must(st.Close()) })
		parent, err := app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "swarm parent")
		must(err)
		return st, parent
	}

	It("validates a manifest through the store-backed port", func() {
		v, err := swarmValidatePort(context.Background(), writeManifest(passingManifest))
		must(err)
		Expect(v).To(Equal(server.SwarmValidation{Name: "demo.swarm", Valid: true}))
	})

	It("maps a manifest validation failure onto usage", func() {
		_, err := swarmValidatePort(context.Background(), writeManifest("scope: demo.swarm\n"))
		Expect(app.ErrorCode(err)).To(Equal("usage"))
	})

	runPassing := func() (*app.Store, *app.Session, server.SwarmRun) {
		st, parent := seedSwarmParent()
		run, err := swarmRunPort(context.Background(), st, parent.ID, writeManifest(passingManifest), 100)
		must(err)
		return st, parent, run
	}

	It("reports the executed manifest through the run port", func() {
		_, _, run := runPassing()
		Expect(run.Manifest).To(Equal("demo.swarm"))
	})

	It("lists the admitted packets through the run port", func() {
		_, _, run := runPassing()
		Expect(run.Admitted).To(Equal([]string{"p1"}))
	})

	It("reports the accepted gate decision through the run port", func() {
		_, _, run := runPassing()
		Expect(run.Accepted).To(BeTrue())
	})

	It("reports one delegated child through the run port", func() {
		_, _, run := runPassing()
		Expect(run.Children).To(HaveLen(1))
	})

	It("labels the child with its manifest packet", func() {
		_, _, run := runPassing()
		Expect(run.Children[0].Packet).To(Equal("p1"))
	})

	It("names the child session created for the packet", func() {
		_, _, run := runPassing()
		Expect(run.Children[0].Session).NotTo(BeEmpty())
	})

	It("creates the child active", func() {
		_, _, run := runPassing()
		Expect(run.Children[0].Status).To(Equal("active"))
	})

	It("attributes the child to the parent session", func() {
		_, parent, run := runPassing()
		Expect(run.Children[0].ParentID).To(Equal(parent.ID))
	})

	It("maps an unknown parent onto session_not_found", func() {
		st, _ := seedSwarmParent()
		_, err := swarmRunPort(context.Background(), st, "01a1f000-0000-4000-8000-0000000000ff",
			writeManifest(passingManifest), 100)
		Expect(app.ErrorCode(err)).To(Equal("session_not_found"))
	})

	It("maps a terminal parent onto session_terminal", func() {
		st, parent := seedSwarmParent()
		_, err := app.CloseSession(context.Background(), st, parent.ID)
		must(err)
		_, err = swarmRunPort(context.Background(), st, parent.ID, writeManifest(passingManifest), 100)
		Expect(app.ErrorCode(err)).To(Equal("session_terminal"))
	})

	// Actual mapping: the app's not-accepted gate error carries no
	// sentinel, so the shared table answers internal — the endpoint
	// renders 500, mirroring the CLI exit path.
	It("reports the gate not-accepted error from the run port", func() {
		st, parent := seedSwarmParent()
		failing := strings.Replace(passingManifest, "command: true", "command: false", 1)
		_, err := swarmRunPort(context.Background(), st, parent.ID, writeManifest(failing), 100)
		Expect(err).To(MatchError(ContainSubstring("not accepted")))
	})

	It("maps a gate-not-accepted run onto internal", func() {
		st, parent := seedSwarmParent()
		failing := strings.Replace(passingManifest, "command: true", "command: false", 1)
		_, err := swarmRunPort(context.Background(), st, parent.ID, writeManifest(failing), 100)
		Expect(app.ErrorCode(err)).To(Equal("internal"))
	})
})

var _ = Describe("Serve receipt port wiring", func() {
	It("reports a valid receipt through the store-backed port", func() {
		p := writeReceipt(validReceipt())
		v, err := receiptPort(context.Background(), p)
		must(err)
		Expect(v).To(Equal(server.ReceiptVerdict{Path: p, Verdict: "valid"}))
	})

	// Mirrors the CLI: an invalid receipt renders the invalid verdict
	// line, not an error; the port's error return stays reserved for
	// transport-level failures through the shared code table.
	It("reports a tampered receipt as the invalid verdict without an error", func() {
		doc := validReceipt()
		doc["repo_state"].(map[string]any)["manifest_sha256"] = "tampered"
		p := writeReceipt(doc)
		v, err := receiptPort(context.Background(), p)
		must(err)
		Expect(v).To(Equal(server.ReceiptVerdict{Path: p, Verdict: "invalid"}))
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

var _ = DescribeTable("serve exposure check",
	func(addr, token string, wantRefused bool) {
		Expect(checkServeExposure(addr, token) != nil).To(Equal(wantRefused))
	},
	Entry("default loopback address without a token", "127.0.0.1:7600", "", false),
	Entry("IPv4 loopback range without a token", "127.9.9.9:1", "", false),
	Entry("IPv6 loopback without a token", "[::1]:7600", "", false),
	Entry("the name localhost without a token", "localhost:7600", "", false),
	Entry("the name LOCALHOST without a token", "LOCALHOST:7600", "", false),
	Entry("all interfaces by empty host without a token", ":8080", "", true),
	Entry("IPv4 wildcard without a token", "0.0.0.0:8080", "", true),
	Entry("IPv6 wildcard without a token", "[::]:8080", "", true),
	Entry("a private address without a token", "192.168.1.5:8080", "", true),
	Entry("a hostname other than localhost without a token", "example.com:8080", "", true),
	Entry("a name that only starts with localhost", "localhost.example.com:8080", "", true),
	Entry("all interfaces with a token", ":8080", "s3cret", false),
	Entry("a private address with a token", "192.168.1.5:8080", "s3cret", false),
	Entry("loopback with a token", "127.0.0.1:7600", "s3cret", false),
)

var _ = Describe("serve startup refusal", func() {
	// refusal runs `serve` on the given address with the given token and
	// returns its exit code and stderr. The port is out of range, so even a
	// regression that skipped the check could not bind an exposed address.
	refusal := func(addr, token string) (int, string) {
		GinkgoT().Setenv(server.TokenEnv, token)
		errFile, err := os.Create(filepath.Join(GinkgoT().TempDir(), "stderr"))
		must(err)
		DeferCleanup(func() { must(errFile.Close()) })
		code := dispatchContexted(context.Background(), []string{"serve", "--addr", addr,
			"--db", filepath.Join(GinkgoT().TempDir(), "refused.db")}, errFile, errFile)
		raw, err := os.ReadFile(errFile.Name())
		must(err)
		return code, string(raw)
	}

	It("exits usage for an exposed address without a token", func() {
		code, _ := refusal(":99999", "")
		Expect(code).To(Equal(2))
	})

	It("names the refusal", func() {
		_, msg := refusal(":99999", "")
		Expect(msg).To(ContainSubstring("refusing to serve"))
	})

	It("names the loopback remedy", func() {
		_, msg := refusal(":99999", "")
		Expect(msg).To(ContainSubstring("bind a loopback address"))
	})

	It("names the token remedy", func() {
		_, msg := refusal(":99999", "")
		Expect(msg).To(ContainSubstring(server.TokenEnv))
	})
})
